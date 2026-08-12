//go:build amd64 && !noasm

package hevc

var hasAVX2 = cpuidAVX2()

func cpuidAVX2() bool

//go:noescape
func addResidual8AVX2(dst *uint8, stride int, coef *int32, n, shift int)

//go:noescape
func addResidual16AVX2(dst *uint16, stride int, coef *int32, n, shift int, maxV int32)

//go:noescape
func odd16AVX2(out *int32, in *int32, m *int8, stride int)

//go:noescape
func predPlanar8AVX2(dst *uint8, stride int, top *int32, left *int32, tr, bl, n, shift int)

//go:noescape
func predUni8AVX2(dst *uint8, dstStride int, src *int16, srcStride, w, h, shift int)

//go:noescape
func mcTap8AVX2(dst *int16, dstStride int, src *uint8, srcStride, tapStride, w, h int, f *int16)

//go:noescape
func mcTap4AVX2(dst *int16, dstStride int, src *uint8, srcStride, tapStride, w, h int, f *int16)

//go:noescape
func mcTapV16x8AVX2(dst *int16, dstStride int, src *int16, srcStride, w, h, shift int, f *int16)

//go:noescape
func mcTapV16x4AVX2(dst *int16, dstStride int, src *int16, srcStride, w, h, shift int, f *int16)

//go:noescape
func mcCopy8AVX2(dst *int16, dstStride int, src *uint8, srcStride, w, h, shift int)

//go:noescape
func predBi8AVX2(dst *uint8, dstStride int, a, b *int16, srcStride, w, h, shift int)

//go:noescape
func idctCols8AVX2(dst, src, m *int32, n, mstride, shift int, rnd, lo, hi int32)

//go:noescape
func transpose8AVX2(dst, src *int32, n int)

//go:noescape
func dequant32AVX2(coef *int32, m *uint8, n int, ls, rnd, sh, lo, hi int32)

func dspInit(d *dspContext) {
	if !hasAVX2 {
		return
	}

	d.addResidual8 = func(dst []uint8, stride int, coef []int32, n, shift int) {
		addResidual8AVX2(&dst[0], stride, &coef[0], n, shift)
	}

	d.addResidual16 = func(dst []uint16, stride int, coef []int32, n, shift int, maxV int32) {
		addResidual16AVX2(&dst[0], stride, &coef[0], n, shift, maxV)
	}

	idctColsAsm = func(dst, src []int32, n int, rnd int32, shift int, lo, hi int32) {
		idctCols8AVX2(&dst[0], &src[0], &transMatrix32[0][0], n, 32*(32/n), shift,
			rnd, lo, hi)
	}

	transposeAsm = func(dst, src []int32, n int) {
		transpose8AVX2(&dst[0], &src[0], n)
	}

	oddAsm = func(out, in []int32, stride int) {
		odd16AVX2(&out[0], &in[0], &transMatrix[0][0], stride)
	}

	mcTapAsm = func(dst []int16, dstStride int, src []uint8, srcStride, tapStride, w, h int,
		f []int16,
	) {
		if len(f) == 8 {
			mcTap8AVX2(&dst[0], dstStride, &src[0], srcStride, tapStride, w, h, &f[0])

			return
		}

		mcTap4AVX2(&dst[0], dstStride, &src[0], srcStride, tapStride, w, h, &f[0])
	}

	mcTapV16Asm = func(dst []int16, dstStride int, src []int16, srcStride, w, h, shift int,
		f []int16,
	) {
		if len(f) == 8 {
			mcTapV16x8AVX2(&dst[0], dstStride, &src[0], srcStride, w, h, shift, &f[0])

			return
		}

		mcTapV16x4AVX2(&dst[0], dstStride, &src[0], srcStride, w, h, shift, &f[0])
	}

	dequant32Asm = func(coef []int32, m []uint8, ls, rnd int32, sh int, lo, hi int32) {
		var mp *uint8
		if m != nil {
			mp = &m[0]
		}

		dequant32AVX2(&coef[0], mp, len(coef), ls, rnd, int32(sh), lo, hi)
	}

	mcCopyAsm = func(dst []int16, dstStride int, src []uint8, srcStride, w, h, shift int) {
		mcCopy8AVX2(&dst[0], dstStride, &src[0], srcStride, w, h, shift)
	}

	predBiAsm = func(dst []uint8, dstStride int, a, b []int16, srcStride, w, h, shift int) {
		predBi8AVX2(&dst[0], dstStride, &a[0], &b[0], srcStride, w, h, shift)
	}

	predUniAsm = func(dst []uint8, dstStride int, src []int16, srcStride, w, h, shift int) {
		predUni8AVX2(&dst[0], dstStride, &src[0], srcStride, w, h, shift)
	}

	planarAsm = func(dst []uint8, stride int, r *refSamples, shift int) {
		n := r.n
		predPlanar8AVX2(&dst[0], stride, &r.s[2*n+1], &r.s[2*n-1],
			int(r.top(n)), int(r.left(n)), n, shift)
	}
}
