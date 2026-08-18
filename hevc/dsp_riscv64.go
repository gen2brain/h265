//go:build riscv64 && riscv64.rva23u64 && !noasm

package hevc

//go:noescape
func addResidual8RVV(dst *uint8, stride int, coef *int32, n, shift int)

//go:noescape
func addResidual16RVV(dst *uint16, stride int, coef *int32, n, shift int, maxV int32)

//go:noescape
func odd16RVV(out *int32, in *int32, m *int8, stride int)

//go:noescape
func predPlanar8RVV(dst *uint8, stride int, top *int32, left *int32, tr, bl, n, shift int)

//go:noescape
func mcTap8RVV(dst *int16, dstStride int, src *uint8, srcStride, tapStride, w, h, taps int, f *int16)

//go:noescape
func mcTapV16RVV(dst *int16, dstStride int, src *int16, srcStride, w, h, shift, taps int, f *int16)

//go:noescape
func mcCopy8RVV(dst *int16, dstStride int, src *uint8, srcStride, w, h, shift int)

//go:noescape
func predUni8RVV(dst *uint8, dstStride int, src *int16, srcStride, w, h, shift int)

//go:noescape
func predBi8RVV(dst *uint8, dstStride int, a, b *int16, srcStride, w, h, shift int)

//go:noescape
func idctColsRVV(dst, src, m *int32, n, mstride, shift int, rnd, lo, hi int32)

//go:noescape
func predAngular8RVV(dst *uint8, stride int, ref *int32, angle, n int)

//go:noescape
func satd16x8RVV(src *uint8, srcStride int, pred *uint8, predStride int) int64

//go:noescape
func transposeRVV(dst, src *int32, n int)

//go:noescape
func forwardTransform8RVV(dst, src, m *int32, n, shift1, shift2 int)

var forwardTransformMatrixRVV = func() [4][32 * 32]int32 {
	var m [4][32 * 32]int32

	for p, n := range [4]int{4, 8, 16, 32} {
		stride := 32 / n

		for x := range n {
			for k := range n {
				m[p][x*n+k] = int32(transMatrix[k*stride][x])
			}
		}
	}

	return m
}()

//go:noescape
func dequant32RVV(coef *int32, m *uint8, n int, ls, rnd, sh, lo, hi int32)

func dspInit(d *dspContext) {
	d.addResidual8 = func(dst []uint8, stride int, coef []int32, n, shift int) {
		addResidual8RVV(&dst[0], stride, &coef[0], n, shift)
	}

	d.addResidual16 = func(dst []uint16, stride int, coef []int32, n, shift int, maxV int32) {
		addResidual16RVV(&dst[0], stride, &coef[0], n, shift, maxV)
	}

	idctColsAsm = func(dst, src []int32, n int, rnd int32, shift int, lo, hi int32) {
		idctColsRVV(&dst[0], &src[0], &transMatrix32[0][0], n, 32*(32/n), shift,
			rnd, lo, hi)
	}

	transposeAsm = func(dst, src []int32, n int) {
		transposeRVV(&dst[0], &src[0], n)
	}

	satd16x8Asm = func(src []uint8, srcStride int, pred []uint8, predStride int) int64 {
		return satd16x8RVV(&src[0], srcStride, &pred[0], predStride)
	}

	forwardTransform8Asm = func(dst, src []int32, n int) {
		forwardTransform8RVV(&dst[0], &src[0], &forwardTransformMatrixRVV[log2(n)-2][0],
			n, log2(n)-1, log2(n)+6)
	}

	oddAsm = func(out, in []int32, stride int) {
		odd16RVV(&out[0], &in[0], &transMatrix[0][0], stride)
	}

	mcTapAsm = func(dst []int16, dstStride int, src []uint8, srcStride, tapStride, w, h int,
		f []int16,
	) {
		mcTap8RVV(&dst[0], dstStride, &src[0], srcStride, tapStride, w, h, len(f), &f[0])
	}

	mcTapV16Asm = func(dst []int16, dstStride int, src []int16, srcStride, w, h, shift int,
		f []int16,
	) {
		mcTapV16RVV(&dst[0], dstStride, &src[0], srcStride, w, h, shift, len(f), &f[0])
	}

	dequant32Asm = func(coef []int32, m []uint8, ls, rnd int32, sh int, lo, hi int32) {
		var mp *uint8
		if m != nil {
			mp = &m[0]
		}

		dequant32RVV(&coef[0], mp, len(coef), ls, rnd, int32(sh), lo, hi)
	}

	mcCopyAsm = func(dst []int16, dstStride int, src []uint8, srcStride, w, h, shift int) {
		mcCopy8RVV(&dst[0], dstStride, &src[0], srcStride, w, h, shift)
	}

	predBiAsm = func(dst []uint8, dstStride int, a, b []int16, srcStride, w, h, shift int) {
		predBi8RVV(&dst[0], dstStride, &a[0], &b[0], srcStride, w, h, shift)
	}

	predUniAsm = func(dst []uint8, dstStride int, src []int16, srcStride, w, h, shift int) {
		predUni8RVV(&dst[0], dstStride, &src[0], srcStride, w, h, shift)
	}

	planarAsm = func(dst []uint8, stride int, r *refSamples, shift int) {
		n := r.n
		predPlanar8RVV(&dst[0], stride, &r.s[2*n+1], &r.s[2*n-1],
			int(r.top(n)), int(r.left(n)), n, shift)
	}
}

func predAngularRows(dst []uint8, stride int, ref []int32, angle, n int) bool {
	if n < 8 {
		return false
	}

	predAngular8RVV(&dst[0], stride, &ref[0], angle, n)

	return true
}
