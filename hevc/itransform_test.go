package hevc

import (
	"bytes"
	"math"
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
		got, want [32]int32
		scratch   transformScratch
	)

	r := rand.New(rand.NewPCG(1, 2))

	for _, n := range []int{4, 8, 16, 32} {
		for range 2000 {
			in := make([]int32, n)
			for i := range in {
				in[i] = int32(r.IntN(1<<16) - 1<<15)
			}

			idct(got[:n], in[:n], n, &scratch)
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

func TestResidualShiftBits(t *testing.T) {
	for _, bd := range []int{8, 10, 12} {
		if got := residualShiftBits(bd, false); got != 20-bd {
			t.Errorf("bd=%d: %d, want %d", bd, got, 20-bd)
		}

		if got := residualShiftBits(bd, true); got != max(20-bd, 11) {
			t.Errorf("extended bd=%d: %d, want %d", bd, got, max(20-bd, 11))
		}
	}
}

// TestAddResidual checks the fused shift, add and clip against a longhand
// transcription of 8.6.2 and 8.6.6, at both shifts and both bit depths.
func TestAddResidual(t *testing.T) {
	r := rand.New(rand.NewPCG(13, 14))

	for _, bitDepth := range []int{8, 10, 12} {
		for _, n := range []int{4, 8, 16, 32} {
			for _, extended := range []bool{false, true} {
				shift := residualShiftBits(bitDepth, extended)

				stride := n + 3
				maxV := int32(1)<<bitDepth - 1

				coef := make([]int32, n*n)
				for i := range coef {
					coef[i] = int32(r.IntN(1<<22) - 1<<21)
				}

				src := make([]uint16, stride*(n+2))
				for i := range src {
					src[i] = uint16(r.IntN(int(maxV) + 1))
				}

				got := make([]uint16, len(src))
				copy(got, src)
				addResidual(got, stride, 1, 1, n, shift, coef, bitDepth)

				for j := range n {
					for i := range n {
						c := coef[j*n+i]
						if shift > 0 {
							c = (c + 1<<(shift-1)) >> shift
						}

						o := (1+j)*stride + 1 + i
						want := uint16(clip3(int32(src[o])+c, 0, maxV))

						if got[o] != want {
							t.Fatalf("bd=%d n=%d ext=%v [%d,%d] = %d, want %d",
								bitDepth, n, extended, i, j, got[o], want)
						}
					}
				}

				// Everything outside the block must be untouched.
				for i := range src {
					j, k := i/stride, i%stride
					if j >= 1 && j <= n && k >= 1 && k <= n {
						continue
					}

					if got[i] != src[i] {
						t.Fatalf("wrote outside the block at %d", i)
					}
				}
			}
		}
	}
}

// TestAddResidualAsm checks any compiled-in kernel against the Go path, over
// every block size and both shifts, with the block placed at an offset so a
// stride error shows up.
func TestAddResidualAsm(t *testing.T) {
	k := dsp.addResidual8
	if k == nil {
		t.Skip("no assembly for this target")
	}

	r := rand.New(rand.NewPCG(15, 16))

	for _, n := range []int{8, 16, 32} {
		for _, shift := range []int{0, 8, 12} {
			stride := n + 5

			coef := make([]int32, n*n)
			for i := range coef {
				coef[i] = int32(r.IntN(1<<20) - 1<<19)
			}

			src := make([]uint8, stride*(n+3))
			for i := range src {
				src[i] = uint8(r.IntN(256))
			}

			got := make([]uint8, len(src))
			copy(got, src)
			k(got[2*stride+3:], stride, coef, n, shift)

			want := make([]uint8, len(src))
			copy(want, src)
			addResidualGo(want, stride, 3, 2, n, shift, coef, 8)

			if !bytes.Equal(got, want) {
				for i := range got {
					if got[i] != want[i] {
						t.Fatalf("n=%d shift=%d: [%d] = %d, want %d",
							n, shift, i, got[i], want[i])
					}
				}
			}
		}
	}
}

// TestAddResidual16Asm is TestAddResidualAsm for the high bit depth planes,
// where the clamp is the bit depth rather than a fixed 255.
func TestAddResidual16Asm(t *testing.T) {
	k := dsp.addResidual16
	if k == nil {
		t.Skip("no assembly for this target")
	}

	r := rand.New(rand.NewPCG(31, 32))

	for _, bitDepth := range []int{10, 12} {
		maxV := int32(1)<<bitDepth - 1

		for _, n := range []int{8, 16, 32} {
			for _, shift := range []int{0, 8, 20 - bitDepth} {
				stride := n + 5

				coef := make([]int32, n*n)
				for i := range coef {
					coef[i] = int32(r.IntN(1<<20) - 1<<19)
				}

				src := make([]uint16, stride*(n+3))
				for i := range src {
					src[i] = uint16(r.IntN(int(maxV) + 1))
				}

				got := make([]uint16, len(src))
				copy(got, src)
				k(got[2*stride+3:], stride, coef, n, shift, maxV)

				want := make([]uint16, len(src))
				copy(want, src)
				addResidualGo(want, stride, 3, 2, n, shift, coef, bitDepth)

				for i := range got {
					if got[i] != want[i] {
						t.Fatalf("bd=%d n=%d shift=%d: [%d] = %d, want %d",
							bitDepth, n, shift, i, got[i], want[i])
					}
				}
			}
		}
	}
}

// TestIDCTColsAsm checks any compiled-in column kernel against the Go form of
// the same decomposition, over both passes: the first shifts and clips, the
// second neither.
func TestIDCTColsAsm(t *testing.T) {
	k := idctColsAsm
	if k == nil {
		t.Skip("no assembly for this target")
	}

	r := rand.New(rand.NewPCG(41, 42))

	for _, n := range []int{8, 16, 32} {
		for _, pass := range []struct {
			rnd    int32
			shift  int
			lo, hi int32
		}{
			{64, 7, -1 << 15, 1<<15 - 1},
			{0, 0, math.MinInt32, math.MaxInt32},
		} {
			// A block that is dense, and one that is zero above a last
			// significant position, which is what the row skip is for.
			for _, last := range []int{n * n, n * 3} {
				src := make([]int32, n*n)
				for i := range last {
					src[i] = int32(r.IntN(1<<16) - 1<<15)
				}

				got := make([]int32, n*n)
				want := make([]int32, n*n)

				k(got, src, n, pass.rnd, pass.shift, pass.lo, pass.hi)
				idctColsGo(want, src, n, pass.rnd, pass.shift, pass.lo, pass.hi)

				for i := range got {
					if got[i] != want[i] {
						t.Fatalf("n=%d shift=%d last=%d: [%d] = %d, want %d",
							n, pass.shift, last, i, got[i], want[i])
					}
				}
			}
		}
	}
}

// TestIDCTColsMatchesButterfly holds the column decomposition to the recursive
// one, which is what the decoder runs when no kernel is compiled in.
func TestIDCTColsMatchesButterfly(t *testing.T) {
	r := rand.New(rand.NewPCG(43, 44))

	var s transformScratch

	for _, n := range []int{8, 16, 32} {
		for _, bitDepth := range []int{8, 10} {
			for _, last := range []int{n * n, n * 2, 1} {
				src := make([]int32, n*n)
				for i := range last {
					src[i] = int32(r.IntN(1<<16) - 1<<15)
				}

				got := make([]int32, n*n)
				copy(got, src)

				want := make([]int32, n*n)
				copy(want, src)

				rng := transformRange(bitDepth, false)
				lo, hi := int32(-1<<rng), int32(1<<rng-1)

				block, block2 := s.block[:n*n], s.block2[:n*n]

				idctColsGo(block, got, n, 64, 7, lo, hi)
				transposeBlock(block2, block, n)
				idctColsGo(block, block2, n, 0, 0, math.MinInt32, math.MaxInt32)
				transposeBlock(got, block, n)

				save := idctColsAsm
				idctColsAsm = nil
				inverseTransform(want, n, false, bitDepth, false, &s)
				idctColsAsm = save

				for i := range got {
					if got[i] != want[i] {
						t.Fatalf("n=%d bd=%d last=%d: [%d] = %d, want %d",
							n, bitDepth, last, i, got[i], want[i])
					}
				}
			}
		}
	}
}

// TestOddAsm checks any compiled-in butterfly against the Go one, with zero
// coefficients mixed in since a zero skips a basis row.
func TestOddAsm(t *testing.T) {
	if oddAsm == nil {
		t.Skip("no assembly for this target")
	}

	r := rand.New(rand.NewPCG(17, 18))

	for _, c := range []struct{ n, stride int }{{16, 1}} {
		for iter := range 500 {
			in := make([]int32, 2*c.n)
			for i := range in {
				// Later iterations are increasingly sparse.
				if iter%4 != 0 && r.IntN(4) != 0 {
					continue
				}

				in[i] = int32(r.IntN(1<<16) - 1<<15)
			}

			got := make([]int32, c.n)
			want := make([]int32, c.n)

			oddAsm(got, in, c.stride)
			oddGo(want, in, c.stride)

			for i := range got {
				if got[i] != want[i] {
					t.Fatalf("n=%d stride=%d in=%v: [%d] = %d, want %d",
						c.n, c.stride, in, i, got[i], want[i])
				}
			}
		}
	}
}

// TestTransposeAsm checks any compiled-in transpose against the Go one at
// every block size the transform uses.
func TestTransposeAsm(t *testing.T) {
	k := transposeAsm
	if k == nil {
		t.Skip("no assembly for this target")
	}

	r := rand.New(rand.NewPCG(45, 46))

	for _, n := range []int{8, 16, 32} {
		src := make([]int32, n*n)
		for i := range src {
			src[i] = int32(r.IntN(1 << 30))
		}

		got := make([]int32, n*n)
		k(got, src, n)

		for y := range n {
			for x := range n {
				if got[x*n+y] != src[y*n+x] {
					t.Fatalf("n=%d: [%d][%d] = %d, want %d",
						n, x, y, got[x*n+y], src[y*n+x])
				}
			}
		}
	}
}
