package hevc

import "sync"

type dspContext struct {
	inverseTransform func(coef []int32, n int, dst bool, bitDepth int, extended bool, s *transformScratch)
	transformSkip    func(coef []int32, n int, rotate bool)
	dequant          func(coef []int32, m []uint8, n, qp, bitDepth int, extended bool)

	// addResidual8 is nil unless an implementation is compiled in. It handles
	// n of at least eight.
	addResidual8 func(dst []uint8, stride int, coef []int32, n, shift int)
}

func newDSPGo() *dspContext {
	return &dspContext{
		inverseTransform: inverseTransform,
		transformSkip:    transformSkip,
		dequant:          dequant,
	}
}

var dsp = sync.OnceValue(func() *dspContext {
	d := newDSPGo()
	dspInit(d)

	return d
})
