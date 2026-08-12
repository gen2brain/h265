//go:build arm64 && !noasm

package hevc

//go:noescape
func addResidual8NEON(dst *uint8, stride int, coef *int32, n, shift int)

//go:noescape
func odd16NEON(out *int32, in *int32, m *int8, stride int)

//go:noescape
func predPlanar8NEON(dst *uint8, stride int, top *int32, left *int32, tr, bl, n, shift int)

//go:noescape
func mcCopy8NEON(dst *int16, dstStride int, src *uint8, srcStride, w, h, shift int)

//go:noescape
func predUni8NEON(dst *uint8, dstStride int, src *int16, srcStride, w, h, shift int)

func dspInit(d *dspContext) {
	d.addResidual8 = func(dst []uint8, stride int, coef []int32, n, shift int) {
		addResidual8NEON(&dst[0], stride, &coef[0], n, shift)
	}

	oddAsm = func(out, in []int32, stride int) {
		odd16NEON(&out[0], &in[0], &transMatrix[0][0], stride)
	}

	mcCopyAsm = func(dst []int16, dstStride int, src []uint8, srcStride, w, h, shift int) {
		mcCopy8NEON(&dst[0], dstStride, &src[0], srcStride, w, h, shift)
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
