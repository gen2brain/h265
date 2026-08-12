package hevc

func (d *ctuDecoder) mvIndex(x, y int) int {
	return (y>>2)*d.mvWidth + x>>2
}

// availablePB is 6.4.2. A neighbour inside the current coding block is
// available without consulting the z-scan order of 6.4.1, apart from the one
// partition the clause excludes.
func (d *ctuDecoder) availablePB(xN, yN int) bool {
	if xN < 0 || yN < 0 ||
		xN >= int(d.s.picWidthInLumaSamples) || yN >= int(d.s.picHeightInLumaSamples) {
		return false
	}

	sameCb := xN >= d.cuX && xN < d.cuX+d.cuSize && yN >= d.cuY && yN < d.cuY+d.cuSize

	if !sameCb {
		return d.available(d.curX, d.curY, xN, yN)
	}

	return !(d.puW<<1 == d.cuSize && d.puH<<1 == d.cuSize && d.partIdx == 1 &&
		d.cuY+d.puH <= yN && d.cuX+d.puW > xN)
}

func (d *ctuDecoder) neighbourMV() neighbour {
	return func(x, y int) (*mvInfo, bool) {
		if !d.availablePB(x, y) {
			return nil, false
		}

		i := d.mvIndex(x, y)
		if i < 0 || i >= len(d.mvField) || !d.mvValid[i] {
			return nil, false
		}

		return &d.mvField[i], true
	}
}

// setMV records the motion of a prediction unit and the pictures its reference
// indices name, which 8.5.3.2.8 reads back from a later picture.
func (d *ctuDecoder) setMV(x, y, w, h int, m mvInfo) {
	var poc [2]int32

	var long [2]bool

	for l := range 2 {
		if m.pred[l] && int(m.refIdx[l]) < len(d.refPOC[l]) {
			poc[l] = d.refPOC[l][m.refIdx[l]]
			long[l] = d.refLong[l][m.refIdx[l]]
		}
	}

	for j := y; j < y+h; j += 4 {
		for i := x; i < x+w; i += 4 {
			if i >= int(d.s.picWidthInLumaSamples) || j >= int(d.s.picHeightInLumaSamples) {
				continue
			}

			k := d.mvIndex(i, j)
			d.mvField[k] = m
			d.mvPoc[k] = poc
			d.mvLong[k] = long
			d.mvValid[k] = true
		}
	}
}

// mvdCoding is 7.3.8.9.
func (d *ctuDecoder) mvdCoding() mv {
	g0x := d.c.decodeBin(ctxAbsMVDGreater0Flag) != 0
	g0y := d.c.decodeBin(ctxAbsMVDGreater0Flag) != 0

	g1x, g1y := false, false

	if g0x {
		g1x = d.c.decodeBin(ctxAbsMVDGreater1Flag+1) != 0
	}

	if g0y {
		g1y = d.c.decodeBin(ctxAbsMVDGreater1Flag+1) != 0
	}

	comp := func(g0, g1 bool) int16 {
		if !g0 {
			return 0
		}

		v := int32(1)
		if g1 {
			v = d.expGolombBypass(1) + 2
		}

		if d.c.decodeBypass() != 0 {
			v = -v
		}

		return int16(v)
	}

	return mv{comp(g0x, g1x), comp(g0y, g1y)}
}

// expGolombBypass reads a bypass-coded order-k exponential Golomb value.
func (d *ctuDecoder) expGolombBypass(k int) int32 {
	n := 0
	for d.c.decodeBypass() != 0 {
		n++

		if n > 30 {
			return 0
		}
	}

	v := int32(0)
	for i := 0; i < n+k; i++ {
		v = v<<1 | int32(d.c.decodeBypass())
	}

	if n == 0 {
		return v
	}

	return v + int32((1<<(n+k))-(1<<k))
}

func (d *ctuDecoder) refIdx(numActive int) int8 {
	if numActive <= 1 {
		return 0
	}

	v := 0

	for v < numActive-1 {
		var bit uint32

		if v < 2 {
			bit = d.c.decodeBin(ctxRefIDXL0 + v)
		} else {
			bit = d.c.decodeBypass()
		}

		if bit == 0 {
			break
		}

		v++
	}

	return int8(v)
}

// interPredIdc is the binarization of 9.3.3.9.
func (d *ctuDecoder) interPredIdc(nPbW, nPbH, depth int) int {
	if nPbW+nPbH != 12 {
		if d.c.decodeBin(ctxInterPredIDC+depth) != 0 {
			return predBI
		}
	}

	return int(d.c.decodeBin(ctxInterPredIDC + 4))
}

// predictionUnit is 7.3.8.6 followed by the motion compensation of 8.5.3.
func (d *ctuDecoder) predictionUnit(x, y, w, h, partIdx, partMode, depth, nCbS, xCb, yCb int,
	skip bool,
) error {
	d.curX, d.curY = x, y
	d.cuX, d.cuY, d.cuSize = xCb, yCb, nCbS
	d.puW, d.puH, d.partIdx = w, h, partIdx

	// 8.5.3.2.2: inside a parallel merge region a small coding block derives
	// its candidates, its neighbour availability and its temporal candidate as
	// if the whole block were one unit.
	mx, my, mw, mh, mIdx := x, y, w, h, partIdx
	if int(d.p.log2ParallelMergeLevel) > 2 && nCbS == 8 {
		mx, my, mw, mh, mIdx = xCb, yCb, nCbS, nCbS, 0
	}

	nb := d.neighbourMV()

	lists := [2][]int32{d.refPOC[0], d.refPOC[1]}

	var m mvInfo

	merge := skip
	if !skip {
		merge = d.c.decodeBin(ctxMergeFlag) != 0
	}

	if partIdx == 0 {
		d.lastMerge = merge
	}

	if merge {
		idx := 0

		if d.sh.maxNumMergeCand > 1 {
			for idx < int(d.sh.maxNumMergeCand)-1 {
				var bit uint32

				if idx == 0 {
					bit = d.c.decodeBin(ctxMergeIDX)
				} else {
					bit = d.c.decodeBypass()
				}

				if bit == 0 {
					break
				}

				idx++
			}
		}

		d.curX, d.curY = mx, my
		d.puW, d.puH, d.partIdx = mw, mh, mIdx

		cand := mergeCandidates(d.mergeCd[:], nb, mx, my, mw, mh, mIdx, partMode,
			int(d.sh.maxNumMergeCand), int(d.p.log2ParallelMergeLevel))

		d.curX, d.curY = x, y
		d.puW, d.puH, d.partIdx = w, h, partIdx

		if len(cand) < int(d.sh.maxNumMergeCand) {
			if t, ok := d.temporalMerge(mx, my, mw, mh); ok {
				cand = append(cand, t)
			}
		}

		if d.sh.sliceType == sliceB {
			cand = combineBiCandidates(cand, int(d.sh.maxNumMergeCand), lists)
		}

		numRef := int(d.sh.numRefIdxL0Active)
		if d.sh.sliceType == sliceB {
			numRef = min(numRef, int(d.sh.numRefIdxL1Active))
		}

		cand = zeroCandidates(cand, int(d.sh.maxNumMergeCand), numRef,
			d.sh.sliceType == sliceB)

		if idx >= len(cand) {
			return ErrInvalid
		}

		m = cand[idx]

		// 8.5.3.2.1: the smallest prediction units may not be bi-predicted, so
		// a merge candidate that is drops to list zero.
		if w+h == 12 && m.pred[0] && m.pred[1] {
			m.pred[1] = false
			m.mv[1] = mv{}
		}
	} else {
		idc := predL0
		if d.sh.sliceType == sliceB {
			idc = d.interPredIdc(w, h, depth)
		}

		for l := range 2 {
			if (l == 0 && idc == predL1) || (l == 1 && idc == predL0) {
				continue
			}

			active := int(d.sh.numRefIdxL0Active)
			if l == 1 {
				active = int(d.sh.numRefIdxL1Active)
			}

			ref := d.refIdx(active)

			var mvd mv

			if l == 1 && d.sh.mvdL1Zero && idc == predBI {
				mvd = mv{}
			} else {
				mvd = d.mvdCoding()
			}

			flag := d.c.decodeBin(ctxMVPLXFlag)

			if int(ref) >= len(lists[l]) {
				return ErrInvalid
			}

			ab, avail := amvpCandidates(nb, x, y, w, h, l, lists[l][ref], d.poc, lists,
				d.refLong, d.refLong[l][ref])

			var cand [2]mv

			n := 0

			if avail[0] {
				cand[n] = ab[0]
				n++
			}

			if avail[1] && !(avail[0] && ab[0] == ab[1]) {
				cand[n] = ab[1]
				n++
			}

			if n < 2 {
				if t, ok := d.temporalMV(x, y, w, h, l, lists[l][ref], d.refLong[l][ref]); ok {
					cand[n] = t
					n++
				}
			}

			p := cand[flag]

			m.mv[l] = mv{p.x + mvd.x, p.y + mvd.y}
			m.refIdx[l] = ref
			m.pred[l] = true
		}
	}

	if !m.pred[0] {
		m.refIdx[0] = -1
	}

	if !m.pred[1] {
		m.refIdx[1] = -1
	}

	d.setMV(x, y, w, h, m)
	d.markPU(x, y, w, h, false)

	return d.motionCompensate(x, y, w, h, &m)
}

func (d *ctuDecoder) motionCompensate(x, y, w, h int, m *mvInfo) error {
	var buf [2][]int16

	for l := range 2 {
		if !m.pred[l] {
			continue
		}

		if int(m.refIdx[l]) >= len(d.refPics[l]) {
			return ErrInvalid
		}

		ref := d.refPics[l][m.refIdx[l]]
		if ref == nil {
			return ErrInvalid
		}

		buf[l] = d.mcBuf[l][:w*h]

		if d.pic.BitDepth > 8 {
			src, stride := ref.plane16(0)
			mcLuma(buf[l], w, src, stride, ref.Width, ref.Height,
				x+int(m.mv[l].x>>2), y+int(m.mv[l].y>>2),
				int(m.mv[l].x&3), int(m.mv[l].y&3), w, h, d.pic.BitDepth,
				d.mcTmp[:], d.mcTmp16[:], d.mcPad16[:])
		} else {
			src, stride := ref.plane8(0)
			mcLuma(buf[l], w, src, stride, ref.Width, ref.Height,
				x+int(m.mv[l].x>>2), y+int(m.mv[l].y>>2),
				int(m.mv[l].x&3), int(m.mv[l].y&3), w, h, d.pic.BitDepth,
				d.mcTmp[:], d.mcTmp16[:], d.mcPad8[:])
		}
	}

	if err := d.combine(x, y, w, h, 0, buf, m); err != nil {
		return err
	}

	if d.s.chromaArrayType() == 0 {
		return nil
	}

	sw, sh := d.s.subWidthC, d.s.subHeightC
	cw, ch := w/sw, h/sh

	if cw == 0 || ch == 0 {
		return nil
	}

	for cIdx := 1; cIdx <= 2; cIdx++ {
		var cbuf [2][]int16

		for l := range 2 {
			if !m.pred[l] {
				continue
			}

			ref := d.refPics[l][m.refIdx[l]]
			cbuf[l] = d.mcBuf[l][:cw*ch]

			mvx := int(m.mv[l].x) * 2 / sw
			mvy := int(m.mv[l].y) * 2 / sh

			px, py := x/sw+mvx>>3, y/sh+mvy>>3
			fx, fy := mvx&7, mvy&7

			if d.pic.BitDepth > 8 {
				src, stride := ref.plane16(cIdx)
				mcChroma(cbuf[l], cw, src, stride, ref.WidthC, ref.HeightC,
					px, py, fx, fy, cw, ch, d.pic.BitDepth, d.mcTmp[:], d.mcPad16[:])
			} else {
				src, stride := ref.plane8(cIdx)
				mcChroma(cbuf[l], cw, src, stride, ref.WidthC, ref.HeightC,
					px, py, fx, fy, cw, ch, d.pic.BitDepth, d.mcTmp[:], d.mcPad8[:])
			}
		}

		if err := d.combine(x/sw, y/sh, cw, ch, cIdx, cbuf, m); err != nil {
			return err
		}
	}

	return nil
}

func (d *ctuDecoder) weighted() bool {
	return (d.p.weightedPred && d.sh.sliceType == sliceP) ||
		(d.p.weightedBipred && d.sh.sliceType == sliceB)
}

func (d *ctuDecoder) combine(x, y, w, h, cIdx int, buf [2][]int16, m *mvInfo) error {
	bd := d.pic.BitDepth

	if d.pic.BitDepth > 8 {
		plane, stride := d.pic.plane16(cIdx)

		return combineInto(d, plane, y*stride+x, stride, w, h, cIdx, bd, buf, m)
	}

	plane, stride := d.pic.plane8(cIdx)

	return combineInto(d, plane, y*stride+x, stride, w, h, cIdx, bd, buf, m)
}

// combineInto applies 8.5.3.3.4, choosing the default or the explicitly
// weighted process as the picture parameter set requires.
func combineInto[P pixel](d *ctuDecoder, dst []P, off, stride, w, h, cIdx, bd int,
	buf [2][]int16, m *mvInfo,
) error {
	if !d.weighted() {
		return combinePred(dst, off, stride, w, h, bd, buf, m)
	}

	wt := &d.sh.weights

	denom := int(wt.lumaLog2Denom)
	if cIdx > 0 {
		denom = int(wt.chromaLog2Denom)
	}

	weight := func(l int) (int, int) {
		i := int(m.refIdx[l])

		if cIdx == 0 {
			return int(wt.lumaWeight[l][i]), int(wt.lumaOffset[l][i])
		}

		return int(wt.chromaWeight[l][i][cIdx-1]), int(wt.chromaOffset[l][i][cIdx-1])
	}

	switch {
	case m.pred[0] && m.pred[1]:
		w0, o0 := weight(0)
		w1, o1 := weight(1)
		weightBi(dst, off, stride, buf[0], buf[1], w, w, h, w0, w1, o0, o1, denom, bd,
			d.s.highPrecisionOffsets)
	case m.pred[0]:
		w0, o0 := weight(0)
		weightUni(dst, off, stride, buf[0], w, w, h, w0, o0, denom, bd,
			d.s.highPrecisionOffsets)
	case m.pred[1]:
		w1, o1 := weight(1)
		weightUni(dst, off, stride, buf[1], w, w, h, w1, o1, denom, bd,
			d.s.highPrecisionOffsets)
	default:
		return ErrInvalid
	}

	return nil
}

func combinePred[P pixel](dst []P, off, stride, w, h, bd int, buf [2][]int16, m *mvInfo) error {
	switch {
	case m.pred[0] && m.pred[1]:
		predBi(dst, off, stride, buf[0], buf[1], w, w, h, bd)
	case m.pred[0]:
		predUni(dst, off, stride, buf[0], w, w, h, bd)
	case m.pred[1]:
		predUni(dst, off, stride, buf[1], w, w, h, bd)
	default:
		return ErrInvalid
	}

	return nil
}

// storeColMotion subsamples the picture's motion field to the 16x16 grid the
// temporal predictor reads, recording reference pictures by POC.
func (d *ctuDecoder) storeColMotion() {
	for y := 0; y < int(d.s.picHeightInLumaSamples); y += 16 {
		for x := 0; x < int(d.s.picWidthInLumaSamples); x += 16 {
			c := &d.pic.Col[d.pic.colIndex(x, y)]

			k := d.blkIndex(x, y)
			c.intra = d.blk[k].intra

			if c.intra || !d.mvValid[k] {
				c.intra = true

				continue
			}

			c.info = d.mvField[k]
			c.refPoc = d.mvPoc[k]
			c.refLong = d.mvLong[k]
		}
	}
}

// temporalMV is 8.5.3.2.8 and 8.5.3.2.9: the collocated block at the
// bottom-right of the prediction unit, falling back to its centre.
func (d *ctuDecoder) temporalMV(xPb, yPb, nPbW, nPbH, list int, refPoc int32, refLong bool) (mv, bool) {
	if !d.sh.temporalMvp || d.colPic == nil {
		return mv{}, false
	}

	col := d.colPic

	try := func(x, y int) (*colMotion, bool) {
		if x < 0 || y < 0 || x >= col.Width || y >= col.Height {
			return nil, false
		}

		c := &col.Col[col.colIndex(x, y)]
		if c.intra {
			return nil, false
		}

		return c, true
	}

	// 8.5.3.2.9, which the caller falls back from when it yields nothing.
	derive := func(c *colMotion) (mv, bool) {
		l := 0

		switch {
		case !c.info.pred[0]:
			l = 1
		case !c.info.pred[1]:
			l = 0
		case d.noBackwardPred:
			l = list
		case d.sh.collocatedFromL0:
			l = 1
		default:
			l = 0
		}

		if !c.info.pred[l] {
			return mv{}, false
		}

		// The collocated reference must match the target in long term status,
		// and a long term one is never scaled.
		if c.refLong[l] != refLong {
			return mv{}, false
		}

		if c.refPoc[l] == d.poc || refPoc == d.poc {
			return mv{}, false
		}

		if refLong || int32(col.POC)-c.refPoc[l] == d.poc-refPoc {
			return c.info.mv[l], true
		}

		return scaleMV(c.info.mv[l], d.poc, refPoc, int32(col.POC), c.refPoc[l]), true
	}

	// The bottom-right candidate is unavailable outside the picture or past the
	// coding tree row, and the centre takes over whenever it yields nothing.
	if c, ok := try(xPb+nPbW, yPb+nPbH); ok &&
		yPb>>d.s.ctbLog2SizeY == (yPb+nPbH)>>d.s.ctbLog2SizeY {
		if v, ok := derive(c); ok {
			return v, true
		}
	}

	c, ok := try(xPb+nPbW/2, yPb+nPbH/2)
	if !ok {
		return mv{}, false
	}

	return derive(c)
}

// temporalMerge builds the temporal merge candidate, which uses reference
// index zero in each list the slice has.
func (d *ctuDecoder) temporalMerge(x, y, w, h int) (mvInfo, bool) {
	var m mvInfo

	m.refIdx = [2]int8{-1, -1}

	for l := range 2 {
		if l == 1 && d.sh.sliceType != sliceB {
			continue
		}

		if len(d.refPOC[l]) == 0 {
			continue
		}

		v, ok := d.temporalMV(x, y, w, h, l, d.refPOC[l][0], d.refLong[l][0])
		if !ok {
			continue
		}

		m.mv[l] = v
		m.refIdx[l] = 0
		m.pred[l] = true
	}

	return m, m.pred[0] || m.pred[1]
}
