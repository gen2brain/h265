package hevc

import (
	"bytes"
	"math"
	"math/rand/v2"
	"slices"
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

func TestForwardTransformDC(t *testing.T) {
	for _, n := range []int{4, 8, 16, 32} {
		src := make([]int32, n*n)
		for i := range src {
			src[i] = 17
		}
		got := make([]int32, n*n)
		forwardTransform(got, src, n, 8)
		if got[0] != 17<<7 {
			t.Fatalf("n=%d DC = %d, want %d", n, got[0], 17<<7)
		}
		for i, v := range got[1:] {
			if v != 0 {
				t.Fatalf("n=%d AC %d = %d", n, i+1, v)
			}
		}
	}
}

func TestForwardTransform8(t *testing.T) {
	r := rand.New(rand.NewPCG(5, 6))

	for _, n := range []int{4, 8, 16, 32} {
		for range 100 {
			src := make([]int32, n*n)
			for i := range src {
				src[i] = int32(r.IntN(511) - 255)
			}
			got := make([]int32, n*n)
			want := make([]int32, n*n)
			forwardTransform8(got, src, n)
			forwardTransformWide(want, src, n, 8)
			if !slices.Equal(got, want) {
				t.Fatalf("n=%d: got %v, want %v", n, got, want)
			}
		}
	}
}

func TestForwardTransform8Fallback(t *testing.T) {
	save := forwardTransform8Asm
	forwardTransform8Asm = nil
	defer func() { forwardTransform8Asm = save }()

	r := rand.New(rand.NewPCG(47, 48))

	for _, n := range []int{4, 8, 16, 32} {
		for range 100 {
			src := make([]int32, n*n)
			for i := range src {
				src[i] = int32(r.IntN(511) - 255)
			}

			got := make([]int32, n*n)
			want := make([]int32, n*n)
			forwardTransform8(got, src, n)
			forwardTransform8Go(want, src, n)

			if !slices.Equal(got, want) {
				t.Fatalf("n=%d: got %v, want %v", n, got, want)
			}
		}
	}
}

func TestForwardTransform8Asm(t *testing.T) {
	k := forwardTransform8Asm
	if k == nil {
		t.Skip("no assembly for this target")
	}

	r := rand.New(rand.NewPCG(49, 50))

	for _, n := range []int{4, 8, 16, 32} {
		check := func(src []int32) {
			t.Helper()

			got := make([]int32, n*n)
			want := make([]int32, n*n)
			k(got, src, n)
			forwardTransform8Go(want, src, n)

			if !slices.Equal(got, want) {
				t.Fatalf("n=%d src=%v: got %v, want %v", n, src, got, want)
			}
		}

		for range 500 {
			src := make([]int32, n*n)
			for i := range src {
				src[i] = int32(r.IntN(511) - 255)
			}

			check(src)
		}

		for _, v := range []int32{-255, 255} {
			src := make([]int32, n*n)
			for i := range src {
				src[i] = v
			}

			check(src)
		}

		src := make([]int32, n*n)
		for i := range src {
			if (i/n+i%n)%2 == 0 {
				src[i] = -255
			} else {
				src[i] = 255
			}
		}

		check(src)
	}
}

func TestForwardTransform8WideInput(t *testing.T) {
	src := make([]int32, 16)
	src[0] = 1 << 20
	src[1] = -(1 << 20)
	got := make([]int32, len(src))
	want := make([]int32, len(src))

	save := forwardTransform8Asm
	called := false
	forwardTransform8Asm = func([]int32, []int32, int) { called = true }
	defer func() { forwardTransform8Asm = save }()

	forwardTransform(got, src, 4, 8)
	forwardTransformWide(want, src, 4, 8)
	if called {
		t.Fatal("dispatched unsafe input to assembly")
	}

	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestForwardTransformDST4(t *testing.T) {
	r := rand.New(rand.NewPCG(53, 54))
	for _, bitDepth := range []int{8, 10, 12} {
		for range 100 {
			src := make([]int32, 16)
			for i := range src {
				src[i] = int32(r.IntN(1<<bitDepth) - 1<<(bitDepth-1))
			}
			got := make([]int32, 16)
			want := make([]int32, 16)
			var mid [16]int32

			for y := range 4 {
				for k := range 4 {
					var sum int32
					for x := range 4 {
						sum += int32(dstMatrix[k][x]) * src[y*4+x]
					}
					mid[y*4+k] = (sum + 1<<uint(bitDepth-8)) >> uint(bitDepth-8+1)
				}
			}

			for k := range 4 {
				for v := range 4 {
					var sum int32
					for y := range 4 {
						sum += int32(dstMatrix[v][y]) * mid[y*4+k]
					}
					want[v*4+k] = (sum + 128) >> 8
				}
			}

			forwardTransformDST4(got, src, bitDepth)
			if !slices.Equal(got, want) {
				t.Fatalf("bd=%d src=%v: got %v, want %v", bitDepth, src, got, want)
			}
		}
	}
}

func TestQuantizeDC(t *testing.T) {
	for _, n := range []int{4, 8, 16, 32} {
		for _, qp := range []int{0, 18, 26, 42, 51} {
			src := make([]int32, n*n)
			src[0] = 1 << 20
			level := make([]int32, n*n)
			quantize(level, src, n, qp, 8)
			if level[0] == 0 {
				t.Fatalf("n=%d qp=%d quantized DC to zero", n, qp)
			}

			dequant(level, nil, n, qp, 8, false)
			if level[0] <= 0 {
				t.Fatalf("n=%d qp=%d dequantized DC = %d", n, qp, level[0])
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
	eachWidth(t, func(t *testing.T) {
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
	})
}

// TestLevelError holds the constant that turns a coefficient's rounding error
// into picture error to what the dequantiser and the inverse transform actually
// do with one level of the quantiser.
func TestLevelError(t *testing.T) {
	var sc transformScratch

	shift := residualShiftBits(8, false)

	for _, n := range []int{4, 8, 16, 32} {
		for _, qp := range []int{12, 20, 26, 33, 40, 51} {
			coef := make([]int32, n*n)
			coef[0] = 1

			dequant(coef, nil, n, qp, 8, false)
			inverseTransform(coef, n, false, 8, false, &sc)

			var want float64

			for _, v := range coef {
				d := float64(v) / float64(int32(1)<<shift)
				want += d * d
			}

			_, qbits := quantScale(n, qp, 8)
			got := float64(levelError(int64(1)<<qbits, qbits-15, levelDist(qp))) / (1 << 12) / (1 << 30)

			// The dequantiser rounds its step to an integer, which is where
			// the last few percent go.
			if math.Abs(got-want) > 0.05*want {
				t.Fatalf("n=%d qp=%d: level costs %g, the transform makes %g", n, qp, got, want)
			}
		}
	}
}

// TestRDOQLevels holds the quantiser to what it is allowed to do: a level is
// either what rounding would give, one less, or nothing at all. The
// coefficients are spread across the rounding boundary in sixteenths of a
// level, which is where the choice is a real one.
func TestRDOQLevels(t *testing.T) {
	// A 4x4 is a single sub-block, so a level that goes to zero there went for
	// its own cost rather than with the sub-block around it.
	dropped, lowered := 0, 0

	for _, n := range []int{4, 8, 16, 32} {
		for _, qp := range []int{6, 26, 45} {
			scale, qbits := quantScale(n, qp, 8)

			raw := make([]int32, n*n)
			for i := range raw {
				raw[i] = int32(int64(i%32) << qbits / 16 / scale)
				if i&1 != 0 {
					raw[i] = -raw[i]
				}
			}

			var e intraEncoder

			e.reset(make([]uint8, 64*64), make([]uint8, 32*32), make([]uint8, 32*32), 64, 64, qp)

			coef := make([]int32, n*n)
			e.rdoq(coef, raw, n, qp, 0, intraDC)

			for i, v := range coef {
				abs := int64(raw[i])
				if abs < 0 {
					abs = -abs
				}

				nearest := int32((abs*scale + int64(1)<<(qbits-1)) >> qbits)

				if v < 0 != (raw[i] < 0) && v != 0 {
					t.Fatalf("n=%d qp=%d level %d: sign flipped", n, qp, i)
				}

				if m := absLevel(v); m != 0 && m != nearest && m != nearest-1 {
					t.Fatalf("n=%d qp=%d level %d: %d, rounding gives %d", n, qp, i, m, nearest)
				}

				if v == 0 && nearest != 0 && n == 4 {
					dropped++
				}

				if absLevel(v) == nearest-1 && nearest >= 2 {
					lowered++
				}
			}
		}
	}

	if dropped == 0 {
		t.Fatal("no level was ever worth dropping on its own")
	}

	if lowered == 0 {
		t.Fatal("no level was ever worth coding one lower")
	}
}

// TestHadamard8 holds the transform to its defining property: applied twice it
// is eight times what it started as.
// TestRDOQTruncates covers where the block is made to end. A lone level far out
// along the scan costs a long last_sig_coeff to point at and is worth less than
// it costs; one that carries real weight is not.
func TestRDOQTruncates(t *testing.T) {
	const (
		n  = 8
		qp = 26
	)

	scale, qbits := quantScale(n, qp, 8)

	for _, c := range []struct {
		name string
		tail int64
		want bool
	}{
		{"faint", 15, false},
		{"strong", 25, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			raw := make([]int32, n*n)
			raw[0] = int32(int64(12) << qbits / 10 / scale)
			raw[n*n-1] = int32(c.tail << qbits / 10 / scale)

			var e intraEncoder

			e.reset(make([]uint8, 64*64), make([]uint8, 32*32), make([]uint8, 32*32), 64, 64, qp)

			coef := make([]int32, n*n)
			e.rdoq(coef, raw, n, qp, 0, intraDC)

			if coef[0] == 0 {
				t.Fatal("the coefficient carrying the block went too")
			}

			if kept := coef[n*n-1] != 0; kept != c.want {
				t.Fatalf("tail kept = %v, want %v", kept, c.want)
			}
		})
	}
}

// TestRDOQTruncatesRun covers a whole run of levels at the end of the scan.
// Faint ones go together; ones that each carry their own bits stay, which is
// what stops the block being cut back too far.
func TestRDOQTruncatesRun(t *testing.T) {
	const (
		n  = 8
		qp = 26
	)

	scale, qbits := quantScale(n, qp, 8)

	for _, c := range []struct {
		name string
		tail int64
		want int
	}{
		{"faint", 6, 0},
		{"worthwhile", 10, 16},
	} {
		t.Run(c.name, func(t *testing.T) {
			raw := make([]int32, n*n)
			raw[0] = int32(int64(40) << qbits / 10 / scale)

			// The bottom right 4x4 is the last sub-block of the diagonal scan.
			for y := n / 2; y < n; y++ {
				for x := n / 2; x < n; x++ {
					raw[y*n+x] = int32(c.tail << qbits / 10 / scale)
				}
			}

			var e intraEncoder

			e.reset(make([]uint8, 64*64), make([]uint8, 32*32), make([]uint8, 32*32), 64, 64, qp)

			coef := make([]int32, n*n)
			e.rdoq(coef, raw, n, qp, 0, intraDC)

			if coef[0] == 0 {
				t.Fatal("the coefficient carrying the block went too")
			}

			live := 0

			for y := n / 2; y < n; y++ {
				for x := n / 2; x < n; x++ {
					if coef[y*n+x] != 0 {
						live++
					}
				}
			}

			if live != c.want {
				t.Fatalf("%d of the tail survived, want %d", live, c.want)
			}
		})
	}
}

func TestHadamard8(t *testing.T) {
	r := rand.New(rand.NewPCG(31, 32))

	for range 64 {
		var v, want [8]int32

		for i := range v {
			v[i] = int32(r.IntN(2001) - 1000)
			want[i] = 8 * v[i]
		}

		hadamard8(&v)
		hadamard8(&v)

		if v != want {
			t.Fatalf("twice over gives %v, want %v", v, want)
		}
	}
}

// TestSATD holds the sum to what the transform of a flat difference is: the
// whole of it lands in the DC of each 8x8, which is 64 times the difference.
func TestSATD(t *testing.T) {
	const n = 16

	var e intraEncoder

	e.reset(make([]uint8, n*n), make([]uint8, n*n/4), make([]uint8, n*n/4), n, n, 26)

	pred := make([]uint8, n*n)

	for _, c := range []int{0, 1, 7, 100} {
		for i := range pred {
			pred[i] = uint8(c)
		}

		want := int64(n / 8 * (n / 8) * 64 * c)
		if got := e.satd(0, 0, pred, n, 1<<62); got != want {
			t.Fatalf("flat difference of %d: %d, want %d", c, got, want)
		}

		// A limit may only cut the sum short once it has been reached.
		if got := e.satd(0, 0, pred, n, want+1); got != want {
			t.Fatalf("limit above the sum of %d: %d, want %d", c, got, want)
		}

		if c == 0 {
			continue
		}

		if got := e.satd(0, 0, pred, n, want/2); got < want/2 {
			t.Fatalf("limit of %d cut the sum to %d, below the limit", want/2, got)
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
