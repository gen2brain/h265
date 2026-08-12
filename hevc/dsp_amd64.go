//go:build amd64 && !noasm

package hevc

var hasAVX2 = cpuidAVX2()

func cpuidAVX2() bool

//go:noescape
func addResidual8AVX2(dst *uint8, stride int, coef *int32, n, shift int)

//go:noescape
func odd16AVX2(out *int32, in *int32, m *int8, stride int)

//go:noescape
func predPlanar8AVX2(dst *uint8, stride int, top *int32, left *int32, tr, bl, n, shift int)

//go:noescape
func predUni8AVX2(dst *uint8, dstStride int, src *int16, srcStride, w, h, shift int)

//go:noescape
func mcLumaTap8AVX2(dst *int16, dstStride int, src *uint8, srcStride, tapStride, w, h int, f *int16)

//go:noescape
func mcLumaTapV16AVX2(dst *int16, dstStride int, src *int16, srcStride, w, h, shift int, f *int32)

//go:noescape
func mcCopy8AVX2(dst *int16, dstStride int, src *uint8, srcStride, w, h, shift int)

func dspInit(d *dspContext) {
	if !hasAVX2 {
		return
	}

	d.addResidual8 = func(dst []uint8, stride int, coef []int32, n, shift int) {
		addResidual8AVX2(&dst[0], stride, &coef[0], n, shift)
	}

	oddAsm = func(out, in []int32, stride int) {
		odd16AVX2(&out[0], &in[0], &transMatrix[0][0], stride)
	}

	mcLumaTapAsm = func(dst []int16, dstStride int, src []uint8, srcStride, tapStride, w, h int,
		f *[8]int16,
	) {
		mcLumaTap8AVX2(&dst[0], dstStride, &src[0], srcStride, tapStride, w, h, &f[0])
	}

	mcLumaTapV16Asm = func(dst []int16, dstStride int, src []int16, srcStride, w, h, shift int,
		f *[8]int32,
	) {
		mcLumaTapV16AVX2(&dst[0], dstStride, &src[0], srcStride, w, h, shift, &f[0])
	}

	mcCopyAsm = func(dst []int16, dstStride int, src []uint8, srcStride, w, h, shift int) {
		mcCopy8AVX2(&dst[0], dstStride, &src[0], srcStride, w, h, shift)
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
