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

	copy(src, plane)

	maxV := int32(1)<<d.pic.BitDepth - 1
	shift := d.pic.BitDepth - 5

	ctbW := int(d.s.ctbSizeY) / sw
	ctbH := int(d.s.ctbSizeY) / sh

	picW := int(d.s.picWidthInCtbs)

	for ctb := range d.sao {
		p := &d.sao[ctb][cIdx]
		if p.typeIdx == saoOff {
			continue
		}

		sl := d.slices[d.ctbSlice[ctb]]

		if (cIdx == 0 && !sl.saoLuma) || (cIdx > 0 && !sl.saoChroma) {
			continue
		}

		x0, y0 := ctb%picW*ctbW, ctb/picW*ctbH

		for y := y0; y < min(y0+ctbH, h); y++ {
			for x := x0; x < min(x0+ctbW, w); x++ {
				if d.noFilter[d.tbIndex(x*sw, y*sh)] {
					continue
				}

				v := int32(src[y*stride+x])

				if p.typeIdx == saoBand {
					if k := (int(v>>shift) - p.band) & 31; k < 4 {
						plane[y*stride+x] = P(clip3(v+p.offset[k], 0, maxV))
					}

					continue
				}

				a := eoOffsets[p.class][0]
				b := eoOffsets[p.class][1]

				if x+a[0] < 0 || x+a[0] >= w || y+a[1] < 0 || y+a[1] >= h ||
					x+b[0] < 0 || x+b[0] >= w || y+b[1] < 0 || y+b[1] >= h {
					continue
				}

				if !d.saoNeighbour(x, y, a, sw, sh) || !d.saoNeighbour(x, y, b, sw, sh) {
					continue
				}

				va := int32(src[(y+a[1])*stride+x+a[0]])
				vb := int32(src[(y+b[1])*stride+x+b[0]])

				cat := sign(v-va) + sign(v-vb)

				switch {
				case cat == -2:
					plane[y*stride+x] = P(clip3(v+p.offset[0], 0, maxV))
				case cat == -1:
					plane[y*stride+x] = P(clip3(v+p.offset[1], 0, maxV))
				case cat == 1:
					plane[y*stride+x] = P(clip3(v+p.offset[2], 0, maxV))
				case cat == 2:
					plane[y*stride+x] = P(clip3(v+p.offset[3], 0, maxV))
				}
			}
		}
	}
}

// saoNeighbour is the availability part of 8.7.3: an edge offset sample is
// left alone when the neighbour it compares against sits across a tile or
// slice boundary that filtering may not cross.
func (d *ctuDecoder) saoNeighbour(x, y int, off [2]int, sw, sh int) bool {
	return d.filterEdge(x*sw, y*sh, (x+off[0])*sw, (y+off[1])*sh)
}
