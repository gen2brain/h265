//go:build amd64 && !noasm

package hevc

var hasAVX2 = cpuidAVX2()

func cpuidAVX2() bool

//go:noescape
func addResidual8AVX2(dst *uint8, stride int, coef *int32, n, shift int)

//go:noescape
func odd16AVX2(out *int32, in *int32, m *int8, stride int)

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
}
