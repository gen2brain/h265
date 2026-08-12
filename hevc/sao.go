package hevc

const (
	saoOff  = 0
	saoBand = 1
	saoEdge = 2
)

type saoParams struct {
	typeIdx int
	class   int
	band    int
	offset  [4]int32
}

// Table 8-16, the two sample positions each edge class compares against.
var eoOffsets = [4][2][2]int{
	{{-1, 0}, {1, 0}},
	{{0, -1}, {0, 1}},
	{{-1, -1}, {1, 1}},
	{{1, -1}, {-1, 1}},
}

// parseSAO reads 7.3.8.3 into the per-CTB parameters 8.7.3 applies.
func (d *ctuDecoder) parseSAO(x, y int) {
	w := int(d.s.picWidthInCtbs)
	rs := (y>>d.s.ctbLog2SizeY)*w + x>>d.s.ctbLog2SizeY
	ts := int(d.rsToTs[rs])

	mergeLeft, mergeUp := false, false

	if rs%w > 0 && rs-1 >= d.sliceAddrRs && d.tileID[ts] == d.tileID[d.rsToTs[rs-1]] {
		mergeLeft = d.c.decodeBin(ctxSAOMergeFlag) != 0
	}

	if !mergeLeft && rs >= w && rs-w >= d.sliceAddrRs &&
		d.tileID[ts] == d.tileID[d.rsToTs[rs-w]] {
		mergeUp = d.c.decodeBin(ctxSAOMergeFlag) != 0
	}

	ctb := rs

	if mergeLeft {
		d.sao[ctb] = d.sao[rs-1]

		return
	}

	if mergeUp {
		d.sao[ctb] = d.sao[rs-w]

		return
	}

	nComp := 1
	if d.s.chromaArrayType() != 0 {
		nComp = 3
	}

	cMax := 1<<(min(d.pic.BitDepth, 10)-5) - 1

	for cIdx := range nComp {
		if cIdx == 0 && !d.sh.saoLuma {
			continue
		}

		if cIdx > 0 && !d.sh.saoChroma {
			continue
		}

		typeIdx := 0

		if cIdx != 2 {
			if d.c.decodeBin(ctxSAOTypeIDX) != 0 {
				typeIdx = saoBand
				if d.c.decodeBypass() != 0 {
					typeIdx = saoEdge
				}
			}

			d.saoType[cIdx] = typeIdx
		} else {
			typeIdx = d.saoType[1]
		}

		p := &d.sao[ctb][cIdx]
		p.typeIdx = typeIdx

		if typeIdx == saoOff {
			continue
		}

		var offAbs [4]int

		for i := range 4 {
			v := 0
			for v < cMax && d.c.decodeBypass() != 0 {
				v++
			}

			offAbs[i] = v
		}

		if typeIdx == saoBand {
			for i := range 4 {
				p.offset[i] = int32(offAbs[i])

				if offAbs[i] != 0 && d.c.decodeBypass() != 0 {
					p.offset[i] = -p.offset[i]
				}
			}

			p.band = int(d.c.decodeBypassBits(5))

			continue
		}

		// Edge offsets are signed by position: the first two are positive,
		// the last two negative.
		for i := range 4 {
			p.offset[i] = int32(offAbs[i])
			if i >= 2 {
				p.offset[i] = -p.offset[i]
			}
		}

		if cIdx != 2 {
			d.eoClass[cIdx] = int(d.c.decodeBypassBits(2))
		}

		p.class = d.eoClass[min(cIdx, 1)]
	}
}

// grow returns a buffer of at least n elements, reusing b when it is big
// enough. The loop filters run once per picture over the whole plane.
func grow[T any](b []T, n int) []T {
	if cap(b) < n {
		return make([]T, n)
	}

	return b[:n]
}

// applySAO is 8.7.3. It reads the picture as deblocking left it, so the source
// is copied first and every offset is taken from the unfiltered neighbourhood.
func (d *ctuDecoder) applySAO() {
	nComp := 1
	if d.s.chromaArrayType() != 0 {
		nComp = 3
	}

	for cIdx := range nComp {
		if d.pic.BitDepth > 8 {
			plane, stride := d.pic.plane16(cIdx)
			d.saoSrc16 = grow(d.saoSrc16, len(plane))
			saoPlane(d, plane, stride, cIdx, d.saoSrc16)
		} else {
			plane, stride := d.pic.plane8(cIdx)
			d.saoSrc8 = grow(d.saoSrc8, len(plane))
			saoPlane(d, plane, stride, cIdx, d.saoSrc8)
		}
	}
}

func saoPlane[P pixel](d *ctuDecoder, plane []P, stride, cIdx int, src []P) {
	sw, sh := 1, 1
	if cIdx > 0 {
		sw, sh = d.s.subWidthC, d.s.subHeightC
	}

	w, h := d.pic.Width/sw, d.pic.Height/sh
	if w == 0 || h == 0 {
		return
	}

	maxV := int32(1)<<d.pic.BitDepth - 1
	shift := d.pic.BitDepth - 5

	ctbW := int(d.s.ctbSizeY) / sw
	ctbH := int(d.s.ctbSizeY) / sh

	picW := int(d.s.picWidthInCtbs)

	// 8.7.3 reads the picture as deblocking left it, so the rows that filter
	// are copied first, with one row of margin for the classes reaching across.
	lo, hi := len(d.sao), -1

	for ctb := range d.sao {
		if !d.saoEnabled(ctb, cIdx) {
			continue
		}

		lo, hi = min(lo, ctb/picW), max(hi, ctb/picW)
	}

	if hi < 0 {
		return
	}

	from := max(lo*ctbH-1, 0) * stride
	to := min(min((hi+1)*ctbH+1, h)*stride, len(plane))

	copy(src[from:to], plane[from:to])

	// Every block reads the copy and writes only its own samples, so the
	// blocks spread over the workers once the copy is in place.
	d.overRows(len(d.sao), func(c0, c1 int) {
		for ctb := c0; ctb < c1; ctb++ {
			if !d.saoEnabled(ctb, cIdx) {
				continue
			}

			p := &d.sao[ctb][cIdx]

			x0, y0 := ctb%picW*ctbW, ctb/picW*ctbH

			a := eoOffsets[p.class][0]
			b := eoOffsets[p.class][1]

			band := p.typeIdx == saoBand

			xhi := min(x0+ctbW, w)

			for y := y0; y < min(y0+ctbH, h); y++ {
				xlo := x0

				if !band {
					if y+a[1] < 0 || y+a[1] >= h || y+b[1] < 0 || y+b[1] >= h {
						continue
					}

					xlo = max(xlo, max(-a[0], -b[0]))
					xhi = min(min(x0+ctbW, w), w-max(a[0], b[0]))
				}

				// Only the first and last row and column of a coding tree block
				// can reach across one, so the interior needs no availability test.
				ilo, ihi := xlo, xhi

				if !band {
					if a[1] != 0 && (y == y0 || y == min(y0+ctbH, h)-1) {
						ihi = ilo
					} else if a[0] != 0 {
						ilo = min(max(xlo, x0+1), xhi)
						ihi = max(min(xhi, x0+ctbW-1), ilo)
					}
				}

				for x := xlo; x < ilo; x++ {
					saoSample(d, plane, src, stride, x, y, w, h, sw, sh, p, a, b, shift, maxV)
				}

				for x := ihi; x < xhi; x++ {
					saoSample(d, plane, src, stride, x, y, w, h, sw, sh, p, a, b, shift, maxV)
				}

				// The next sample whose transform block may differ.
				next := func(x int) int {
					return min((x*sw>>d.minTbLog2+1)<<d.minTbLog2/sw, ihi)
				}

				for x := ilo; x < ihi; {
					if d.noFilter[d.tbIndex(x*sw, y*sh)] {
						x = next(x)

						continue
					}

					run := x
					for run < ihi && !d.noFilter[d.tbIndex(run*sw, y*sh)] {
						run = next(run)
					}

					o := &p.offset

					if band {
						saoBandRow(plane[y*stride+x:], src[y*stride+x:], run-x, p.band, o,
							shift, maxV)
					} else {
						base := y*stride + x
						saoEdgeRow(plane[base:], src[base:],
							src[base+a[1]*stride+a[0]:], src[base+b[1]*stride+b[0]:],
							run-x, o, maxV)
					}

					x = run
				}
			}
		}
	})
}

func (d *ctuDecoder) saoEnabled(ctb, cIdx int) bool {
	if d.sao[ctb][cIdx].typeIdx == saoOff {
		return false
	}

	sl := d.slices[d.ctbSlice[ctb]]

	if cIdx == 0 {
		return sl.saoLuma
	}

	return sl.saoChroma
}

// saoSample is 8.7.3 for one sample, for the block edges where a neighbour may
// be across a tile or slice boundary the filter may not cross.
func saoSample[P pixel](d *ctuDecoder, plane, src []P, stride, x, y, w, h, sw, sh int,
	p *saoParams, a, b [2]int, shift int, maxV int32,
) {
	if d.noFilter[d.tbIndex(x*sw, y*sh)] {
		return
	}

	v := int32(src[y*stride+x])

	if p.typeIdx == saoBand {
		if k := (int(v>>shift) - p.band) & 31; k < 4 {
			plane[y*stride+x] = P(clip3(v+p.offset[k], 0, maxV))
		}

		return
	}

	if x+a[0] < 0 || x+a[0] >= w || y+a[1] < 0 || y+a[1] >= h ||
		x+b[0] < 0 || x+b[0] >= w || y+b[1] < 0 || y+b[1] >= h {
		return
	}

	if !d.saoNeighbour(x, y, a, sw, sh) || !d.saoNeighbour(x, y, b, sw, sh) {
		return
	}

	va := int32(src[(y+a[1])*stride+x+a[0]])
	vb := int32(src[(y+b[1])*stride+x+b[0]])

	if cat := sign(v-va) + sign(v-vb); cat != 0 {
		k := cat + 2
		if cat > 0 {
			k = cat + 1
		}

		plane[y*stride+x] = P(clip3(v+p.offset[k], 0, maxV))
	}
}

// saoBandRow is the band offset of 8.7.3 over a run of samples.
func saoBandRow[P pixel](dst, src []P, n, band int, offset *[4]int32, shift int, maxV int32) {
	for i, s := range src[:n] {
		v := int32(s)

		if k := (int(v>>shift) - band) & 31; k < 4 {
			dst[i] = P(clip3(v+offset[k], 0, maxV))
		}
	}
}

// saoEdgeRow is the edge offset of 8.7.3 over a run of samples, with the two
// neighbours the class compares against passed as their own rows.
func saoEdgeRow[P pixel](dst, src, na, nb []P, n int, offset *[4]int32, maxV int32) {
	for i, s := range src[:n] {
		v := int32(s)

		cat := sign(v-int32(na[i])) + sign(v-int32(nb[i]))
		if cat == 0 {
			continue
		}

		k := cat + 2
		if cat > 0 {
			k = cat + 1
		}

		dst[i] = P(clip3(v+offset[k], 0, maxV))
	}
}

// saoNeighbour is the availability part of 8.7.3: an edge offset sample is
// left alone when the neighbour it compares against sits across a tile or
// slice boundary that filtering may not cross.
func (d *ctuDecoder) saoNeighbour(x, y int, off [2]int, sw, sh int) bool {
	return d.filterEdge(x*sw, y*sh, (x+off[0])*sw, (y+off[1])*sh)
}
