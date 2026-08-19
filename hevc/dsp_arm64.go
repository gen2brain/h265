//go:build arm64 && !noasm

package hevc

//go:noescape
func addResidual8NEON(dst *uint8, stride int, coef *int32, n, shift int)

//go:noescape
func addResidual16NEON(dst *uint16, stride int, coef *int32, n, shift int, maxV int32)

//go:noescape
func odd16NEON(out *int32, in *int32, m *int8, stride int)

//go:noescape
func predPlanar8NEON(dst *uint8, stride int, top *int32, left *int32, tr, bl, n, shift int)

//go:noescape
func mcTap8NEON(dst *int16, dstStride int, src *uint8, srcStride, tapStride, w, h int, f *int16)

//go:noescape
func mcTap4NEON(dst *int16, dstStride int, src *uint8, srcStride, tapStride, w, h int, f *int16)

//go:noescape
func mcTapV16x8NEON(dst *int16, dstStride int, src *int16, srcStride, w, h, shift int, f *int16)

//go:noescape
func mcTapV16x4NEON(dst *int16, dstStride int, src *int16, srcStride, w, h, shift int, f *int16)

//go:noescape
func mcCopy8NEON(dst *int16, dstStride int, src *uint8, srcStride, w, h, shift int)

//go:noescape
func predUni8NEON(dst *uint8, dstStride int, src *int16, srcStride, w, h, shift int)

//go:noescape
func predBi8NEON(dst *uint8, dstStride int, a, b *int16, srcStride, w, h, shift int)

//go:noescape
func idctCols4NEON(dst, src, m *int32, n, mstride, shift int, rnd, lo, hi int32)

//go:noescape
func predAngular8NEON(dst *uint8, stride int, ref *int32, angle, n int)

//go:noescape
func sse8NEON(src *uint8, srcStride int, block *uint8, blockStride, n int) int64

//go:noescape
func quantize8NEON(dst, src *int32, count int, scale, offset int32, qbits int)

//go:noescape
func satd16x8NEON(src *uint8, srcStride int, pred *uint8, predStride int) int64

//go:noescape
func transpose4NEON(dst, src *int32, n int)

//go:noescape
func forwardTransform8NEON(dst, src, m *int32, n, shift1, shift2 int)

//go:noescape
func dequant32NEON(coef *int32, m *uint8, n int, ls, rnd, sh, lo, hi int32)

var forwardTransformMatrixNEON = func() [4][32 * 32]int32 {
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
func deblockStrong8NEON(p *uint8, pitch int, tc0, tc1, flags int32)

//go:noescape
func deblockNormal8NEON(p *uint8, pitch int, tc0, tc1, nd, flags int32)

//go:noescape
func turnIn8NEON(dst *uint8, src *uint8, stride int)

//go:noescape
func turnOut8NEON(dst *uint8, stride int, src *uint8)

func dspInit(d *dspContext) {
	d.addResidual8 = func(dst []uint8, stride int, coef []int32, n, shift int) {
		addResidual8NEON(&dst[0], stride, &coef[0], n, shift)
	}

	d.addResidual16 = func(dst []uint16, stride int, coef []int32, n, shift int, maxV int32) {
		addResidual16NEON(&dst[0], stride, &coef[0], n, shift, maxV)
	}

	idctColsAsm = func(dst, src []int32, n int, rnd int32, shift int, lo, hi int32) {
		idctCols4NEON(&dst[0], &src[0], &transMatrix32[0][0], n, 32*(32/n), shift,
			rnd, lo, hi)
	}

	transposeAsm = func(dst, src []int32, n int) {
		transpose4NEON(&dst[0], &src[0], n)
	}

	deblockStrongAsm = func(p []uint8, pitch int, tc0, tc1, flags int32) {
		deblockStrong8NEON(&p[0], pitch, tc0, tc1, flags)
	}

	deblockNormalAsm = func(p []uint8, pitch int, tc0, tc1, nd, flags int32) {
		deblockNormal8NEON(&p[0], pitch, tc0, tc1, nd, flags)
	}

	deblockTurnIn = func(dst, src []uint8, stride int) {
		turnIn8NEON(&dst[0], &src[0], stride)
	}

	deblockTurnOut = func(dst []uint8, stride int, src []uint8) {
		turnOut8NEON(&dst[0], stride, &src[0])
	}

	satd16x8Asm = func(src []uint8, srcStride int, pred []uint8, predStride int) int64 {
		return satd16x8NEON(&src[0], srcStride, &pred[0], predStride)
	}

	sse8Asm = func(src []uint8, srcStride int, block []uint8, blockStride, n int) int64 {
		return sse8NEON(&src[0], srcStride, &block[0], blockStride, n)
	}

	quantize8Asm = func(dst, src []int32, count int, scale, offset int32, qbits int) {
		quantize8NEON(&dst[0], &src[0], count, scale, offset, qbits)
	}

	forwardTransform8Asm = func(dst, src []int32, n int) {
		forwardTransform8NEON(&dst[0], &src[0], &forwardTransformMatrixNEON[log2(n)-2][0],
			n, log2(n)-1, log2(n)+6)
	}

	oddAsm = func(out, in []int32, stride int) {
		odd16NEON(&out[0], &in[0], &transMatrix[0][0], stride)
	}

	mcTapAsm = func(dst []int16, dstStride int, src []uint8, srcStride, tapStride, w, h int,
		f []int16,
	) {
		if len(f) == 8 {
			mcTap8NEON(&dst[0], dstStride, &src[0], srcStride, tapStride, w, h, &f[0])

			return
		}

		mcTap4NEON(&dst[0], dstStride, &src[0], srcStride, tapStride, w, h, &f[0])
	}

	mcTapV16Asm = func(dst []int16, dstStride int, src []int16, srcStride, w, h, shift int,
		f []int16,
	) {
		if len(f) == 8 {
			mcTapV16x8NEON(&dst[0], dstStride, &src[0], srcStride, w, h, shift, &f[0])

			return
		}

		mcTapV16x4NEON(&dst[0], dstStride, &src[0], srcStride, w, h, shift, &f[0])
	}

	dequant32Asm = func(coef []int32, m []uint8, ls, rnd int32, sh int, lo, hi int32) {
		var mp *uint8
		if m != nil {
			mp = &m[0]
		}

		dequant32NEON(&coef[0], mp, len(coef), ls, rnd, int32(sh), lo, hi)
	}

	mcCopyAsm = func(dst []int16, dstStride int, src []uint8, srcStride, w, h, shift int) {
		mcCopy8NEON(&dst[0], dstStride, &src[0], srcStride, w, h, shift)
	}

	predBiAsm = func(dst []uint8, dstStride int, a, b []int16, srcStride, w, h, shift int) {
		predBi8NEON(&dst[0], dstStride, &a[0], &b[0], srcStride, w, h, shift)
	}

	predUniAsm = func(dst []uint8, dstStride int, src []int16, srcStride, w, h, shift int) {
		predUni8NEON(&dst[0], dstStride, &src[0], srcStride, w, h, shift)
	}

	planarAsm = func(dst []uint8, stride int, r *refSamples, shift int) {
		n := r.n
		predPlanar8NEON(&dst[0], stride, &r.s[2*n+1], &r.s[2*n-1],
			int(r.top(n)), int(r.left(n)), n, shift)
	}
}

func predAngularRows(dst []uint8, stride int, ref []int32, angle, n int) bool {
	if n < 8 {
		return false
	}

	predAngular8NEON(&dst[0], stride, &ref[0], angle, n)

	return true
}
