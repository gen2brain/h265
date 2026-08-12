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

// planarAsm is 8.4.4.2.4 for eight-bit output, nil unless an implementation is
// compiled in. It handles n of at least eight.
var planarAsm func(dst []uint8, stride int, r *refSamples, shift int)

// predUniAsm is 8.5.3.3.4.2 for eight-bit output, nil unless an
// implementation is compiled in. w is a multiple of eight.
var predUniAsm func(dst []uint8, dstStride int, src []int16, srcStride, w, h, shift int)

// mcLumaTapAsm is one direction of the eight-tap interpolation for eight-bit
// samples, nil unless an implementation is compiled in. w is a multiple of
// eight and src points at the first tap.
var mcLumaTapAsm func(dst []int16, dstStride int, src []uint8, srcStride, tapStride, w, h int,
	f *[8]int16)

// mcCopyAsm is the integer-position case of 8.5.3.3.3 for eight-bit samples.
var mcCopyAsm func(dst []int16, dstStride int, src []uint8, srcStride, w, h, shift int)

// mcLumaTapV16Asm is the vertical half of the two-pass interpolation, reading
// the first pass at sixteen bits.
var mcLumaTapV16Asm func(dst []int16, dstStride int, src []int16, srcStride, w, h, shift int,
	f *[8]int32)

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
