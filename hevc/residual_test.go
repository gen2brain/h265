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

			var bits putBits
			var e cabacWriter
			e.init(&bits, 26, sliceI, false)
			for _, v := range vals {
				e.encodeCoeffAbsLevelRemaining(v, rice, rng)
			}

			e.encodeTerminate(1)

			var d cabac
			if err := d.init(e.bytes(), 0); err != nil {
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

func TestTerminalRDOQ(t *testing.T) {
	const n = 8
	mode := intraVer
	scan := scanOrder[2][scanIndex(3, 0, mode, true, 1)]
	index := func(pos scanPos) int { return int(pos.y)*n + int(pos.x) }

	t.Run("removes terminal level", func(t *testing.T) {
		raw := make([]int32, n*n)
		level := make([]int32, n*n)
		level[index(scan[13])] = 2
		level[index(scan[14])] = -1
		raw[index(scan[14])] = 1
		terminalRDOQ(raw, level, n, mode, 0, 0)
		if level[index(scan[14])] != 0 {
			t.Fatal("terminal level was not removed")
		}
		if raw[index(scan[14])] != 1 {
			t.Fatal("raw coefficient changed")
		}
	})

	t.Run("keeps ineligible terminal levels", func(t *testing.T) {
		for _, test := range []struct {
			name string
			raw  int32
			last int32
		}{
			{"level", 1, 2},
			{"distortion", 100, 1},
		} {
			t.Run(test.name, func(t *testing.T) {
				raw := make([]int32, n*n)
				level := make([]int32, n*n)
				level[index(scan[13])] = 2
				level[index(scan[14])] = test.last
				raw[index(scan[14])] = test.raw
				terminalRDOQ(raw, level, n, mode, 0, 0)
				if level[index(scan[14])] != test.last {
					t.Fatalf("got %d, want %d", level[index(scan[14])], test.last)
				}
			})
		}
	})

	t.Run("keeps terminal level in another group", func(t *testing.T) {
		sbScan := scanOrder[1][scanIndex(3, 0, mode, true, 1)]
		raw := make([]int32, n*n)
		level := make([]int32, n*n)
		prevSB, prevPos := sbScan[0], scan[15]
		lastSB, lastPos := sbScan[1], scan[1]
		prev := ((int(prevSB.y)<<2)+int(prevPos.y))*n + (int(prevSB.x) << 2) + int(prevPos.x)
		last := ((int(lastSB.y)<<2)+int(lastPos.y))*n + (int(lastSB.x) << 2) + int(lastPos.x)
		level[prev], level[last], raw[last] = 2, 1, 1
		terminalRDOQ(raw, level, n, mode, 0, 0)
		if level[last] != 1 {
			t.Fatal("terminal level in another group was removed")
		}
	})
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

func TestEncodeResidualRoundTrip(t *testing.T) {
	tests := []residualBlock{
		{log2Size: 2, cIdx: 0},
		{log2Size: 2, cIdx: 1, predModeIntra: 22, intra: true},
		{log2Size: 2, cIdx: 0, predModeIntra: 6, intra: true},
		{log2Size: 3, cIdx: 2},
		{log2Size: 4, cIdx: 0},
		{log2Size: 5, cIdx: 0},
	}

	for testIdx, b := range tests {
		r := rand.New(rand.NewPCG(uint64(testIdx+11), uint64(testIdx+37)))
		n := 1 << b.log2Size
		for trial := range 8 {
			want := make([]int32, n*n)
			for i := range want {
				if r.IntN(5) == 0 {
					level := int32(1 + r.IntN(12))
					if r.IntN(7) == 0 {
						level = int32(100 + r.IntN(30000))
					}
					if r.IntN(2) != 0 {
						level = -level
					}
					want[i] = level
				}
			}
			want[r.IntN(len(want))] = int32(1 + r.IntN(3))

			s := sps{chromaFormatIDC: 1}
			sh := sliceHeader{sliceType: sliceI}
			var bits putBits
			var w cabacWriter
			w.init(&bits, 26, sh.sliceType, false)
			var stat [4]uint8
			if err := encodeResidual(&w, &s, &pps{}, &sh, want, b, &stat); err != nil {
				t.Fatalf("case %d trial %d: encode: %v", testIdx, trial, err)
			}
			w.encodeTerminate(1)

			var c cabac
			if err := c.init(w.bytes(), 0); err != nil {
				t.Fatal(err)
			}
			c.initContexts(26, sh.sliceType, false)
			got := make([]int32, len(want))
			if _, err := decodeResidual(&c, &s, &pps{}, &sh, got, b, &stat); err != nil {
				t.Fatalf("case %d trial %d: decode: %v", testIdx, trial, err)
			}
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("case %d trial %d level %d: got %d, want %d",
						testIdx, trial, i, got[i], want[i])
				}
			}
			if c.decodeTerminate() != 1 {
				t.Fatalf("case %d trial %d: missing termination", testIdx, trial)
			}
		}
	}
}

// TestEncodeResidualEmptyDCSubBlock covers a block whose first sub-block holds
// no level at all. 7.3.8.11 codes it anyway, with its coded_sub_block_flag
// inferred, so the encoder has to write sixteen zero significance flags and
// nothing else.
func TestEncodeResidualEmptyDCSubBlock(t *testing.T) {
	for _, b := range []residualBlock{
		{log2Size: 3},
		{log2Size: 3, cIdx: 1, predModeIntra: 22, intra: true},
		{log2Size: 4, predModeIntra: 6, intra: true},
		{log2Size: 5},
	} {
		n := 1 << b.log2Size
		want := make([]int32, n*n)
		want[(n/2+1)*n+n/2+1] = 3
		want[(n/2+2)*n+n/2+2] = -2

		s := sps{chromaFormatIDC: 1}
		p := pps{}

		var (
			bits putBits
			w    cabacWriter
			stat [4]uint8
		)

		w.init(&bits, 26, sliceI, false)

		if err := encodeResidual(&w, &s, &p, &sliceHeader{sliceType: sliceI}, want, b, &stat); err != nil {
			t.Fatalf("size %d cIdx %d: encode: %v", n, b.cIdx, err)
		}

		w.encodeTerminate(1)

		var c cabac
		if err := c.init(w.bytes(), 0); err != nil {
			t.Fatal(err)
		}

		c.initContexts(26, sliceI, false)

		got := make([]int32, len(want))
		if _, err := decodeResidual(&c, &s, &p, &sliceHeader{sliceType: sliceI}, got, b, &stat); err != nil {
			t.Fatalf("size %d cIdx %d: decode: %v", n, b.cIdx, err)
		}

		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("size %d cIdx %d level %d: got %d, want %d", n, b.cIdx, i, got[i], want[i])
			}
		}
	}
}

func TestEncodeResidualIntraModesRoundTrip(t *testing.T) {
	for mode := intraPlanar; mode <= 34; mode++ {
		for _, log2Size := range []int{3, 4} {
			n := 1 << log2Size
			want := make([]int32, n*n)
			for i := range want {
				if (i*17+mode*11)%5 == 0 {
					want[i] = int32((i*31+mode*7)%29 + 1)
					if (i+mode)&1 != 0 {
						want[i] = -want[i]
					}
				}
			}
			want[len(want)-1] = 1

			s := sps{chromaFormatIDC: 1}
			sh := sliceHeader{sliceType: sliceI}
			b := residualBlock{log2Size: log2Size, predModeIntra: mode, intra: true}
			var bits putBits
			var w cabacWriter
			var stat [4]uint8
			w.init(&bits, 26, sh.sliceType, false)
			if err := encodeResidual(&w, &s, &pps{}, &sh, want, b, &stat); err != nil {
				t.Fatalf("mode %d size %d: encode: %v", mode, n, err)
			}
			w.encodeTerminate(1)

			var c cabac
			if err := c.init(w.bytes(), 0); err != nil {
				t.Fatal(err)
			}
			c.initContexts(26, sh.sliceType, false)
			got := make([]int32, len(want))
			if _, err := decodeResidual(&c, &s, &pps{}, &sh, got, b, &stat); err != nil {
				t.Fatalf("mode %d size %d: decode: %v", mode, n, err)
			}
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("mode %d size %d level %d: got %d, want %d", mode, n, i, got[i], want[i])
				}
			}
		}
	}
}

func TestEncodeResidualSignDataHidingRoundTrip(t *testing.T) {
	for _, b := range []residualBlock{
		{log2Size: 2},
		{log2Size: 3, cIdx: 1, predModeIntra: 22, intra: true},
		{log2Size: 4, predModeIntra: 6, intra: true},
	} {
		n := 1 << b.log2Size
		want := make([]int32, n*n)
		for i := range want {
			if i%3 == 0 {
				want[i] = int32(i%5 + 1)
				if i&1 != 0 {
					want[i] = -want[i]
				}
			}
		}
		normalizeSignDataHiding(want, n, b.predModeIntra, b.cIdx)

		s := sps{chromaFormatIDC: 1}
		p := pps{signDataHidingEnabled: true}
		sh := sliceHeader{sliceType: sliceI}
		var bits putBits
		var w cabacWriter
		var stat [4]uint8
		w.init(&bits, 26, sh.sliceType, false)
		if err := encodeResidual(&w, &s, &p, &sh, want, b, &stat); err != nil {
			t.Fatalf("size %d: encode: %v", n, err)
		}
		w.encodeTerminate(1)

		var c cabac
		if err := c.init(w.bytes(), 0); err != nil {
			t.Fatal(err)
		}
		c.initContexts(26, sh.sliceType, false)
		got := make([]int32, len(want))
		if _, err := decodeResidual(&c, &s, &p, &sh, got, b, &stat); err != nil {
			t.Fatalf("size %d: decode: %v", n, err)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("size %d level %d: got %d, want %d", n, i, got[i], want[i])
			}
		}
	}
}
