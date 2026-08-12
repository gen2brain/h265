//go:build !amd64 || noasm

package hevc

func wideKernels() bool { return false }

func setWideKernels(bool) {}
