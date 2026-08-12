//go:build arm64 && !noasm

package hevc

//go:noescape
func addResidual8NEON(dst *uint8, stride int, coef *int32, n, shift int)

func dspInit(d *dspContext) {
	d.addResidual8 = func(dst []uint8, stride int, coef []int32, n, shift int) {
		addResidual8NEON(&dst[0], stride, &coef[0], n, shift)
	}
}
