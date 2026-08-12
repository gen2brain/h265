//go:build riscv64 && riscv64.rva23u64 && !noasm

package hevc

//go:noescape
func addResidual8RVV(dst *uint8, stride int, coef *int32, n, shift int)

//go:noescape
func odd16RVV(out *int32, in *int32, m *int8, stride int)

func dspInit(d *dspContext) {
	d.addResidual8 = func(dst []uint8, stride int, coef []int32, n, shift int) {
		addResidual8RVV(&dst[0], stride, &coef[0], n, shift)
	}

	oddAsm = func(out, in []int32, stride int) {
		odd16RVV(&out[0], &in[0], &transMatrix[0][0], stride)
	}
}
