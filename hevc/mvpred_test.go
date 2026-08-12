package hevc

import "testing"

func mi(x, y int16, ref int8) mvInfo {
	return mvInfo{mv: [2]mv{{x, y}}, refIdx: [2]int8{ref, -1}, pred: [2]bool{true, false}}
}

// field builds a neighbour lookup from explicit positions.
func field(m map[[2]int]mvInfo) neighbour {
	return func(x, y int) (*mvInfo, bool) {
		v, ok := m[[2]int{x, y}]
		if !ok {
			return nil, false
		}

		return &v, true
	}
}

func TestMergeCandidateOrder(t *testing.T) {
	a1, b1, b0, a0, b2 := mi(1, 0, 0), mi(2, 0, 0), mi(3, 0, 0), mi(4, 0, 0), mi(5, 0, 0)

	nb := field(map[[2]int]mvInfo{
		{7, 15}: a1,
		{15, 7}: b1,
		{16, 7}: b0,
		{7, 16}: a0,
		{7, 7}:  b2,
	})

	got := mergeCandidates(make([]mvInfo, 8), nb, 8, 8, 8, 8, 0, partMode2Nx2N, 5, 2)

	want := []int16{1, 2, 3, 4}
	if len(got) != len(want) {
		t.Fatalf("got %d candidates, want %d", len(got), len(want))
	}

	for i, w := range want {
		if got[i].mv[0].x != w {
			t.Fatalf("candidate %d is %d, want %d", i, got[i].mv[0].x, w)
		}
	}
}

// TestMergePruning covers the comparisons in 8.5.3.2.3: B1 against A1, B0
// against B1, A0 against A1, and B2 against both.
func TestMergePruning(t *testing.T) {
	same := mi(1, 0, 0)

	nb := field(map[[2]int]mvInfo{
		{7, 15}: same,
		{15, 7}: same,
		{16, 7}: same,
		{7, 16}: same,
		{7, 7}:  same,
	})

	if got := mergeCandidates(make([]mvInfo, 8), nb, 8, 8, 8, 8, 0, partMode2Nx2N, 5, 2); len(got) != 1 {
		t.Fatalf("identical neighbours gave %d candidates, want 1", len(got))
	}

	// B2 is only considered while fewer than four candidates are present.
	nb4 := field(map[[2]int]mvInfo{
		{7, 15}: mi(1, 0, 0),
		{15, 7}: mi(2, 0, 0),
		{16, 7}: mi(3, 0, 0),
		{7, 16}: mi(4, 0, 0),
		{7, 7}:  mi(5, 0, 0),
	})

	if got := mergeCandidates(make([]mvInfo, 8), nb4, 8, 8, 8, 8, 0, partMode2Nx2N, 5, 2); len(got) != 4 {
		t.Fatalf("B2 was added past four candidates: %d", len(got))
	}
}

// TestMergePartitionRules checks that the second partition of a two-way split
// cannot merge with its sibling.
func TestMergePartitionRules(t *testing.T) {
	nb := field(map[[2]int]mvInfo{
		{7, 15}: mi(1, 0, 0),
		{15, 7}: mi(2, 0, 0),
	})

	got := mergeCandidates(make([]mvInfo, 8), nb, 8, 8, 8, 8, 1, partModeNx2N, 5, 2)
	for _, c := range got {
		if c.mv[0].x == 1 {
			t.Fatal("A1 was used by the second partition of an Nx2N split")
		}
	}

	got = mergeCandidates(make([]mvInfo, 8), nb, 8, 8, 8, 8, 1, partMode2NxN, 5, 2)
	for _, c := range got {
		if c.mv[0].x == 2 {
			t.Fatal("B1 was used by the second partition of a 2NxN split")
		}
	}
}

func TestZeroCandidates(t *testing.T) {
	got := zeroCandidates(nil, 3, 2, false)
	if len(got) != 3 {
		t.Fatalf("got %d candidates, want 3", len(got))
	}

	for i, c := range got {
		want := int8(i)
		if i >= 2 {
			want = 0
		}

		if c.refIdx[0] != want || c.pred[1] {
			t.Fatalf("candidate %d = %+v", i, c)
		}
	}
}

func TestCombineBiCandidates(t *testing.T) {
	a := mvInfo{mv: [2]mv{{1, 1}, {2, 2}}, refIdx: [2]int8{0, 0}, pred: [2]bool{true, true}}
	b := mvInfo{mv: [2]mv{{3, 3}, {4, 4}}, refIdx: [2]int8{1, 1}, pred: [2]bool{true, true}}

	lists := [2][]int32{{4, 8}, {4, 8}}

	got := combineBiCandidates([]mvInfo{a, b}, 4, lists)
	if len(got) != 4 {
		t.Fatalf("got %d candidates, want 4", len(got))
	}

	// The first combination takes L0 from candidate 0 and L1 from candidate 1.
	if got[2].mv[0] != a.mv[0] || got[2].mv[1] != b.mv[1] {
		t.Fatalf("first combination = %+v", got[2])
	}

	if got[3].mv[0] != b.mv[0] || got[3].mv[1] != a.mv[1] {
		t.Fatalf("second combination = %+v", got[3])
	}

	// A pair that would duplicate an existing candidate is skipped.
	same := mvInfo{mv: [2]mv{{1, 1}, {1, 1}}, refIdx: [2]int8{0, 0}, pred: [2]bool{true, true}}
	if got := combineBiCandidates([]mvInfo{same, same}, 4, lists); len(got) != 2 {
		t.Fatalf("identical candidates combined into %d", len(got))
	}
}

func TestScaleMV(t *testing.T) {
	// Equal distances leave the vector alone.
	if got := scaleMV(mv{8, -8}, 10, 8, 20, 18); got != (mv{8, -8}) {
		t.Fatalf("equal distance scaling gave %+v", got)
	}

	// Halving the distance halves the vector.
	if got := scaleMV(mv{8, -8}, 10, 9, 20, 18); got != (mv{4, -4}) {
		t.Fatalf("half distance scaling gave %+v", got)
	}

	// Doubling it doubles the vector.
	if got := scaleMV(mv{8, -8}, 12, 8, 20, 18); got != (mv{16, -16}) {
		t.Fatalf("double distance scaling gave %+v", got)
	}

	// A zero reference distance cannot be scaled and passes through.
	if got := scaleMV(mv{5, 5}, 10, 8, 20, 20); got != (mv{5, 5}) {
		t.Fatalf("zero distance scaling gave %+v", got)
	}
}

// naiveScaleMV transcribes 8.5.3.2.8 term by term.
func naiveScaleMV(v mv, currPoc, refPoc, colPoc, colRefPoc int32) mv {
	td := clip3(colPoc-colRefPoc, -128, 127)
	tb := clip3(currPoc-refPoc, -128, 127)

	if td == 0 {
		return v
	}

	tx := (16384 + absI32(td)/2) / td
	distScale := clip3((tb*tx+32)>>6, -4096, 4095)

	scale := func(c int16) int16 {
		p := distScale * int32(c)

		s := int32(1)
		if p < 0 {
			s = -1
		}

		return int16(clip3(s*((absI32(p)+127)>>8), -32768, 32767))
	}

	return mv{scale(v.x), scale(v.y)}
}

// TestScaleMVAgainstSpec sweeps distances that are not powers of two, where the
// two rounding terms in 8.5.3.2.8 actually change the result.
func TestScaleMVAgainstSpec(t *testing.T) {
	for td := int32(-40); td <= 40; td++ {
		for tb := int32(-70); tb <= 70; tb += 7 {
			for _, c := range []int16{1, 3, 8, 20, 64, 100, -37, -255, 1000, -4096} {
				v := mv{c, -c}

				got := scaleMV(v, tb, 0, td, 0)
				want := naiveScaleMV(v, tb, 0, td, 0)

				if got != want {
					t.Fatalf("td=%d tb=%d v=%+v: got %+v, want %+v", td, tb, v, got, want)
				}
			}
		}
	}
}
