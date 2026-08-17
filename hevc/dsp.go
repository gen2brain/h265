package hevc

type dspContext struct {
	inverseTransform func(coef []int32, n int, dst bool, bitDepth int, extended bool, s *transformScratch)
	transformSkip    func(coef []int32, n int, rotate bool)
	dequant          func(coef []int32, m []uint8, n, qp, bitDepth int, extended bool)

	// addResidual8 and addResidual16 are nil unless an implementation is
	// compiled in. They handle n of at least eight.
	addResidual8  func(dst []uint8, stride int, coef []int32, n, shift int)
	addResidual16 func(dst []uint16, stride int, coef []int32, n, shift int, maxV int32)
}

// oddAsm is the sixteen-wide odd half of 8.6.4.2, nil unless an
// implementation is compiled in. It stands outside dspContext because the
// inverse transform is itself a kernel, so reaching it through dsp would be an
// initialisation cycle.
var oddAsm func(out, in []int32, stride int)

var forwardTransform8Asm func(dst, src []int32, n int)

// planarAsm is 8.4.4.2.4 for eight-bit output, nil unless an implementation is
// compiled in. It handles n of at least eight.
var planarAsm func(dst []uint8, stride int, r *refSamples, shift int)

// predUniAsm and predBiAsm are 8.5.3.3.4.2 for eight-bit output, nil unless an
// implementation is compiled in. w is a multiple of eight.
var (
	predUniAsm func(dst []uint8, dstStride int, src []int16, srcStride, w, h, shift int)
	predBiAsm  func(dst []uint8, dstStride int, a, b []int16, srcStride, w, h, shift int)
)

// The motion compensation kernels of 8.5.3.3.3, nil unless an implementation
// is compiled in. w is a multiple of eight throughout.
//
// mcTapAsm is one direction over eight-bit samples, with src at the first tap;
// mcTapV16Asm is the vertical half of a two-pass filter, reading the first
// pass at sixteen bits. Both take the tap count, so luma's eight-tap and
// chroma's four-tap share them. mcCopyAsm is the integer-position case.
var (
	mcCopyAsm func(dst []int16, dstStride int, src []uint8, srcStride, w, h, shift int)

	mcTapAsm func(dst []int16, dstStride int, src []uint8, srcStride, tapStride, w, h int,
		f []int16)

	mcTapV16Asm func(dst []int16, dstStride int, src []int16, srcStride, w, h, shift int,
		f []int16)
)

// dequant32Asm is 8.6.3 in its 32-bit form, nil unless an implementation is
// compiled in. m may be nil for a flat matrix.
var dequant32Asm func(coef []int32, m []uint8, ls, rnd int32, sh int, lo, hi int32)

// idctColsAsm is one pass of 8.6.4.2 over eight columns at a time, nil unless
// an implementation is compiled in. n is at least eight.
var idctColsAsm func(dst, src []int32, n int, rnd int32, shift int, lo, hi int32)

// transposeAsm transposes an n by n block of int32, nil unless an
// implementation is compiled in. n is a multiple of eight.
var transposeAsm func(dst, src []int32, n int)

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
