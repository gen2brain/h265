package hevc

import "errors"

// ErrInvalidEncodeInput means the frame or the options do not describe
// something this encoder can code.
var ErrInvalidEncodeInput = errors.New("hevc: invalid encode input")

// Frame is one 8-bit 4:2:0 picture. StrideY and StrideC are in samples and may
// exceed the width, so a frame can be a window on a larger buffer.
type Frame struct {
	Y, Cb, Cr        []uint8
	StrideY, StrideC int
}

// EncoderOptions configures an [Encoder]. Width and Height must be non-zero
// multiples of 16. QP runs from 1 through 51 and selects 26 when left at zero;
// Lossless codes the samples as PCM instead and ignores QP.
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

	intra  intraEncoder
	planes [3][]uint8
}

func NewEncoder(opts EncoderOptions) (*Encoder, error) {
	if opts.Width <= 0 || opts.Height <= 0 || opts.Width&15 != 0 || opts.Height&15 != 0 ||
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

	src := [3][]uint8{frame.Y, frame.Cb, frame.Cr}
	stride := [3]int{frame.StrideY, frame.StrideC, frame.StrideC}
	width := [3]int{e.width, e.width / 2, e.width / 2}
	height := [3]int{e.height, e.height / 2, e.height / 2}

	for i := range src {
		plane, ok := packPlane(e.planes[i], src[i], stride[i], width[i], height[i])
		if !ok {
			return nil, ErrInvalidEncodeInput
		}

		e.planes[i] = plane
	}

	if e.lossless {
		return encodePCM(e.planes[0], e.planes[1], e.planes[2], e.width, e.height)
	}

	rbsp, err := e.intra.slice(e.planes[0], e.planes[1], e.planes[2], e.width, e.height, e.qp)
	if err != nil {
		return nil, err
	}

	return lossyNALs(e.width, e.height, rbsp), nil
}

// Flush ends the sequence. Every frame is coded on its own, so there is never
// anything held back.
func (e *Encoder) Flush() ([]NALUnit, error) {
	if e == nil {
		return nil, ErrInvalidEncodeInput
	}

	return nil, nil
}

// packPlane gathers a strided plane into dst, growing it only when what it
// already holds is too small.
func packPlane(dst, src []uint8, stride, width, height int) ([]uint8, bool) {
	if stride <= 0 || stride < width || len(src)/stride < height {
		return nil, false
	}

	if cap(dst) < width*height {
		dst = make([]uint8, 0, width*height)
	}

	out := dst[:0]
	for y := range height {
		out = append(out, src[y*stride:y*stride+width]...)
	}

	return out, true
}
