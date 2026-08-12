package hevc

// Table 8-12.
var betaTable = [52]uint8{
	0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 20, 22, 24,
	26, 28, 30, 32, 34, 36, 38, 40, 42, 44, 46, 48, 50, 52, 54, 56,
	58, 60, 62, 64,
}

var tcTable = [54]uint8{
	0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	0, 0, 1, 1, 1, 1, 1, 1, 1, 1, 1, 2, 2, 2, 2, 3,
	3, 3, 3, 4, 4, 4, 5, 5, 6, 6, 7, 8, 9, 10, 11, 13,
	14, 16, 18, 20, 22, 24,
}

// blockInfo is what boundary strength derivation reads back after decoding.
type blockInfo struct {
	intra bool
	cbf   bool
	tuV   bool
	tuH   bool
	puV   bool
	puH   bool
}

func (d *ctuDecoder) blkIndex(x, y int) int {
	return (y>>2)*d.mvWidth + x>>2
}

func (d *ctuDecoder) markTU(x, y, w, h int, cbf bool) {
	for j := y; j < y+h; j += 4 {
		for i := x; i < x+w; i += 4 {
			if i >= int(d.s.picWidthInLumaSamples) || j >= int(d.s.picHeightInLumaSamples) {
				continue
			}

			b := &d.blk[d.blkIndex(i, j)]
			b.cbf = b.cbf || cbf
			b.tuV = b.tuV || i == x
			b.tuH = b.tuH || j == y
		}
	}
}

func (d *ctuDecoder) markPU(x, y, w, h int, intra bool) {
	for j := y; j < y+h; j += 4 {
		for i := x; i < x+w; i += 4 {
			if i >= int(d.s.picWidthInLumaSamples) || j >= int(d.s.picHeightInLumaSamples) {
				continue
			}

			b := &d.blk[d.blkIndex(i, j)]
			b.intra = intra
			b.puV = b.puV || i == x
			b.puH = b.puH || j == y
		}
	}
}

// boundaryStrength is 8.7.2.4.
func (d *ctuDecoder) boundaryStrength(xP, yP, xQ, yQ int, vertical bool) int {
	pi, qi := d.blkIndex(xP, yP), d.blkIndex(xQ, yQ)

	p := &d.blk[pi]
	q := &d.blk[qi]

	tu := q.tuV
	pu := q.puV

	if !vertical {
		tu, pu = q.tuH, q.puH
	}

	// 8.7.2 filters transform and prediction block edges only, not every
	// edge on the eight-sample grid.
	if !tu && !pu {
		return 0
	}

	if p.intra || q.intra {
		return 2
	}

	if tu && (p.cbf || q.cbf) {
		return 1
	}

	if !d.mvValid[pi] || !d.mvValid[qi] {
		return 1
	}

	if differentMotion(&d.mvField[pi], &d.mvField[qi], d.mvPoc[pi], d.mvPoc[qi]) {
		return 1
	}

	return 0
}

func mvFar(a, b mv) bool {
	return absI32(int32(a.x)-int32(b.x)) >= 4 || absI32(int32(a.y)-int32(b.y)) >= 4
}

// differentMotion is the motion part of 8.7.2.4, comparing the pictures each
// block recorded when it was decoded.
func differentMotion(p, q *mvInfo, pPoc, qPoc [2]int32) bool {
	var (
		pp, qp [2]int32
		pv, qv [2]mv
		np, nq int
	)

	for l := range 2 {
		if p.pred[l] {
			pp[np], pv[np] = pPoc[l], p.mv[l]
			np++
		}

		if q.pred[l] {
			qp[nq], qv[nq] = qPoc[l], q.mv[l]
			nq++
		}
	}

	if np != nq {
		return true
	}

	if np == 1 {
		return pp[0] != qp[0] || mvFar(pv[0], qv[0])
	}

	if np == 0 {
		return false
	}

	if pp[0] == qp[0] && pp[1] == qp[1] {
		if pp[0] == pp[1] {
			return (mvFar(pv[0], qv[0]) || mvFar(pv[1], qv[1])) &&
				(mvFar(pv[0], qv[1]) || mvFar(pv[1], qv[0]))
		}

		return mvFar(pv[0], qv[0]) || mvFar(pv[1], qv[1])
	}

	if pp[0] == qp[1] && pp[1] == qp[0] {
		return mvFar(pv[0], qv[1]) || mvFar(pv[1], qv[0])
	}

	return true
}

// sliceAt is the slice header governing a coding tree block. The filtering
// parameters of 7.4.7.1 are per slice.
func (d *ctuDecoder) sliceAt(x, y int) *sliceHeader {
	rs := (y>>d.s.ctbLog2SizeY)*int(d.s.picWidthInCtbs) + x>>d.s.ctbLog2SizeY
	if rs < 0 || rs >= len(d.ctbSlice) || int(d.ctbSlice[rs]) >= len(d.slices) {
		return d.sh
	}

	return d.slices[d.ctbSlice[rs]]
}

// deblock is 8.7.2: every vertical edge in the picture, then every horizontal
// one, both on the eight-sample grid. Within a pass each edge writes its own
// band of eight lines, so the bands split across workers.
func (d *ctuDecoder) deblock() {
	w, h := int(d.s.picWidthInLumaSamples), int(d.s.picHeightInLumaSamples)

	for _, vertical := range []bool{true, false} {
		d.overRows((h+7)/8, func(r0, r1 int) {
			for r := r0; r < r1; r++ {
				y := r * 8

				if !vertical && y == 0 {
					continue
				}

				for x := 0; x < w; x += 8 {
					if vertical && x == 0 {
						continue
					}

					d.deblockEdge(x, y, vertical)
				}
			}
		})
	}
}

func (d *ctuDecoder) deblockEdge(x, y int, vertical bool) {
	// The two sides share a coding tree block unless the edge sits on one of
	// its boundaries, and 8.7.2 only asks about tiles and slices when they do.
	ctb := 1<<d.s.ctbLog2SizeY - 1

	across := x&ctb == 0
	if !vertical {
		across = y&ctb == 0
	}

	for k := 0; k < 8; k += 4 {
		var px, py, qx, qy int

		if vertical {
			px, py = x-1, y+k
			qx, qy = x, y+k
		} else {
			px, py = x+k, y-1
			qx, qy = x+k, y
		}

		if qy >= int(d.s.picHeightInLumaSamples) || qx >= int(d.s.picWidthInLumaSamples) {
			continue
		}

		if across && !d.filterEdge(px, py, qx, qy) {
			continue
		}

		bs := d.boundaryStrength(px, py, qx, qy, vertical)
		if bs == 0 {
			continue
		}

		sl := d.sliceAt(qx, qy)
		if sl.deblockingDisabled {
			continue
		}

		pt, qt := d.tbIndex(px, py), d.tbIndex(qx, qy)

		qp := (int32(d.qpY[pt]) + int32(d.qpY[qt]) + 1) >> 1

		// 8.7.2.5.3 leaves a side untouched when its coding unit bypassed the
		// transform, or is pulse code modulated with filtering turned off.
		noP, noQ := d.noFilter[pt], d.noFilter[qt]

		if d.pic.BitDepth > 8 {
			plane, stride := d.pic.plane16(0)
			deblockLuma(plane, stride, qx, qy, vertical, bs, qp, sl, d.pic.BitDepth, noP, noQ)
		} else {
			plane, stride := d.pic.plane8(0)
			deblockLuma(plane, stride, qx, qy, vertical, bs, qp, sl, d.pic.BitDepth, noP, noQ)
		}

		if bs != 2 || d.s.chromaArrayType() == 0 {
			continue
		}

		sw, sh := d.s.subWidthC, d.s.subHeightC

		if (vertical && x%(8*sw) != 0) || (!vertical && y%(8*sh) != 0) {
			continue
		}

		// The four-sample luma segment spans 4/subHeightC chroma rows on a
		// vertical edge, and 4/subWidthC columns on a horizontal one.
		n := 4 / sh
		if !vertical {
			n = 4 / sw
		}

		for cIdx := 1; cIdx <= 2; cIdx++ {
			off := d.p.cbQPOffset
			if cIdx == 2 {
				off = d.p.crQPOffset
			}

			cqp := chromaQP(clip3(qp+off, 0, 57), d.s.chromaArrayType())

			if d.pic.BitDepth > 8 {
				plane, stride := d.pic.plane16(cIdx)
				deblockChroma(plane, stride, qx/sw, qy/sh, n, vertical, cqp, sl, d.pic.BitDepth,
					noP, noQ)
			} else {
				plane, stride := d.pic.plane8(cIdx)
				deblockChroma(plane, stride, qx/sw, qy/sh, n, vertical, cqp, sl, d.pic.BitDepth,
					noP, noQ)
			}
		}
	}
}

func betaTc(qp int32, bs int, sh *sliceHeader, bitDepth int) (int32, int32) {
	qb := clip3(qp+int32(sh.betaOffsetDiv2)*2, 0, 51)
	beta := int32(betaTable[qb]) * (1 << (bitDepth - 8))

	qt := clip3(qp+2*int32(bs-1)+int32(sh.tcOffsetDiv2)*2, 0, 53)
	tc := int32(tcTable[qt]) * (1 << (bitDepth - 8))

	return beta, tc
}

// deblockLuma is 8.7.2.5.3, filtering the four lines that share one decision.
func deblockLuma[P pixel](plane []P, stride, x, y int, vertical bool, bs int, qp int32,
	sh *sliceHeader, bitDepth int, noP, noQ bool,
) {
	beta, tc := betaTc(qp, bs, sh, bitDepth)
	if beta == 0 || tc == 0 {
		return
	}

	step, line := 1, stride
	if !vertical {
		step, line = stride, 1
	}

	base := y*stride + x

	at := func(l, i int) int32 {
		return int32(plane[base+l*line+i*step])
	}

	set := func(l, i int, v int32) {
		if (i < 0 && noP) || (i >= 0 && noQ) {
			return
		}

		plane[base+l*line+i*step] = P(clip3(v, 0, 1<<bitDepth-1))
	}

	dpq := func(l int) (int32, int32) {
		dp := absI32(at(l, -3) - 2*at(l, -2) + at(l, -1))
		dq := absI32(at(l, 2) - 2*at(l, 1) + at(l, 0))

		return dp, dq
	}

	dp0, dq0 := dpq(0)
	dp3, dq3 := dpq(3)

	dp, dq := dp0+dp3, dq0+dq3
	if dp+dq >= beta {
		return
	}

	strong := func(l int, d2 int32) bool {
		return 2*d2 < beta>>2 &&
			absI32(at(l, -4)-at(l, -1))+absI32(at(l, 0)-at(l, 3)) < beta>>3 &&
			absI32(at(l, -1)-at(l, 0)) < (5*tc+1)>>1
	}

	if strong(0, dp0+dq0) && strong(3, dp3+dq3) {
		for l := range 4 {
			p0, p1, p2, p3 := at(l, -1), at(l, -2), at(l, -3), at(l, -4)
			q0, q1, q2, q3 := at(l, 0), at(l, 1), at(l, 2), at(l, 3)

			c := 2 * tc

			set(l, -1, clip3((p2+2*p1+2*p0+2*q0+q1+4)>>3, p0-c, p0+c))
			set(l, -2, clip3((p2+p1+p0+q0+2)>>2, p1-c, p1+c))
			set(l, -3, clip3((2*p3+3*p2+p1+p0+q0+4)>>3, p2-c, p2+c))
			set(l, 0, clip3((p1+2*p0+2*q0+2*q1+q2+4)>>3, q0-c, q0+c))
			set(l, 1, clip3((p0+q0+q1+q2+2)>>2, q1-c, q1+c))
			set(l, 2, clip3((p0+q0+q1+3*q2+2*q3+4)>>3, q2-c, q2+c))
		}

		return
	}

	nDp, nDq := 0, 0

	if dp < (beta+beta>>1)>>3 {
		nDp = 1
	}

	if dq < (beta+beta>>1)>>3 {
		nDq = 1
	}

	for l := range 4 {
		p0, p1, p2 := at(l, -1), at(l, -2), at(l, -3)
		q0, q1, q2 := at(l, 0), at(l, 1), at(l, 2)

		delta := (9*(q0-p0) - 3*(q1-p1) + 8) >> 4
		if absI32(delta) >= tc*10 {
			continue
		}

		delta = clip3(delta, -tc, tc)

		set(l, -1, p0+delta)
		set(l, 0, q0-delta)

		if nDp != 0 {
			v := clip3(((p2+p0+1)>>1-p1+delta)>>1, -(tc >> 1), tc>>1)
			set(l, -2, p1+v)
		}

		if nDq != 0 {
			v := clip3(((q2+q0+1)>>1-q1-delta)>>1, -(tc >> 1), tc>>1)
			set(l, 1, q1+v)
		}
	}
}

// deblockChroma is 8.7.2.5.5, which only runs where the strength is two.
func deblockChroma[P pixel](plane []P, stride, x, y, n int, vertical bool, qp int32,
	sh *sliceHeader, bitDepth int, noP, noQ bool,
) {
	_, tc := betaTc(qp, 2, sh, bitDepth)
	if tc == 0 {
		return
	}

	step, line := 1, stride
	if !vertical {
		step, line = stride, 1
	}

	base := y*stride + x

	for l := range n {
		off := base + l*line

		p0, p1 := int32(plane[off-step]), int32(plane[off-2*step])
		q0, q1 := int32(plane[off]), int32(plane[off+step])

		delta := clip3((((q0-p0)<<2)+p1-q1+4)>>3, -tc, tc)

		if !noP {
			plane[off-step] = P(clip3(p0+delta, 0, 1<<bitDepth-1))
		}

		if !noQ {
			plane[off] = P(clip3(q0-delta, 0, 1<<bitDepth-1))
		}
	}
}

// filterEdge is the filterEdgeFlag derivation of 8.7.2, which suppresses
// filtering across a tile or slice boundary the parameter sets close off.
func (d *ctuDecoder) filterEdge(xP, yP, xQ, yQ int) bool {
	w := int(d.s.picWidthInCtbs)

	pRs := (yP>>d.s.ctbLog2SizeY)*w + xP>>d.s.ctbLog2SizeY
	qRs := (yQ>>d.s.ctbLog2SizeY)*w + xQ>>d.s.ctbLog2SizeY

	if pRs == qRs {
		return true
	}

	if !d.p.loopFilterAcrossTiles && d.tileID[d.rsToTs[pRs]] != d.tileID[d.rsToTs[qRs]] {
		return false
	}

	if d.ctbSliceAddr[pRs] != d.ctbSliceAddr[qRs] {
		if !d.sliceLF[d.ctbSliceAddr[qRs]] {
			return false
		}
	}

	return true
}
