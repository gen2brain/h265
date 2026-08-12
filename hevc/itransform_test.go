package hevc

import (
	"math/rand/v2"
	"testing"
)

// 8.6.4.2 as the sum it is defined as.
func naive1D(out, in []int32, n int) {
	stride := 32 / n

	for i := range n {
		var v int32

		for j := range n {
			v += int32(transMatrix[j*stride][i]) * in[j]
		}

		out[i] = v
	}
}

func TestIDCT1D(t *testing.T) {
	var (
		got     [32]int32
		want    [32]int32
		scratch [64]int32
	)

	r := rand.New(rand.NewPCG(1, 2))

	for _, n := range []int{4, 8, 16, 32} {
		for range 2000 {
			in := make([]int32, n)
			for i := range in {
				in[i] = int32(r.IntN(1<<16) - 1<<15)
			}

			idct1D(got[:n], in, n, 32/n, scratch[:])
			naive1D(want[:n], in, n)

			for i := range n {
				if got[i] != want[i] {
					t.Fatalf("n=%d in=%v: out[%d] = %d, want %d", n, in, i, got[i], want[i])
				}
			}
		}
	}
}

func TestTransMatrixReflection(t *testing.T) {
	for n := range 32 {
		if transMatrix[0][n] != 64 {
			t.Fatalf("transMatrix[0][%d] = %d", n, transMatrix[0][n])
		}
	}

	for k := range 32 {
		sign := int8(1)
		if k%2 == 1 {
			sign = -1
		}

		for n := range 32 {
			if transMatrix[k][31-n] != sign*transMatrix[k][n] {
				t.Fatalf("reflection broken at [%d][%d]", k, n)
			}
		}
	}
}

// The basis is near-orthogonal by design, not orthogonal.
func TestTransMatrixOrthogonal(t *testing.T) {
	for _, n := range []int{4, 8, 16, 32} {
		stride := 32 / n

		diag := int32(n) * 64 * 64

		for a := range n {
			for b := range n {
				var dot int32

				for i := range n {
					dot += int32(transMatrix[a*stride][i]) * int32(transMatrix[b*stride][i])
				}

				if a == b {
					if dot < diag-diag/256 || dot > diag+diag/256 {
						t.Fatalf("n=%d row %d has norm %d, want near %d", n, a, dot, diag)
					}

					continue
				}

				if dot < -diag/256 || dot > diag/256 {
					t.Fatalf("n=%d rows %d and %d have dot product %d, want near 0", n, a, b, dot)
				}
			}
		}
	}
}

func TestIDST4(t *testing.T) {
	var got, want [4]int32

	r := rand.New(rand.NewPCG(3, 4))

	for range 2000 {
		var in [4]int32
		for i := range in {
			in[i] = int32(r.IntN(1<<16) - 1<<15)
		}

		idst1D(got[:], in[:])

		for i := range 4 {
			want[i] = 0
			for j := range 4 {
				want[i] += int32(dstMatrix[j][i]) * in[j]
			}
		}

		if got != want {
			t.Fatalf("in=%v: got %v, want %v", in, got, want)
		}
	}
}

// 8.6.4.1 with both passes written out.
func naiveTransform2D(coef []int32, n int, dst bool, bitDepth int, extended bool) {
	rng := transformRange(bitDepth, extended)
	lo, hi := int32(-1<<rng), int32(1<<rng-1)

	col := make([]int32, n)
	out := make([]int32, n)
	mid := make([]int32, n*n)

	for x := range n {
		for y := range n {
			col[y] = coef[y*n+x]
		}

		if dst {
			idst1D(out, col)
		} else {
			naive1D(out, col, n)
		}

		for y := range n {
			mid[y*n+x] = clip3((out[y]+64)>>7, lo, hi)
		}
	}

	for y := range n {
		if dst {
			idst1D(out, mid[y*n:y*n+n])
		} else {
			naive1D(out, mid[y*n:y*n+n], n)
		}

		copy(coef[y*n:y*n+n], out)
	}
}

func TestInverseTransform2D(t *testing.T) {
	var s transformScratch

	r := rand.New(rand.NewPCG(5, 6))

	for _, bitDepth := range []int{8, 10, 12} {
		for _, n := range []int{4, 8, 16, 32} {
			for _, useDST := range []bool{false, true} {
				if useDST && n != 4 {
					continue
				}

				for range 300 {
					got := make([]int32, n*n)
					for i := range got {
						got[i] = int32(r.IntN(1<<16) - 1<<15)
					}

					want := make([]int32, n*n)
					copy(want, got)

					inverseTransform(got, n, useDST, bitDepth, false, &s)
					naiveTransform2D(want, n, useDST, bitDepth, false)

					for i := range got {
						if got[i] != want[i] {
							t.Fatalf("bd=%d n=%d dst=%v: [%d] = %d, want %d",
								bitDepth, n, useDST, i, got[i], want[i])
						}
					}
				}
			}
		}
	}
}

func TestDequant(t *testing.T) {
	r := rand.New(rand.NewPCG(7, 8))

	for _, bitDepth := range []int{8, 10, 12} {
		for _, n := range []int{4, 8, 16, 32} {
			for qp := 0; qp <= 51+6*(bitDepth-8); qp++ {
				coef := make([]int32, n*n)
				m := make([]uint8, n*n)

				for i := range coef {
					coef[i] = int32(r.IntN(1<<17) - 1<<16)
					m[i] = uint8(1 + r.IntN(255))
				}

				want := make([]int32, n*n)

				rng := transformRange(bitDepth, false)
				shift := bitDepth + log2(n) + 10 - rng
				lo, hi := int32(-1<<rng), int32(1<<rng-1)

				// 8.6.3 scales at full precision and clips afterwards, so the
				// product must not be truncated to the coefficient width.
				for i, c := range coef {
					v := int64(c) * int64(m[i]) * (int64(levelScale[qp%6]) << (qp / 6))
					v = (v + 1<<(shift-1)) >> shift

					switch {
					case v < int64(lo):
						want[i] = lo
					case v > int64(hi):
						want[i] = hi
					default:
						want[i] = int32(v)
					}
				}

				got := make([]int32, n*n)
				copy(got, coef)
				dequant(got, m, n, qp, bitDepth, false)

				for i := range got {
					if got[i] != want[i] {
						t.Fatalf("bd=%d n=%d qp=%d: [%d] = %d, want %d",
							bitDepth, n, qp, i, got[i], want[i])
					}
				}

				copy(got, coef)
				dequant(got, nil, n, qp, bitDepth, false)

				for i, c := range coef {
					v := int64(c) * 16 * (int64(levelScale[qp%6]) << (qp / 6))
					v = (v + 1<<(shift-1)) >> shift

					w := int32(v)

					switch {
					case v < int64(lo):
						w = lo
					case v > int64(hi):
						w = hi
					}

					if got[i] != w {
						t.Fatalf("flat bd=%d n=%d qp=%d: [%d] = %d, want %d",
							bitDepth, n, qp, i, got[i], w)
					}
				}
			}
		}
	}
}

func TestLog2(t *testing.T) {
	for k := range 16 {
		if got := log2(1 << k); got != k {
			t.Fatalf("log2(%d) = %d, want %d", 1<<k, got, k)
		}
	}
}

func TestTransformSkip(t *testing.T) {
	r := rand.New(rand.NewPCG(9, 10))

	for _, n := range []int{4, 8, 16, 32} {
		shift := 5 + log2(n)

		in := make([]int32, n*n)
		for i := range in {
			in[i] = int32(r.IntN(1<<16) - 1<<15)
		}

		got := make([]int32, len(in))
		copy(got, in)
		transformSkip(got, n, false)

		for i, c := range in {
			if want := c << shift; got[i] != want {
				t.Fatalf("n=%d [%d] = %d, want %d", n, i, got[i], want)
			}
		}

		copy(got, in)
		transformSkip(got, n, true)

		for i, c := range in {
			if want := c << shift; got[len(in)-1-i] != want {
				t.Fatalf("n=%d rotated [%d] = %d, want %d", n, len(in)-1-i, got[len(in)-1-i], want)
			}
		}
	}
}

func TestResidualShift(t *testing.T) {
	r := rand.New(rand.NewPCG(11, 12))

	for _, bitDepth := range []int{8, 10, 12} {
		in := make([]int32, 64)
		for i := range in {
			in[i] = int32(r.IntN(1<<22) - 1<<21)
		}

		got := make([]int32, len(in))
		copy(got, in)
		residualShift(got, bitDepth, false)

		shift := 20 - bitDepth
		for i, c := range in {
			if want := (c + 1<<(shift-1)) >> shift; got[i] != want {
				t.Fatalf("bd=%d [%d] = %d, want %d", bitDepth, i, got[i], want)
			}
		}
	}
}
