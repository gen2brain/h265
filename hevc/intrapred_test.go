package hevc

import (
	"math/rand/v2"
	"testing"
)

func newRef(n int, fill func(i int) int32) *refSamples {
	r := &refSamples{n: n}
	for i := range 4*n + 1 {
		r.s[i] = fill(i)
	}

	return r
}

func TestRefAccessors(t *testing.T) {
	for _, n := range []int{4, 8, 16, 32} {
		r := newRef(n, func(i int) int32 { return int32(i) })

		if got := r.corner(); got != int32(2*n) {
			t.Fatalf("n=%d corner = %d", n, got)
		}

		for x := range 2 * n {
			if got := r.top(x); got != int32(2*n+1+x) {
				t.Fatalf("n=%d top(%d) = %d", n, x, got)
			}
		}

		for y := range 2 * n {
			if got := r.left(y); got != int32(2*n-1-y) {
				t.Fatalf("n=%d left(%d) = %d", n, y, got)
			}
		}
	}
}

func TestSubstitute(t *testing.T) {
	n := 4
	size := 4*n + 1

	r := &refSamples{n: n}
	avail := make([]bool, size)

	r.substitute(avail, 8)

	for i := range size {
		if r.s[i] != 128 {
			t.Fatalf("all-unavailable [%d] = %d, want 128", i, r.s[i])
		}
	}

	r = newRef(n, func(i int) int32 { return int32(100 + i) })
	for i := range avail {
		avail[i] = false
	}

	avail[5] = true

	r.substitute(avail, 8)

	for i := range size {
		if r.s[i] != 105 {
			t.Fatalf("single-available [%d] = %d, want 105", i, r.s[i])
		}
	}

	r = newRef(n, func(i int) int32 { return int32(i) })
	for i := range avail {
		avail[i] = true
	}

	avail[3] = false
	avail[4] = false

	r.substitute(avail, 8)

	if r.s[3] != 2 || r.s[4] != 2 || r.s[5] != 5 {
		t.Fatalf("propagation gave %d %d %d", r.s[3], r.s[4], r.s[5])
	}
}

// TestIntraConstant holds every mode to the one property they all share: a
// constant neighbourhood predicts that constant, since each is a weighted
// average whose weights sum to one.
func TestIntraConstant(t *testing.T) {
	sp := &sps{chromaFormatIDC: 1, strongIntraSmoothing: true}

	for _, n := range []int{4, 8, 16, 32} {
		for _, cIdx := range []int{0, 1} {
			for mode := range 35 {
				for _, v := range []int32{0, 1, 128, 255} {
					r := newRef(n, func(int) int32 { return v })
					filterRef(r, mode, cIdx, 8, sp)

					dst := make([]uint8, n*n)
					intraPredict(dst, 0, n, r, mode, cIdx, 8)

					for i, got := range dst {
						if int32(got) != v {
							t.Fatalf("n=%d cIdx=%d mode=%d const=%d: [%d] = %d",
								n, cIdx, mode, v, i, got)
						}
					}
				}
			}
		}
	}
}

func TestPredDC(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 1))

	for _, n := range []int{4, 8, 16, 32} {
		for _, cIdx := range []int{0, 1} {
			ref := newRef(n, func(int) int32 { return int32(r.IntN(256)) })

			dst := make([]uint8, n*n)
			predDC(dst, 0, n, ref, cIdx)

			var sum int32
			for i := range n {
				sum += ref.top(i) + ref.left(i)
			}

			dc := (sum + int32(n)) >> (log2(n) + 1)

			for y := range n {
				for x := range n {
					want := dc

					if cIdx == 0 && n < 32 {
						switch {
						case x == 0 && y == 0:
							want = (ref.left(0) + 2*dc + ref.top(0) + 2) >> 2
						case y == 0:
							want = (ref.top(x) + 3*dc + 2) >> 2
						case x == 0:
							want = (ref.left(y) + 3*dc + 2) >> 2
						}
					}

					if int32(dst[y*n+x]) != want {
						t.Fatalf("n=%d cIdx=%d [%d][%d] = %d, want %d",
							n, cIdx, x, y, dst[y*n+x], want)
					}
				}
			}
		}
	}
}

func TestPredPlanar(t *testing.T) {
	r := rand.New(rand.NewPCG(2, 2))

	for _, n := range []int{4, 8, 16, 32} {
		ref := newRef(n, func(int) int32 { return int32(r.IntN(256)) })

		dst := make([]uint8, n*n)
		predPlanar(dst, 0, n, ref)

		shift := log2(n) + 1

		for y := range n {
			for x := range n {
				want := (int32(n-1-x)*ref.left(y) + int32(x+1)*ref.top(n) +
					int32(n-1-y)*ref.top(x) + int32(y+1)*ref.left(n) + int32(n)) >> shift

				if int32(dst[y*n+x]) != want {
					t.Fatalf("n=%d [%d][%d] = %d, want %d", n, x, y, dst[y*n+x], want)
				}
			}
		}
	}
}

// TestIntraPureDirections checks the two modes whose output is a plain copy of
// the reference, on chroma where the boundary filter does not apply.
func TestIntraPureDirections(t *testing.T) {
	r := rand.New(rand.NewPCG(3, 3))

	for _, n := range []int{4, 8, 16, 32} {
		ref := newRef(n, func(int) int32 { return int32(r.IntN(256)) })

		dst := make([]uint8, n*n)
		intraPredict(dst, 0, n, ref, intraVer, 1, 8)

		for y := range n {
			for x := range n {
				if int32(dst[y*n+x]) != ref.top(x) {
					t.Fatalf("vertical n=%d [%d][%d] = %d, want %d",
						n, x, y, dst[y*n+x], ref.top(x))
				}
			}
		}

		intraPredict(dst, 0, n, ref, intraHor, 1, 8)

		for y := range n {
			for x := range n {
				if int32(dst[y*n+x]) != ref.left(y) {
					t.Fatalf("horizontal n=%d [%d][%d] = %d, want %d",
						n, x, y, dst[y*n+x], ref.left(y))
				}
			}
		}
	}
}

// TestIntraTranspose checks that modes below 18 are the transpose of their
// mirror above it, which exercises the two index paths against each other.
func TestIntraTranspose(t *testing.T) {
	r := rand.New(rand.NewPCG(4, 4))

	for _, n := range []int{4, 8, 16, 32} {
		for mode := 2; mode < 18; mode++ {
			vals := make([]int32, 4*n+1)
			for i := range vals {
				vals[i] = int32(r.IntN(256))
			}

			a := &refSamples{n: n}
			copy(a.s[:len(vals)], vals)

			b := &refSamples{n: n}
			b.s[2*b.n] = a.corner()

			for i := range 2 * n {
				b.setTop(i, a.left(i))
				b.setLeft(i, a.top(i))
			}

			got := make([]uint8, n*n)
			want := make([]uint8, n*n)

			intraPredict(got, 0, n, a, mode, 1, 8)
			intraPredict(want, 0, n, b, 36-mode, 1, 8)

			for y := range n {
				for x := range n {
					if got[y*n+x] != want[x*n+y] {
						t.Fatalf("n=%d mode=%d [%d][%d]: %d vs transposed %d",
							n, mode, x, y, got[y*n+x], want[x*n+y])
					}
				}
			}
		}
	}
}

func TestFilterFlag(t *testing.T) {
	sp := &sps{chromaFormatIDC: 1}

	if filterFlag(intraDC, 8, 0, sp) {
		t.Fatal("DC filtered")
	}

	if filterFlag(intraPlanar, 4, 0, sp) {
		t.Fatal("4x4 filtered")
	}

	if filterFlag(intraPlanar, 8, 1, sp) {
		t.Fatal("chroma filtered at 4:2:0")
	}

	if !filterFlag(intraPlanar, 8, 0, sp) {
		t.Fatal("planar at 8 not filtered")
	}

	if filterFlag(intraVer, 8, 0, sp) || filterFlag(intraHor, 8, 0, sp) {
		t.Fatal("pure directions filtered at 8")
	}

	if !filterFlag(intraVer+1, 32, 0, sp) {
		t.Fatal("near-vertical at 32 not filtered")
	}

	sp444 := &sps{chromaFormatIDC: 3}
	if !filterFlag(intraPlanar, 8, 1, sp444) {
		t.Fatal("chroma not filtered at 4:4:4")
	}
}

func TestFilterRefSmoothing(t *testing.T) {
	sp := &sps{chromaFormatIDC: 1}

	n := 8
	rnd := rand.New(rand.NewPCG(5, 5))
	r := newRef(n, func(int) int32 { return int32(rnd.IntN(256)) })

	orig := r.s

	filterRef(r, intraPlanar, 0, 8, sp)

	if r.s[0] != orig[0] || r.s[4*n] != orig[4*n] {
		t.Fatal("endpoints were filtered")
	}

	for i := 1; i < 4*n; i++ {
		want := (orig[i-1] + 2*orig[i] + orig[i+1] + 2) >> 2
		if r.s[i] != want {
			t.Fatalf("[%d] = %d, want %d", i, r.s[i], want)
		}
	}

	sp.intraSmoothingDisabled = true

	r2 := &refSamples{n: n}
	r2.s = orig
	filterRef(r2, intraPlanar, 0, 8, sp)

	if r2.s != orig {
		t.Fatal("filtered while smoothing was disabled")
	}
}

func TestFilterRefStrong(t *testing.T) {
	sp := &sps{chromaFormatIDC: 1, strongIntraSmoothing: true}

	n := 32
	r := &refSamples{n: n}

	r.s[2*r.n] = 10

	for i := range 2 * n {
		r.setTop(i, 10+190*int32(i+1)/64)
		r.setLeft(i, 10+63*int32(i+1)/64)
	}

	c, tr, bl := r.corner(), r.top(2*n-1), r.left(2*n-1)

	lim := int32(1) << 3
	if absI32(c+tr-2*r.top(n-1)) >= lim || absI32(c+bl-2*r.left(n-1)) >= lim {
		t.Fatal("fixture does not satisfy the flatness condition")
	}

	filterRef(r, intraPlanar, 0, 8, sp)

	if r.corner() != c {
		t.Fatal("corner changed")
	}

	for i := range 2*n - 1 {
		wantTop := ((63-int32(i))*c + int32(i+1)*tr + 32) >> 6
		wantLeft := ((63-int32(i))*c + int32(i+1)*bl + 32) >> 6

		if r.top(i) != wantTop {
			t.Fatalf("top(%d) = %d, want %d", i, r.top(i), wantTop)
		}

		if r.left(i) != wantLeft {
			t.Fatalf("left(%d) = %d, want %d", i, r.left(i), wantLeft)
		}
	}

	if r.top(2*n-1) != tr || r.left(2*n-1) != bl {
		t.Fatal("far endpoints changed")
	}
}

// 8.4.4.2.6 transcribed independently, indexing p[][] directly rather than
// through a shared reference array.
func naiveAngular(dst []int32, n int, r *refSamples, mode, cIdx, bitDepth int) {
	angle := int(intraPredAngle[mode-2])

	p := func(x, y int) int32 {
		switch {
		case x == -1 && y == -1:
			return r.corner()
		case x == -1:
			return r.left(y)
		default:
			return r.top(x)
		}
	}

	ref := map[int]int32{}

	vertical := mode >= 18

	side := func(i int) int32 {
		if vertical {
			return p(-1, -1+i)
		}

		return p(-1+i, -1)
	}

	main := func(i int) int32 {
		if vertical {
			return p(-1+i, -1)
		}

		return p(-1, -1+i)
	}

	for x := 0; x <= n; x++ {
		ref[x] = main(x)
	}

	if angle < 0 {
		if lim := (n * angle) >> 5; lim < -1 {
			inv := int(intraInvAngle[mode-11])

			for x := lim; x <= -1; x++ {
				ref[x] = side((x*inv + 128) >> 8)
			}
		}
	} else {
		for x := n + 1; x <= 2*n; x++ {
			ref[x] = main(x)
		}
	}

	for b := range n {
		iIdx := ((b + 1) * angle) >> 5
		iFact := ((b + 1) * angle) & 31

		for a := range n {
			var v int32

			if iFact != 0 {
				v = (int32(32-iFact)*ref[a+iIdx+1] + int32(iFact)*ref[a+iIdx+2] + 16) >> 5
			} else {
				v = ref[a+iIdx+1]
			}

			if vertical {
				dst[b*n+a] = v
			} else {
				dst[a*n+b] = v
			}
		}
	}

	if cIdx != 0 || n >= 32 {
		return
	}

	maxV := int32(1)<<bitDepth - 1

	if mode == intraVer {
		for y := range n {
			dst[y*n] = clip3(p(0, -1)+((p(-1, y)-p(-1, -1))>>1), 0, maxV)
		}
	}

	if mode == intraHor {
		for x := range n {
			dst[x] = clip3(p(-1, 0)+((p(x, -1)-p(-1, -1))>>1), 0, maxV)
		}
	}
}

func TestIntraAngular(t *testing.T) {
	rnd := rand.New(rand.NewPCG(6, 6))

	for _, n := range []int{4, 8, 16, 32} {
		for _, cIdx := range []int{0, 1} {
			for mode := 2; mode <= 34; mode++ {
				for range 20 {
					ref := newRef(n, func(int) int32 { return int32(rnd.IntN(256)) })

					got := make([]uint8, n*n)
					intraPredict(got, 0, n, ref, mode, cIdx, 8)

					want := make([]int32, n*n)
					naiveAngular(want, n, ref, mode, cIdx, 8)

					for i := range got {
						if int32(got[i]) != want[i] {
							t.Fatalf("n=%d cIdx=%d mode=%d: [%d] = %d, want %d",
								n, cIdx, mode, i, got[i], want[i])
						}
					}
				}
			}
		}
	}
}

// TestPredPlanarAsm checks any compiled-in planar prediction against the Go
// one, over every size and with the block at an offset so a stride error shows.
func TestPredPlanarAsm(t *testing.T) {
	if planarAsm == nil {
		t.Skip("no assembly for this target")
	}

	rnd := rand.New(rand.NewPCG(19, 20))

	for _, n := range []int{8, 16, 32} {
		for range 200 {
			var r refSamples

			r.n = n
			for i := range 4*n + 1 {
				r.s[i] = int32(rnd.IntN(256))
			}

			stride := n + 7
			off := 2*stride + 3

			got := make([]uint8, stride*(n+4))
			want := make([]uint8, len(got))

			for i := range got {
				got[i] = uint8(rnd.IntN(256))
			}

			copy(want, got)

			planarAsm(got[off:], stride, &r, log2(n)+1)
			predPlanarGo(want, off, stride, &r, n, log2(n)+1)

			for i := range got {
				if got[i] != want[i] {
					t.Fatalf("n=%d: [%d] = %d, want %d", n, i, got[i], want[i])
				}
			}
		}
	}
}

// TestPredAngularAsm holds the kernel to the interpolation it replaces, over
// every vertical mode and size. A negative angle reads the reference before the
// corner, which is why ref is sliced out of a whole array.
func TestPredAngularAsm(t *testing.T) {
	var probe [3 * 32 * 2]int32

	if !predAngularRows(make([]uint8, 64), 8, probe[16:], 32, 8) {
		t.Skip("no assembly for this target")
	}

	rnd := rand.New(rand.NewPCG(31, 32))

	for _, n := range []int{8, 16, 32} {
		var ref [3 * 32 * 2]int32

		for i := range ref {
			ref[i] = int32(rnd.IntN(256))
		}

		base := 2 * n

		for mode := 18; mode <= 34; mode++ {
			angle := intraPredAngle[mode-2]
			stride := n + 7

			got := make([]uint8, stride*n)
			want := make([]uint8, stride*n)

			if !predAngularRows(got, stride, ref[base:], int(angle), n) {
				t.Fatalf("n=%d mode=%d: kernel declined", n, mode)
			}

			for b := range n {
				idx := int(int32(b+1) * angle >> 5)
				fact := int32(b+1) * angle & 31
				row := ref[base+idx+1:]

				for a := range n {
					v := row[a]
					if fact != 0 {
						v = ((32-fact)*row[a] + fact*row[a+1] + 16) >> 5
					}

					want[b*stride+a] = uint8(v)
				}
			}

			for i := range got {
				if got[i] != want[i] {
					t.Fatalf("n=%d mode=%d: [%d] = %d, want %d",
						n, mode, i, got[i], want[i])
				}
			}
		}
	}
}
