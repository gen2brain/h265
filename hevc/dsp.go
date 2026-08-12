package hevc

type dspContext struct {
	inverseTransform func(coef []int32, n int, dst bool, bitDepth int, extended bool, s *transformScratch)
	transformSkip    func(coef []int32, n int, rotate bool)
	dequant          func(coef []int32, m []uint8, n, qp, bitDepth int, extended bool)

	// addResidual8 is nil unless an implementation is compiled in. It handles
	// n of at least eight.
	addResidual8 func(dst []uint8, stride int, coef []int32, n, shift int)
}

// oddAsm is the sixteen-wide odd half of 8.6.4.2, nil unless an
// implementation is compiled in. It stands outside dspContext because the
// inverse transform is itself a kernel, so reaching it through dsp would be an
// initialisation cycle.
var oddAsm func(out, in []int32, stride int)

func newDSPGo() *dspContext {
	return &dspContext{
		inverseTransform: inverseTransform,
		transformSkip:    transformSkip,
		dequant:          dequant,
	}
}

// dsp is resolved at package initialisation, so reading a kernel is a plain
// field load. The inverse transform reads one per basis row.
var dsp = func() *dspContext {
	d := newDSPGo()
	dspInit(d)

	return d
}()
