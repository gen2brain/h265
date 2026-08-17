//go:build amd64 && !noasm

package hevc

import (
	"math/rand/v2"
	"slices"
	"testing"
)

func wideKernels() bool { return hasAVX512 }

func setWideKernels(v bool) { hasAVX512 = v }

func TestForwardTransform8AVX512(t *testing.T) {
	if !hasAVX512 {
		t.Skip("no AVX-512")
	}

	r := rand.New(rand.NewPCG(51, 52))
	for _, n := range []int{16, 32} {
		for range 500 {
			src := make([]int32, n*n)
			for i := range src {
				src[i] = int32(r.IntN(511) - 255)
			}

			got := make([]int32, n*n)
			want := make([]int32, n*n)
			forwardTransform8AVX512(&got[0], &src[0], &forwardTransformMatrix[log2(n)-2][0],
				n, log2(n)-1, log2(n)+6)
			forwardTransform8Go(want, src, n)

			if !slices.Equal(got, want) {
				t.Fatalf("n=%d src=%v: got %v, want %v", n, src, got, want)
			}
		}
	}
}
