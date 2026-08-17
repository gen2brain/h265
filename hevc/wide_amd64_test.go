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

func TestQuantizeAVX2(t *testing.T) {
	if !hasAVX2 {
		t.Skip("no AVX2")
	}

	tests := []struct {
		src           []int32
		scale, offset int64
		qbits         int
	}{
		{
			src:    []int32{-1234567, -78901, -511, -1, 0, 3, 257, 987654},
			scale:  17231,
			offset: 43,
			qbits:  9,
		},
		{
			src:    []int32{1 << 30, -(1 << 30), 123456789, -123456789, 32767, -32767, 1, -1},
			scale:  26214,
			offset: 1 << 26 / 3,
			qbits:  26,
		},
	}

	r := rand.New(rand.NewPCG(61, 62))
	for range 1000 {
		src := make([]int32, 64)
		for i := range src {
			src[i] = int32(r.Uint32())
			if src[i] == -1<<31 {
				src[i]++
			}
		}
		tests = append(tests, struct {
			src           []int32
			scale, offset int64
			qbits         int
		}{src, int64(1 + r.IntN(26214)), int64(r.IntN(1 << 27)), 1 + r.IntN(27)})
	}

	for _, test := range tests {
		got := make([]int32, len(test.src))
		want := make([]int32, len(test.src))
		quantizeAVX2(&got[0], &test.src[0], len(test.src), test.scale, test.offset, test.qbits)

		for i, v := range test.src {
			level := min((int64(v)*test.scale+test.offset)>>uint(test.qbits), int64(0x7fff))
			if v < 0 {
				level = min((-int64(v)*test.scale+test.offset)>>uint(test.qbits), int64(0x7fff))
				level = -level
			}
			want[i] = int32(level)
		}

		if !slices.Equal(got, want) {
			t.Fatalf("src=%v scale=%d offset=%d qbits=%d: got %v, want %v",
				test.src, test.scale, test.offset, test.qbits, got, want)
		}
	}
}

func TestQuantizeAVX2Dispatch(t *testing.T) {
	if !hasAVX2 {
		t.Skip("no AVX2")
	}

	r := rand.New(rand.NewPCG(63, 64))
	for _, n := range []int{4, 8, 16, 32} {
		for _, qp := range []int{0, 18, 26, 42, 51} {
			src := make([]int32, n*n)
			for i := range src {
				src[i] = int32(r.Uint32())
			}
			src[len(src)/2] = -1 << 31

			got := make([]int32, len(src))
			want := make([]int32, len(src))
			quantize(got, src, n, qp, 8)

			qbits := 14 + qp/6 + 15 - 8 - log2(n)
			scale := (int64(1)<<20 + int64(levelScale[qp%6])/2) / int64(levelScale[qp%6])
			offset := int64(1<<uint(qbits)) / 3
			for i, v := range src {
				abs := int64(v)
				if abs < 0 {
					abs = -abs
				}
				level := min((abs*scale+offset)>>uint(qbits), int64(0x7fff))
				if v < 0 {
					level = -level
				}
				want[i] = int32(level)
			}

			if !slices.Equal(got, want) {
				t.Fatalf("n=%d qp=%d: got %v, want %v", n, qp, got, want)
			}
		}
	}
}
