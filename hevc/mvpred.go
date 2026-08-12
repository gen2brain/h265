package hevc

type mv struct {
	x, y int16
}

// predFlag records which lists a prediction unit uses.
type mvInfo struct {
	mv     [2]mv
	refIdx [2]int8
	pred   [2]bool
}

func (m *mvInfo) sameMotion(o *mvInfo) bool {
	return m.pred == o.pred && m.mv == o.mv && m.refIdx == o.refIdx
}

// neighbour supplies the motion of an already-decoded block, or reports it
// unavailable. It stands in for the picture-wide motion field.
type neighbour func(x, y int) (*mvInfo, bool)

// mergeCandidates is the spatial part of 8.5.3.2.2 and 8.5.3.2.3: A1, B1, B0,
// A0 and B2 in that order, with the pruning comparisons the clause specifies
// and the partition rules that stop a 2NxN or Nx2N unit from collapsing onto
// its sibling.
func mergeCandidates(dst []mvInfo, nb neighbour, xPb, yPb, nPbW, nPbH, partIdx, partMode,
	maxCand int, parMrgLevel int,
) []mvInfo {
	inSameRegion := func(xN, yN int) bool {
		return xPb>>parMrgLevel == xN>>parMrgLevel && yPb>>parMrgLevel == yN>>parMrgLevel
	}

	get := func(xN, yN int) (*mvInfo, bool) {
		if inSameRegion(xN, yN) {
			return nil, false
		}

		return nb(xN, yN)
	}

	cand := dst[:0]

	// A partition rule that excludes A1 or B1 also removes it from the pruning
	// comparisons; being pruned as a duplicate does not.
	a1, availA1 := get(xPb-1, yPb+nPbH-1)
	if partIdx == 1 && (partMode == partModeNx2N || partMode == partModenLx2N ||
		partMode == partModenRx2N) {
		availA1 = false
	}

	if availA1 {
		cand = append(cand, *a1)
	}

	b1, availB1 := get(xPb+nPbW-1, yPb-1)
	if partIdx == 1 && (partMode == partMode2NxN || partMode == partMode2NxnU ||
		partMode == partMode2NxnD) {
		availB1 = false
	}

	if availB1 && !(availA1 && a1.sameMotion(b1)) {
		cand = append(cand, *b1)
	}

	if b0, ok := get(xPb+nPbW, yPb-1); ok && !(availB1 && b1.sameMotion(b0)) {
		cand = append(cand, *b0)
	}

	if a0, ok := get(xPb-1, yPb+nPbH); ok && !(availA1 && a1.sameMotion(a0)) {
		cand = append(cand, *a0)
	}

	if len(cand) < 4 {
		if b2, ok := get(xPb-1, yPb-1); ok &&
			!(availA1 && a1.sameMotion(b2)) && !(availB1 && b1.sameMotion(b2)) {
			cand = append(cand, *b2)
		}
	}

	if len(cand) > maxCand {
		cand = cand[:maxCand]
	}

	return cand
}

// combineBiCandidates is 8.5.3.2.4, which pairs the L0 of one candidate with
// the L1 of another until the list is full. It applies to B slices only.
func combineBiCandidates(cand []mvInfo, maxCand int, lists [2][]int32) []mvInfo {
	// Table 8-6, the order the pairs are tried in.
	l0Idx := [12]int{0, 1, 0, 2, 1, 2, 0, 3, 1, 3, 2, 3}
	l1Idx := [12]int{1, 0, 2, 0, 2, 1, 3, 0, 3, 1, 3, 2}

	orig := len(cand)
	if orig < 2 {
		return cand
	}

	for k := 0; k < orig*(orig-1) && len(cand) < maxCand; k++ {
		if k >= len(l0Idx) {
			break
		}

		a, b := &cand[l0Idx[k]], &cand[l1Idx[k]]

		if !a.pred[0] || !b.pred[1] {
			continue
		}

		if int(a.refIdx[0]) >= len(lists[0]) || int(b.refIdx[1]) >= len(lists[1]) {
			continue
		}

		if lists[0][a.refIdx[0]] == lists[1][b.refIdx[1]] && a.mv[0] == b.mv[1] {
			continue
		}

		cand = append(cand, mvInfo{
			mv:     [2]mv{a.mv[0], b.mv[1]},
			refIdx: [2]int8{a.refIdx[0], b.refIdx[1]},
			pred:   [2]bool{true, true},
		})
	}

	return cand
}

// zeroCandidates is 8.5.3.2.5, the zero-motion fill that guarantees the list
// reaches its full length.
func zeroCandidates(cand []mvInfo, maxCand, numRefIdx int, biPred bool) []mvInfo {
	for i := 0; len(cand) < maxCand; i++ {
		r := int8(0)
		if i < numRefIdx {
			r = int8(i)
		}

		c := mvInfo{refIdx: [2]int8{r, r}, pred: [2]bool{true, biPred}}
		if !biPred {
			c.refIdx[1] = -1
		}

		cand = append(cand, c)
	}

	return cand
}

// scaleMV is the temporal distance scaling of 8.5.3.2.8, shared by the
// temporal merge candidate and the spatial predictors that cross references.
func scaleMV(v mv, currPoc, refPoc, colPoc, colRefPoc int32) mv {
	td := clip3(colPoc-colRefPoc, -128, 127)
	tb := clip3(currPoc-refPoc, -128, 127)

	if td == 0 {
		return v
	}

	tx := (16384 + absI32(td)/2) / td
	scale := clip3((tb*tx+32)>>6, -4096, 4095)

	sx := scale * int32(v.x)
	sy := scale * int32(v.y)

	return mv{
		x: int16(clip3(sign(sx)*((absI32(sx)+127)>>8), -32768, 32767)),
		y: int16(clip3(sign(sy)*((absI32(sy)+127)>>8), -32768, 32767)),
	}
}

func sign(v int32) int32 {
	switch {
	case v < 0:
		return -1
	case v > 0:
		return 1
	}

	return 0
}

const (
	predL0 = iota
	predL1
	predBI
)

// refPic pairs a reference picture with the POC the lists resolved it to.
type refPic struct {
	poc int32
	pic *Picture
}

// amvpCandidates is 8.5.3.2.7. It reports the left and above predictors and
// whether each was found, leaving 8.5.3.2.6 to assemble the list.
func amvpCandidates(nb neighbour, xPb, yPb, nPbW, nPbH int, list int, refPoc int32,
	currPoc int32, lists [2][]int32, long [2][]bool, refLong bool,
) (out [2]mv, avail [2]bool) {
	direct := func(m *mvInfo) (mv, bool) {
		for k := range 2 {
			l := list ^ k

			if m.pred[l] && int(m.refIdx[l]) < len(lists[l]) &&
				lists[l][m.refIdx[l]] == refPoc {
				return m.mv[l], true
			}
		}

		return mv{}, false
	}

	// 8.5.3.2.7 takes a neighbour only when its reference matches the target in
	// long term status, and never scales a long term vector.
	scaled := func(m *mvInfo) (mv, bool) {
		for k := range 2 {
			l := list ^ k

			if !m.pred[l] || int(m.refIdx[l]) >= len(lists[l]) {
				continue
			}

			if long[l][m.refIdx[l]] != refLong {
				continue
			}

			if refLong {
				return m.mv[l], true
			}

			return scaleMV(m.mv[l], currPoc, refPoc, currPoc, lists[l][m.refIdx[l]]), true
		}

		return mv{}, false
	}

	sweep := func(pos [][2]int, pick func(*mvInfo) (mv, bool)) (mv, bool) {
		for _, p := range pos {
			m, ok := nb(p[0], p[1])
			if !ok {
				continue
			}

			if v, ok := pick(m); ok {
				return v, true
			}
		}

		return mv{}, false
	}

	left := [][2]int{{xPb - 1, yPb + nPbH}, {xPb - 1, yPb + nPbH - 1}}
	above := [][2]int{{xPb + nPbW, yPb - 1}, {xPb + nPbW - 1, yPb - 1}, {xPb - 1, yPb - 1}}

	isScaled := false

	for _, p := range left {
		if _, ok := nb(p[0], p[1]); ok {
			isScaled = true
		}
	}

	out[0], avail[0] = sweep(left, direct)
	if !avail[0] {
		out[0], avail[0] = sweep(left, scaled)
	}

	out[1], avail[1] = sweep(above, direct)

	// With no left neighbour at all the above predictor becomes the first
	// candidate and the second is derived again, this time with scaling.
	if !isScaled {
		out[0], avail[0] = out[1], avail[1]
		out[1], avail[1] = sweep(above, scaled)
	}

	return out, avail
}
