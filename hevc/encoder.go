package hevc

import (
	"errors"
	"runtime"
)

// ErrInvalidEncodeInput means the frame or the options do not describe
// something this encoder can code.
var ErrInvalidEncodeInput = errors.New("hevc: invalid encode input")

// Frame is one 8-bit 4:2:0 picture. StrideY and StrideC are in samples and may
// exceed the width, so a frame can be a window on a larger buffer.
type Frame struct {
	Y, Cb, Cr        []uint8
	StrideY, StrideC int
}

// EncoderOptions configures an [Encoder]. Width and Height must be non-zero and
// even, which is as fine as 4:2:0 chroma resolves; anything the coding tree
// cannot fill is padded away behind a conformance window. QP runs from 1
// through 51 and selects 26 when left at zero; Lossless codes the samples as
// PCM instead and ignores QP.
type EncoderOptions struct {
	Width, Height int
	QP            int
	Lossless      bool
}

// Encoder writes self-contained intra IDR access units. It holds the working
// memory one picture needs and reuses it for the next, so it is not safe for
// concurrent use.
type Encoder struct {
	width, height int
	qp            int
	lossless      bool
	threads       int

	intra  intraEncoder
	planes [3][]uint8
}

// Threads bounds the goroutines coding one picture's rows. Zero and one code
// serially, and only they leave 9.3.1's synchronisation out of the stream.
func (e *Encoder) Threads(n int) {
	if e != nil {
		e.threads = n
	}
}

func NewEncoder(opts EncoderOptions) (*Encoder, error) {
	if opts.Width <= 0 || opts.Height <= 0 || opts.Width&1 != 0 || opts.Height&1 != 0 ||
		opts.QP < 0 || opts.QP > 51 {
		return nil, ErrInvalidEncodeInput
	}

	if opts.QP == 0 {
		opts.QP = 26
	}

	return &Encoder{width: opts.Width, height: opts.Height, qp: opts.QP, lossless: opts.Lossless}, nil
}

// Encode codes one frame as a complete access unit: a video, a sequence and a
// picture parameter set followed by the slice. The NAL units it returns own
// their bitstream and outlive the next call.
func (e *Encoder) Encode(frame Frame) ([]NALUnit, error) {
	if e == nil || frame.StrideY < e.width || frame.StrideC < e.width/2 {
		return nil, ErrInvalidEncodeInput
	}

	cw, ch := codedSize(e.width), codedSize(e.height)
	src := [3][]uint8{frame.Y, frame.Cb, frame.Cr}
	stride := [3]int{frame.StrideY, frame.StrideC, frame.StrideC}
	width := [3]int{e.width, e.width / 2, e.width / 2}
	height := [3]int{e.height, e.height / 2, e.height / 2}
	padded := [3][2]int{{cw, ch}, {cw / 2, ch / 2}, {cw / 2, ch / 2}}

	for i := range src {
		plane, ok := padPlane(e.planes[i], src[i], stride[i], width[i], height[i],
			padded[i][0], padded[i][1])
		if !ok {
			return nil, ErrInvalidEncodeInput
		}

		e.planes[i] = plane
	}

	if e.lossless {
		return e.pcm()
	}

	e.intra.threads = e.waveThreads()

	rbsp, err := e.intra.slice(e.planes[0], e.planes[1], e.planes[2], cw, ch, e.qp)
	if err != nil {
		return nil, err
	}

	return lossyNALs(e.width, e.height, rbsp, e.intra.wavefront), nil
}

func (e *Encoder) pcm() ([]NALUnit, error) {
	cw, ch := codedSize(e.width), codedSize(e.height)
	h := encoderHeaders{
		width: cw, height: ch, cropRight: cw - e.width, cropBottom: ch - e.height,
		levelIDC: pcmLevelIDC(cw * ch), pcm: true,
	}

	return append(h.parameterSets(), NALUnit{Type: NALIdrNLP,
		RBSP: pcmSlice(e.planes[0], e.planes[1], e.planes[2], cw, ch)}), nil
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
func padPlane(dst, src []uint8, stride, width, height, paddedW, paddedH int) ([]uint8, bool) {
	if stride <= 0 || width <= 0 || height <= 0 || stride < width ||
		paddedW < width || paddedH < height || len(src)/stride < height {
		return nil, false
	}

	if cap(dst) < paddedW*paddedH {
		dst = make([]uint8, 0, paddedW*paddedH)
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
