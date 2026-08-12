package hevc

import (
	"math/rand/v2"
	"testing"
)

// encodeCoeffAbsLevelRemaining is the inverse of 9.3.3.11, written from the
// binarization rather than from the decoder.
func (e *cabacEncoder) encodeCoeffAbsLevelRemaining(v int32, rice, rng int) {
	unary := func(n int, terminate bool) {
		for range n {
			e.encodeBypass(1)
		}

		if terminate {
			e.encodeBypass(0)
		}
	}

	bits := func(val int32, n int) {
		for i := n - 1; i >= 0; i-- {
			e.encodeBypass(uint32(val>>uint(i)) & 1)
		}
	}

	if v < 3<<rice {
		unary(int(v>>rice), true)
		bits(v&(1<<rice-1), rice)

		return
	}

	k := 0
	for ((int32(1)<<(k+1))+2)<<rice <= v {
		k++
	}

	limit := 32
	if rng > 0 {
		limit = 32 - rng
		k = min(k, limit-3)
	}

	unary(k+3, k+3 < limit)

	suffix := k + rice
	if rng > 0 && k+3 == limit {
		suffix = rng
	}

	bits(v-((int32(1)<<k)+2)<<rice, suffix)
}

func TestCoeffAbsLevelRemaining(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))

	for _, rng := range []int{0, 16, 18, 22} {
		for rice := range 5 {
			vals := []int32{0, 1, 2, 3, 4, 7, 8, 15, 16, 31, 32, 100, 1000, 32767}
			for range 200 {
				vals = append(vals, int32(r.IntN(1<<16)))
			}

			if rng > 0 {
				base := (1<<(29-rng) + 2) << rice
				for range 200 {
					vals = append(vals, int32(base+r.IntN(1<<rng)))
				}
			}

			e := newCabacEncoder(26, sliceI, false)
			for _, v := range vals {
				e.encodeCoeffAbsLevelRemaining(v, rice, rng)
			}

			e.encodeTerminate(1)

			var d cabac
			if err := d.init(e.finish(), 0); err != nil {
				t.Fatal(err)
			}

			for i, want := range vals {
				if got := d.coeffAbsLevelRemaining(rice, rng); got != want {
					t.Fatalf("rng=%d rice=%d value %d: got %d, want %d",
						rng, rice, i, got, want)
				}
			}
		}
	}
}

func TestLastSigCoeffSuffix(t *testing.T) {
	r := rand.New(rand.NewPCG(3, 4))

	for prefix := range 10 {
		if prefix <= 3 {
			var d cabac
			if err := d.init([]byte{0, 0, 0, 0}, 0); err != nil {
				t.Fatal(err)
			}

			if got := d.lastSigCoeffSuffix(prefix); got != prefix {
				t.Fatalf("prefix %d = %d, want %d", prefix, got, prefix)
			}

			continue
		}

		n := prefix>>1 - 1

		for range 20 {
			suffix := int32(r.IntN(1 << n))

			e := newCabacEncoder(26, sliceI, false)
			for i := n - 1; i >= 0; i-- {
				e.encodeBypass(uint32(suffix>>uint(i)) & 1)
			}

			e.encodeTerminate(1)

			var d cabac
			if err := d.init(e.finish(), 0); err != nil {
				t.Fatal(err)
			}

			want := (1<<n)*(2+prefix&1) + int(suffix)
			if got := d.lastSigCoeffSuffix(prefix); got != want {
				t.Fatalf("prefix %d suffix %d: got %d, want %d", prefix, suffix, got, want)
			}
		}
	}
}

func TestScanIndex(t *testing.T) {
	tests := []struct {
		log2Size, cIdx, mode int
		intra                bool
		chroma               uint32
		want                 int
	}{
		{2, 0, 0, true, 1, scanDiag},
		{2, 0, 6, true, 1, scanVer},
		{2, 0, 14, true, 1, scanVer},
		{2, 0, 5, true, 1, scanDiag},
		{2, 0, 15, true, 1, scanDiag},
		{2, 0, 22, true, 1, scanHor},
		{2, 0, 30, true, 1, scanHor},
		{2, 0, 31, true, 1, scanDiag},
		{2, 1, 6, true, 1, scanVer},
		{3, 0, 6, true, 1, scanVer},
		{3, 1, 6, true, 1, scanDiag},
		{3, 1, 6, true, 3, scanVer},
		{4, 0, 6, true, 1, scanDiag},
		{2, 0, 6, false, 1, scanDiag},
	}

	for _, tt := range tests {
		got := scanIndex(tt.log2Size, tt.cIdx, tt.mode, tt.intra, tt.chroma)
		if got != tt.want {
			t.Fatalf("scanIndex(%d, %d, %d, %v, %d) = %d, want %d",
				tt.log2Size, tt.cIdx, tt.mode, tt.intra, tt.chroma, got, tt.want)
		}
	}
}

// naiveSigCoeffCtx transcribes 9.3.4.2.5 without the shared helper's structure.
func naiveSigCoeffCtx(xC, yC, log2Size, cIdx, scanIdx, prevCsbf int) int {
	var sigCtx int

	if log2Size == 2 {
		sigCtx = int(sigCtxMap4x4[(yC<<2)+xC])
	} else if xC+yC == 0 {
		sigCtx = 0
	} else {
		xP := xC & 3
		yP := yC & 3

		switch prevCsbf {
		case 0:
			if xP+yP == 0 {
				sigCtx = 2
			} else if xP+yP < 3 {
				sigCtx = 1
			} else {
				sigCtx = 0
			}
		case 1:
			if yP == 0 {
				sigCtx = 2
			} else if yP == 1 {
				sigCtx = 1
			} else {
				sigCtx = 0
			}
		case 2:
			if xP == 0 {
				sigCtx = 2
			} else if xP == 1 {
				sigCtx = 1
			} else {
				sigCtx = 0
			}
		default:
			sigCtx = 2
		}

		if cIdx == 0 {
			if (xC>>2)+(yC>>2) > 0 {
				sigCtx += 3
			}

			if log2Size == 3 {
				if scanIdx == scanDiag {
					sigCtx += 9
				} else {
					sigCtx += 15
				}
			} else {
				sigCtx += 21
			}
		} else {
			if log2Size == 3 {
				sigCtx += 9
			} else {
				sigCtx += 12
			}
		}
	}

	if cIdx == 0 {
		return sigCtx
	}

	return 27 + sigCtx
}

func TestSigCoeffCtx(t *testing.T) {
	for log2Size := 2; log2Size <= 5; log2Size++ {
		n := 1 << log2Size

		for _, cIdx := range []int{0, 1, 2} {
			for _, scanIdx := range []int{scanDiag, scanHor, scanVer} {
				for prevCsbf := range 4 {
					for yC := range n {
						for xC := range n {
							got := sigCoeffCtx(xC, yC, log2Size, cIdx, scanIdx, prevCsbf)
							want := naiveSigCoeffCtx(xC, yC, log2Size, cIdx, scanIdx, prevCsbf)

							if got != want {
								t.Fatalf("log2=%d cIdx=%d scan=%d csbf=%d (%d,%d): %d, want %d",
									log2Size, cIdx, scanIdx, prevCsbf, xC, yC, got, want)
							}
						}
					}
				}
			}
		}
	}
}

// TestSigCoeffCtxRange holds every derived context inside the block of 44 the
// table allocates to sig_coeff_flag.
func TestSigCoeffCtxRange(t *testing.T) {
	for log2Size := 2; log2Size <= 5; log2Size++ {
		n := 1 << log2Size

		for _, cIdx := range []int{0, 1} {
			for _, scanIdx := range []int{scanDiag, scanHor, scanVer} {
				for prevCsbf := range 4 {
					for yC := range n {
						for xC := range n {
							ctx := sigCoeffCtx(xC, yC, log2Size, cIdx, scanIdx, prevCsbf)
							if ctx < 0 || ctx >= 44 {
								t.Fatalf("log2=%d cIdx=%d (%d,%d) gave context %d",
									log2Size, cIdx, xC, yC, ctx)
							}
						}
					}
				}
			}
		}
	}
}

func TestLastSigCoeffCtx(t *testing.T) {
	luma := map[int][]int{
		2: {0, 1, 2},
		3: {3, 3, 4, 4, 5},
		4: {6, 6, 7, 7, 8, 8, 9},
		5: {10, 10, 11, 11, 12, 12, 13, 13, 14},
	}

	chroma := map[int][]int{
		2: {15, 16, 17},
		3: {15, 15, 16, 16, 17},
		4: {15, 15, 15, 15, 16, 16, 16},
		5: {15, 15, 15, 15, 15, 15, 15, 15, 16},
	}

	for log2Size := 2; log2Size <= 5; log2Size++ {
		cMax := log2Size<<1 - 1

		for binIdx := range cMax {
			if got, want := lastSigCoeffCtx(log2Size, 0, binIdx), luma[log2Size][binIdx]; got != want {
				t.Fatalf("luma log2=%d bin=%d: %d, want %d", log2Size, binIdx, got, want)
			}

			if got, want := lastSigCoeffCtx(log2Size, 1, binIdx), chroma[log2Size][binIdx]; got != want {
				t.Fatalf("chroma log2=%d bin=%d: %d, want %d", log2Size, binIdx, got, want)
			}
		}
	}

	for log2Size := 2; log2Size <= 5; log2Size++ {
		for cIdx := range 2 {
			for binIdx := range log2Size<<1 - 1 {
				if ctx := lastSigCoeffCtx(log2Size, cIdx, binIdx); ctx < 0 || ctx >= 18 {
					t.Fatalf("log2=%d cIdx=%d bin=%d gave context %d", log2Size, cIdx, binIdx, ctx)
				}
			}
		}
	}
}

// TestSigCtxSetMatchesDerivation holds the per-sub-block form the decoder runs
// to 9.3.4.2.5 as written, over every combination there is.
func TestSigCtxSetMatchesDerivation(t *testing.T) {
	for _, log2Size := range []int{2, 3, 4, 5} {
		n := 1 << log2Size

		for cIdx := range 3 {
			for _, scanIdx := range []int{scanDiag, scanHor, scanVer} {
				for prevCsbf := range 4 {
					for yS := 0; yS < n/4; yS++ {
						for xS := 0; xS < n/4; xS++ {
							set := newSigCtxSet(xS, yS, log2Size, cIdx, scanIdx, prevCsbf, false)

							for y := range 4 {
								for x := range 4 {
									xC, yC := xS<<2+x, yS<<2+y

									want := sigCoeffCtx(xC, yC, log2Size, cIdx, scanIdx, prevCsbf)
									if got := set.at(x, y); got != want {
										t.Fatalf("log2=%d cIdx=%d scan=%d csbf=%d sb=(%d,%d) at=(%d,%d): %d, want %d",
											log2Size, cIdx, scanIdx, prevCsbf, xS, yS, x, y, got, want)
									}
								}
							}
						}
					}
				}
			}
		}
	}
}
