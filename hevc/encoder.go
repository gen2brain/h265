package hevc

import "errors"

var ErrInvalidEncodeInput = errors.New("hevc: invalid encode input")

type Frame struct {
	Y, Cb, Cr        []uint8
	StrideY, StrideC int
}

type EncoderOptions struct {
	Width, Height int
	QP            int
	Lossless      bool
}

type Encoder struct {
	width, height int
	qp            int
	lossless      bool
}

func NewEncoder(opts EncoderOptions) (*Encoder, error) {
	if opts.Width <= 0 || opts.Height <= 0 || opts.Width&15 != 0 || opts.Height&15 != 0 || opts.QP < 0 || opts.QP > 51 {
		return nil, ErrInvalidEncodeInput
	}
	if opts.QP == 0 {
		opts.QP = 26
	}

	return &Encoder{width: opts.Width, height: opts.Height, qp: opts.QP, lossless: opts.Lossless}, nil
}

func (e *Encoder) Encode(frame Frame) ([]NALUnit, error) {
	if e == nil || frame.StrideY < e.width || frame.StrideC < e.width/2 {
		return nil, ErrInvalidEncodeInput
	}

	y, ok := packPlane(frame.Y, frame.StrideY, e.width, e.height)
	if !ok {
		return nil, ErrInvalidEncodeInput
	}

	cb, ok := packPlane(frame.Cb, frame.StrideC, e.width/2, e.height/2)
	if !ok {
		return nil, ErrInvalidEncodeInput
	}

	cr, ok := packPlane(frame.Cr, frame.StrideC, e.width/2, e.height/2)
	if !ok {
		return nil, ErrInvalidEncodeInput
	}

	if e.lossless {
		return encodePCM(y, cb, cr, e.width, e.height)
	}

	return encodeIntraLossyQP(y, cb, cr, e.width, e.height, e.qp)
}

func (e *Encoder) Flush() ([]NALUnit, error) {
	if e == nil {
		return nil, ErrInvalidEncodeInput
	}

	return nil, nil
}

func packPlane(src []uint8, stride, width, height int) ([]uint8, bool) {
	if stride < width || height > 0 && len(src)/stride < height {
		return nil, false
	}

	out := make([]uint8, 0, width*height)
	for y := range height {
		out = append(out, src[y*stride:y*stride+width]...)
	}

	return out, true
}
