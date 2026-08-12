//go:build amd64 && !noasm

package hevc

func wideKernels() bool { return hasAVX512 }

func setWideKernels(v bool) { hasAVX512 = v }
