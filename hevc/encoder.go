package hevc

import (
	"errors"
	"runtime"
)

// ErrInvalidEncodeInput means the frame or the options do not describe
// something this encoder can code.
var ErrInvalidEncodeInput = errors.New("hevc: invalid encode input")

// MaxLumaSamples is MaxLumaPs of Table A.8, which every level from 6.0 up
// shares. A picture larger than this has no level to be coded at, so a caller
// with one splits it over a grid of items instead.
const MaxLumaSamples = 35651584

// Frame is one picture in the [EncoderOptions.Chroma] sampling. StrideY and
// StrideC are in samples and may exceed the width, so a frame can be a window
// on a larger buffer. A monochrome frame leaves the chroma planes nil. Above
// eight bits the samples come in Y16, Cb16 and Cr16 instead.
type Frame struct {
	Y, Cb, Cr        []uint8
	Y16, Cb16, Cr16  []uint16
	StrideY, StrideC int
}

// ChromaFormat is the chroma sampling a picture is coded in, chroma_format_idc
// of 7.4.3.2 by another name. The zero value is 4:2:0.
type ChromaFormat int

const (
	Chroma420 ChromaFormat = iota
	Chroma422
	Chroma444
	ChromaMono
)

// idc is chroma_format_idc, and sub the SubWidthC and SubHeightC of Table 6-1.
func (c ChromaFormat) idc() uint32 {
	switch c {
	case Chroma422:
		return 2
	case Chroma444:
		return 3
	case ChromaMono:
		return 0
	default:
		return 1
	}
}

func (c ChromaFormat) sub() (int, int) {
	switch c {
	case Chroma422:
		return 2, 1
	case Chroma444, ChromaMono:
		return 1, 1
	default:
		return 2, 2
	}
}

// EncoderOptions configures an [Encoder]. Width and Height must be non-zero, a
// multiple of what Chroma resolves, and no more than [MaxLumaSamples] between
// them; anything the coding tree cannot fill is padded away behind a
// conformance window. QP runs from 1 through 51 and selects 26 when left at
// zero; Lossless codes the samples as PCM instead and ignores QP.
type EncoderOptions struct {
	Width, Height int
	QP            int
	Lossless      bool
	// Chroma is the sampling to code in. The zero value is 4:2:0.
	Chroma ChromaFormat
	// BitDepth is the sample size, 8 through 12. The zero value is 8. Above
	// eight the samples come in the sixteen bit planes of [Frame].
	BitDepth int
	// SAO fits the offsets of 8.7.3 to the error left in each coding tree
	// block. It codes the picture twice, for about 2.2x the time, 3.5% of luma
	// bitrate and half a decibel of chroma.
	SAO bool
}

// Encoder writes self-contained intra IDR access units. It holds the working
// memory one picture needs and reuses it for the next, so it is not safe for
// concurrent use.
type Encoder struct {
	width, height int
	qp            int
	lossless      bool
	threads       int
	chroma        ChromaFormat
	subW, subH    int
	bitDepth      int
	sao           bool

	intra    intraEncoder[uint8]
	deep     *intraEncoder[uint16]
	planes   [3][]uint8
	planes16 [3][]uint16
}

// Threads bounds the goroutines coding one picture's rows. More than one turns
// on the synchronisation of 9.3.1, which costs about 2% of bitrate; zero and
// one code serially and leave it out of the stream.
func (e *Encoder) Threads(n int) {
	if e != nil {
		e.threads = n
	}
}

func NewEncoder(opts EncoderOptions) (*Encoder, error) {
	if opts.Chroma < Chroma420 || opts.Chroma > ChromaMono {
		return nil, ErrInvalidEncodeInput
	}

	sw, sh := opts.Chroma.sub()

	if opts.Width <= 0 || opts.Height <= 0 || opts.Width%sw != 0 || opts.Height%sh != 0 ||
		opts.QP < 0 || opts.QP > 51 {
		return nil, ErrInvalidEncodeInput
	}

	if codedSize(opts.Width)*codedSize(opts.Height) > MaxLumaSamples {
		return nil, ErrInvalidEncodeInput
	}

	if opts.QP == 0 {
		opts.QP = 26
	}

	if opts.BitDepth == 0 {
		opts.BitDepth = 8
	}

	if opts.BitDepth < 8 || opts.BitDepth > 12 {
		return nil, ErrInvalidEncodeInput
	}

	return &Encoder{width: opts.Width, height: opts.Height, qp: opts.QP,
		lossless: opts.Lossless, chroma: opts.Chroma, subW: sw, subH: sh,
		bitDepth: opts.BitDepth, sao: opts.SAO}, nil
}

// Encode codes one frame as a complete access unit: a video, a sequence and a
// picture parameter set followed by the slice. The NAL units it returns own
// their bitstream and outlive the next call.
func (e *Encoder) Encode(frame Frame) ([]NALUnit, error) {
	if e == nil {
		return nil, ErrInvalidEncodeInput
	}

	if e.bitDepth <= 8 {
		return encodePicture(e, &e.intra, [3][]uint8{frame.Y, frame.Cb, frame.Cr},
			&e.planes, frame.StrideY, frame.StrideC)
	}

	if e.deep == nil {
		e.deep = new(intraEncoder[uint16])
	}

	return encodePicture(e, e.deep, [3][]uint16{frame.Y16, frame.Cb16, frame.Cr16},
		&e.planes16, frame.StrideY, frame.StrideC)
}

// encodePicture gathers the frame into the coding grid and codes it, either as
// a slice of its own or, without loss, as pulse code modulated blocks.
func encodePicture[P pixel](e *Encoder, enc *intraEncoder[P], src [3][]P,
	planes *[3][]P, strideY, strideC int,
) ([]NALUnit, error) {
	mono := e.chroma == ChromaMono

	if strideY < e.width || (!mono && strideC < e.width/e.subW) {
		return nil, ErrInvalidEncodeInput
	}

	cw, ch := codedSize(e.width), codedSize(e.height)
	sw, sh := e.subW, e.subH
	stride := [3]int{strideY, strideC, strideC}
	width := [3]int{e.width, e.width / sw, e.width / sw}
	height := [3]int{e.height, e.height / sh, e.height / sh}
	padded := [3][2]int{{cw, ch}, {cw / sw, ch / sh}, {cw / sw, ch / sh}}

	n := 3
	if mono {
		n = 1
		planes[1], planes[2] = nil, nil
	}

	for i := range n {
		plane, ok := padPlane(planes[i], src[i], stride[i], width[i], height[i],
			padded[i][0], padded[i][1])
		if !ok {
			return nil, ErrInvalidEncodeInput
		}

		planes[i] = plane
	}

	if e.lossless {
		h := encoderHeaders{
			width: cw, height: ch, cropRight: cw - e.width, cropBottom: ch - e.height,
			chromaFormat: e.chroma.idc(), subWidthC: sw, subHeightC: sh,
			bitDepth: e.bitDepth,
			levelIDC: pcmLevelIDC(cw * ch), pcm: true,
		}

		return append(h.parameterSets(), NALUnit{Type: NALIdrNLP,
			RBSP: pcmSlice(planes[0], planes[1], planes[2], cw, ch, sw, sh, e.bitDepth)}), nil
	}

	enc.bitDepth = e.bitDepth
	enc.wantSAO = e.sao
	enc.threads = e.waveThreads()

	rbsp, err := enc.slice(planes[0], planes[1], planes[2], cw, ch, e.qp)
	if err != nil {
		return nil, err
	}

	return enc.nals(e.width, e.height, rbsp), nil
}

// Flush ends the sequence. Every frame is coded on its own, so there is never
// anything held back.
func (e *Encoder) Flush() ([]NALUnit, error) {
	if e == nil {
		return nil, ErrInvalidEncodeInput
	}

	return nil, nil
}

// padPlane gathers a strided plane into dst at paddedW by paddedH, repeating the
// last column and row to fill what the picture does not reach. dst grows only
// when what it already holds is too small.
func padPlane[P pixel](dst, src []P, stride, width, height, paddedW, paddedH int) ([]P, bool) {
	if stride <= 0 || width <= 0 || height <= 0 || stride < width ||
		paddedW < width || paddedH < height || len(src)/stride < height {
		return nil, false
	}

	if cap(dst) < paddedW*paddedH {
		dst = make([]P, 0, paddedW*paddedH)
	}

	out := dst[:0]

	for y := range height {
		row := src[y*stride : y*stride+width]
		out = append(out, row...)

		for range paddedW - width {
			out = append(out, row[width-1])
		}
	}

	last := out[(height-1)*paddedW : height*paddedW]
	for y := height; y < paddedH; y++ {
		out = append(out, last...)
	}

	return out, true
}

func (e *Encoder) waveThreads() int {
	if e.threads == 0 {
		return 1
	}

	if e.threads < 0 {
		return runtime.GOMAXPROCS(0)
	}

	return e.threads
}
