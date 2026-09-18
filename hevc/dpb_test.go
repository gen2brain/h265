package hevc

import "testing"

// A displayed reference picture remains in the DPB after its caller releases
// it. Releasing that same output twice must not recycle its prediction samples.
func TestPictureReleaseKeepsDecoderReference(t *testing.T) {
	d := &Decoder{}
	s := poolTestSPS(64)
	p := newPicture(&d.pool, s)
	p.POC = 7
	p.Y[0] = 42
	samples := &p.Y[0]
	d.dpb = []dpbPicture{{pic: p, ref: true, output: true}}

	out := d.dpbBump()
	out.Release()
	out.Release()
	if got := d.dpbFind(7); got == nil || len(got.Y) == 0 || got.Y[0] != 42 {
		t.Fatal("repeated output release destroyed a retained reference")
	}

	d.dpb[0].ref = false
	d.dpbRemoveUnused()
	if p.Y != nil {
		t.Fatal("picture still owns samples after both owners released it")
	}

	reused := newPicture(&d.pool, s)
	defer reused.release()
	if &reused.Y[0] != samples || reused.Y[0] != 0 {
		t.Fatal("released storage was not recycled and cleared")
	}
}

func TestPicturePoolBoundsParameterChanges(t *testing.T) {
	var pool picPool
	var last *uint8
	var s *sps
	for i := range 4 * picPoolDepth {
		s = poolTestSPS(uint32(64 + 16*i))
		p := newPicture(&pool, s)
		last = &p.Y[0]
		p.release()
	}

	if len(pool.free) > picPoolDepth {
		t.Fatalf("cached pictures = %d, limit %d", len(pool.free), picPoolDepth)
	}

	// Old sizes must not fill the bounded cache forever: the newest size should
	// still reuse its buffers on the next picture of that sequence.
	p := newPicture(&pool, s)
	defer p.release()
	if &p.Y[0] != last {
		t.Fatal("obsolete geometries prevented caching the current size")
	}
	for _, cached := range pool.free {
		if cached.geom == p.geom() {
			t.Fatal("consumed buffers still cached")
		}
	}
}

func TestPicturePoolResetKeepsCallerOutput(t *testing.T) {
	var pool picPool
	s := poolTestSPS(64)
	old := newPicture(&pool, s)
	old.acquire() // Transfer an output reference to the caller before reset.
	old.Y[0] = 42
	old.release() // Reset abandons the decoder's reference, not the caller's.
	pool.reset()

	fresh := newPicture(&pool, s)
	if old.Y[0] != 42 || &old.Y[0] == &fresh.Y[0] {
		t.Fatal("pool reset invalidated an outstanding output")
	}
	old.Release()
	if len(pool.free) != 0 {
		t.Fatal("old output repopulated the reset pool")
	}

	fresh.Release()
	if len(pool.free) != 1 {
		t.Fatal("new output did not recycle into the reset pool")
	}
}

func poolTestSPS(width uint32) *sps {
	return &sps{
		picWidthInLumaSamples:  width,
		picHeightInLumaSamples: 64,
		chromaFormatIDC:        1,
		subWidthC:              2,
		subHeightC:             2,
		bitDepthLuma:           8,
		bitDepthChroma:         8,
	}
}

func TestDerivePOC(t *testing.T) {
	const log2Max = 4

	maxLsb := int32(1) << log2Max

	if got := derivePOC(&pocState{}, NALIdrNLP, 0, log2Max, true); got != 0 {
		t.Fatalf("IDR POC = %d, want 0", got)
	}

	st := &pocState{prevLsb: 5, prevMsb: 0}
	if got := derivePOC(st, NALTrailR, 7, log2Max, false); got != 7 {
		t.Fatalf("forward POC = %d, want 7", got)
	}

	// Wrapping upward: the LSB went backwards by at least half the range.
	st = &pocState{prevLsb: maxLsb - 1, prevMsb: 0}
	if got := derivePOC(st, NALTrailR, 0, log2Max, false); got != maxLsb {
		t.Fatalf("wrap up POC = %d, want %d", got, maxLsb)
	}

	// Wrapping downward: the LSB jumped forward by more than half the range.
	st = &pocState{prevLsb: 0, prevMsb: maxLsb}
	if got := derivePOC(st, NALTrailR, maxLsb-1, log2Max, false); got != maxLsb-1-maxLsb+maxLsb {
		t.Fatalf("wrap down POC = %d", got)
	}

	// An IRAP that does not reset keeps following the previous state.
	st = &pocState{prevLsb: 2, prevMsb: maxLsb}
	if got := derivePOC(st, NALCra, 4, log2Max, false); got != maxLsb+4 {
		t.Fatalf("non-resetting IRAP POC = %d, want %d", got, maxLsb+4)
	}
}

// TestPOCSequenceMonotone walks a long sequence past several LSB wraps and
// requires the reconstructed count to keep step with the true one.
func TestPOCSequenceMonotone(t *testing.T) {
	const log2Max = 4

	maxLsb := int32(1) << log2Max

	st := &pocState{}

	for want := int32(0); want < 200; want++ {
		lsb := want % maxLsb

		nal := NALTrailR
		if want == 0 {
			nal = NALIdrNLP
		}

		got := derivePOC(st, nal, lsb, log2Max, want == 0)
		if got != want {
			t.Fatalf("picture %d: POC = %d", want, got)
		}

		st.update(nal, 0, got, lsb, log2Max)
	}
}

func TestPOCStateSkipsNonReference(t *testing.T) {
	st := &pocState{prevLsb: 3, prevMsb: 16}

	st.update(NALRaslN, 0, 99, 9, 4)

	if st.prevLsb != 3 || st.prevMsb != 16 {
		t.Fatal("RASL advanced the state")
	}

	st.update(NALTrailR, 1, 99, 9, 4)

	if st.prevLsb != 3 || st.prevMsb != 16 {
		t.Fatal("a higher temporal layer advanced the state")
	}

	st.update(NALTrailN, 0, 99, 9, 4)

	if st.prevLsb != 3 || st.prevMsb != 16 {
		t.Fatal("a sub-layer non-reference picture advanced the state")
	}

	st.update(NALTrailR, 0, 99, 9, 4)

	if st.prevLsb != 9 || st.prevMsb != 90 {
		t.Fatalf("reference picture left state %+v", st)
	}
}

// TestPOCWrapThreshold pins the asymmetry in 8.3.1: the upward comparison is
// >= half the range, the downward one is strictly greater.
func TestPOCWrapThreshold(t *testing.T) {
	const log2Max = 4

	maxLsb := int32(1) << log2Max
	half := maxLsb / 2

	st := &pocState{prevLsb: half, prevMsb: 0}
	if got := derivePOC(st, NALTrailR, 0, log2Max, false); got != maxLsb {
		t.Fatalf("a backward step of exactly half must wrap up: POC = %d, want %d", got, maxLsb)
	}

	st = &pocState{prevLsb: half - 1, prevMsb: 0}
	if got := derivePOC(st, NALTrailR, 0, log2Max, false); got != 0 {
		t.Fatalf("a backward step below half must not wrap: POC = %d, want 0", got)
	}

	st = &pocState{prevLsb: 0, prevMsb: maxLsb}
	if got := derivePOC(st, NALTrailR, half, log2Max, false); got != maxLsb+half {
		t.Fatalf("a forward step of exactly half must not wrap down: POC = %d, want %d",
			got, maxLsb+half)
	}

	st = &pocState{prevLsb: 0, prevMsb: maxLsb}
	if got := derivePOC(st, NALTrailR, half+1, log2Max, false); got != half+1 {
		t.Fatalf("a forward step above half must wrap down: POC = %d, want %d", got, half+1)
	}
}

func TestDeriveRefPicSet(t *testing.T) {
	st := &shortTermRPS{
		deltaPocS0: []int32{-1, -3},
		usedS0:     []bool{true, false},
		deltaPocS1: []int32{2, 5},
		usedS1:     []bool{true, true},
	}

	rps := deriveRefPicSet(10, 4, st, &longTermRPS{}, 0)

	if len(rps.stCurrBefore) != 1 || rps.stCurrBefore[0] != 9 {
		t.Fatalf("stCurrBefore = %v", rps.stCurrBefore)
	}

	if len(rps.stCurrAfter) != 2 || rps.stCurrAfter[0] != 12 || rps.stCurrAfter[1] != 15 {
		t.Fatalf("stCurrAfter = %v", rps.stCurrAfter)
	}

	if len(rps.stFoll) != 1 || rps.stFoll[0] != 7 {
		t.Fatalf("stFoll = %v", rps.stFoll)
	}

	if got := rps.numPocTotalCurr(); got != 3 {
		t.Fatalf("numPocTotalCurr = %d", got)
	}
}

func TestDeriveRefPicSetLongTerm(t *testing.T) {
	lt := &longTermRPS{
		pocLsbLt:         []uint32{3, 5},
		usedByCurrPicLt:  []bool{true, false},
		deltaPocMsbPresn: []bool{false, true},
		deltaPocMsbCycle: []uint32{0, 2},
	}

	// POC 40 with a 4-bit LSB: the MSB-present entry rebuilds
	// 40 - 2*16 - (40 & 15) + 5 = 40 - 32 - 8 + 5.
	rps := deriveRefPicSet(40, 4, &shortTermRPS{}, lt, 0)

	if len(rps.ltCurr) != 1 || rps.ltCurr[0] != 3 {
		t.Fatalf("ltCurr = %v, want [3]", rps.ltCurr)
	}

	if len(rps.ltFoll) != 1 || rps.ltFoll[0] != 5 {
		t.Fatalf("ltFoll = %v, want [5]", rps.ltFoll)
	}
}

func TestBuildRefPicList(t *testing.T) {
	rps := &refPicSet{
		stCurrBefore: []int32{9, 8},
		stCurrAfter:  []int32{12},
		ltCurr:       []int32{2},
	}

	l0 := buildRefPicList(rps, 4, false, nil, false)
	if want := []int32{9, 8, 12, 2}; !equalInt32(l0, want) {
		t.Fatalf("L0 = %v, want %v", l0, want)
	}

	l1 := buildRefPicList(rps, 4, false, nil, true)
	if want := []int32{12, 9, 8, 2}; !equalInt32(l1, want) {
		t.Fatalf("L1 = %v, want %v", l1, want)
	}

	// Fewer active entries than the set holds truncates.
	if got := buildRefPicList(rps, 2, false, nil, false); !equalInt32(got, []int32{9, 8}) {
		t.Fatalf("truncated L0 = %v", got)
	}

	// More active entries than the set holds repeats the groups.
	got := buildRefPicList(rps, 6, false, nil, false)
	if want := []int32{9, 8, 12, 2, 9, 8}; !equalInt32(got, want) {
		t.Fatalf("repeated L0 = %v, want %v", got, want)
	}

	// Modification selects from the temporary list.
	got = buildRefPicList(rps, 3, true, []uint32{3, 0, 2}, false)
	if want := []int32{2, 9, 12}; !equalInt32(got, want) {
		t.Fatalf("modified L0 = %v, want %v", got, want)
	}

	if buildRefPicList(rps, 2, true, []uint32{99, 0}, false) != nil {
		t.Fatal("an out-of-range modification index was accepted")
	}

	if buildRefPicList(&refPicSet{}, 2, false, nil, false) != nil {
		t.Fatal("an empty set produced a list")
	}
}

func equalInt32(a, b []int32) bool {
	if len(a) != len(b) {
		return false
	}

	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}

	return true
}

// TestLongTermMsbCycleAccumulates covers the running sum in 7.4.7.1, which a
// two-entry set cannot distinguish from a plain assignment.
func TestLongTermMsbCycleAccumulates(t *testing.T) {
	lt := &longTermRPS{
		pocLsbLt:         []uint32{0, 0, 0},
		usedByCurrPicLt:  []bool{true, true, true},
		deltaPocMsbPresn: []bool{true, true, true},
		deltaPocMsbCycle: []uint32{1, 2, 3},
	}

	rps := deriveRefPicSet(100, 4, &shortTermRPS{}, lt, 0)

	// Cumulative cycles are 1, 3 and 6, so 100 - 4 - cycle*16.
	want := []int32{100 - 4 - 16, 100 - 4 - 48, 100 - 4 - 96}

	if !equalInt32(rps.ltCurr, want) {
		t.Fatalf("ltCurr = %v, want %v", rps.ltCurr, want)
	}

	// The sum restarts at the boundary between the SPS-derived entries and the
	// ones coded in the slice header, so with one SPS entry it runs 1, 2, 5.
	rps = deriveRefPicSet(100, 4, &shortTermRPS{}, lt, 1)

	want = []int32{100 - 4 - 16, 100 - 4 - 32, 100 - 4 - 80}

	if !equalInt32(rps.ltCurr, want) {
		t.Fatalf("split-group ltCurr = %v, want %v", rps.ltCurr, want)
	}
}
