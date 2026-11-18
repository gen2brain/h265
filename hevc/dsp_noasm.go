//go:build noasm || (!amd64 && !arm64 && !(riscv64 && riscv64.rva23u64 && go1.26))

package hevc

func dspInit(*dspContext) {}

func predAngularRows([]uint8, int, []int32, int, int) bool { return false }
