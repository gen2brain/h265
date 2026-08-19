package hevc

// saoCand is one candidate: the squared error it saves against the bins it takes.
type saoCand struct {
	p    saoParams
	gain int64
	bins int64
}

// saoMaxOffset is cMax of 7.3.8.3.
func saoMaxOffset(bitDepth int) int32 {
	return int32(1)<<(min(bitDepth, 10)-5) - 1
}

// decideSAO fits every coding tree block against the picture deblocking left,
// which the second coding pass then writes in front of each of them.
func (e *intraEncoder[P]) decideSAO() {
	rows, cols := ctbCount(e.height), ctbCount(e.width)
	e.sao = regrow(e.sao, rows*cols)
	e.saoLuma, e.saoChroma = false, false

	nComp := 1
	if e.s.chromaArrayType() != 0 {
		nComp = 3
	}

	for ctb := range e.sao {
		x0, y0 := ctb%cols*64, ctb/cols*64

		for cIdx := range nComp {
			e.sao[ctb][cIdx] = saoParams{}
		}

		e.sao[ctb][0] = e.saoFor(0, x0, y0).p
		e.saoLuma = e.saoLuma || e.sao[ctb][0].typeIdx != saoOff

		if nComp == 1 {
			continue
		}

		// 7.3.8.3 gives Cb and Cr one type and one class between them.
		cb, cr := e.saoChromaPair(x0, y0)
		e.sao[ctb][1], e.sao[ctb][2] = cb, cr
		e.saoChroma = e.saoChroma || cb.typeIdx != saoOff
	}

	// A component nothing uses leaves the slice header, and its syntax with it.
	if !e.saoLuma {
		for ctb := range e.sao {
			e.sao[ctb][0] = saoParams{}
		}
	}

	if !e.saoChroma {
		for ctb := range e.sao {
			e.sao[ctb][1], e.sao[ctb][2] = saoParams{}, saoParams{}
		}
	}

	e.saoOn = e.saoLuma || e.saoChroma
}

// saoBin is one category's error, summed and counted.
type saoBin struct{ sum, cnt int64 }

// saoStats is what one walk of a coding tree block tells all thirty six
// candidates.
type saoStats struct {
	band [32]saoBin
	edge [4][4]saoBin
}

// saoFor is the best parameters for one component of one coding tree block.
func (e *intraEncoder[P]) saoFor(cIdx, x0, y0 int) saoCand {
	e.saoGather(cIdx, x0, y0)

	var best saoCand

	for band := range 32 {
		if c := e.saoBandCand(cIdx, band); e.saoBetter(c, best) {
			best = c
		}
	}

	for class := range 4 {
		if c := e.saoEdgeCand(cIdx, class); e.saoBetter(c, best) {
			best = c
		}
	}

	return best
}

// saoChromaPair picks one type and class for both chroma components on their
// combined cost, with the offsets each of them wants.
func (e *intraEncoder[P]) saoChromaPair(x0, y0 int) (saoParams, saoParams) {
	var (
		best           saoCand
		bestCb, bestCr saoParams
		cb             [32]saoCand
		cbEdge         [4]saoCand
	)

	e.saoGather(1, x0, y0)

	for band := range 32 {
		cb[band] = e.saoBandCand(1, band)
	}

	for class := range 4 {
		cbEdge[class] = e.saoEdgeCand(1, class)
	}

	e.saoGather(2, x0, y0)

	try := func(a, b saoCand) {
		joint := saoCand{p: a.p, gain: a.gain + b.gain, bins: a.bins + b.bins}

		if e.saoBetter(joint, best) {
			best, bestCb, bestCr = joint, a.p, b.p
		}
	}

	for band := range 32 {
		try(cb[band], e.saoBandCand(2, band))
	}

	for class := range 4 {
		try(cbEdge[class], e.saoEdgeCand(2, class))
	}

	return bestCb, bestCr
}

// saoBetter weighs a candidate against the one held and against doing nothing.
func (e *intraEncoder[P]) saoBetter(c, best saoCand) bool {
	if c.gain <= 0 {
		return false
	}

	return c.gain<<lambdaShift-e.lambda*c.bins >
		best.gain<<lambdaShift-e.lambda*best.bins
}

// saoBandCand fits the four offsets of one band position.
func (e *intraEncoder[P]) saoBandCand(cIdx, band int) saoCand {
	var sum, cnt [4]int64

	for k := range 4 {
		b := &e.saoStat.band[(band+k)&31]
		sum[k], cnt[k] = b.sum, b.cnt
	}

	c := saoCand{p: saoParams{typeIdx: saoBand, band: band}, bins: 5}
	if cIdx != 2 {
		c.bins += 2
	}

	e.saoFit(&c, &sum, &cnt, false)

	return c
}

// saoEdgeCand fits the four offsets of one edge class, which 8.7.3 signs by
// category so that a minimum only rises and a maximum only falls.
func (e *intraEncoder[P]) saoEdgeCand(cIdx, class int) saoCand {
	var sum, cnt [4]int64

	for k := range 4 {
		b := &e.saoStat.edge[class][k]
		sum[k], cnt[k] = b.sum, b.cnt
	}

	c := saoCand{p: saoParams{typeIdx: saoEdge, class: class}}
	if cIdx != 2 {
		c.bins = 2 + 2
	}

	e.saoFit(&c, &sum, &cnt, true)

	return c
}

// saoFit rounds each category's mean error to an offset the syntax can carry.
func (e *intraEncoder[P]) saoFit(c *saoCand, sum, cnt *[4]int64, edge bool) {
	top := int64(saoMaxOffset(e.bitDepth))

	for k := range 4 {
		if cnt[k] == 0 {
			continue
		}

		o := (sum[k]*2/cnt[k] + 1) / 2

		if edge {
			if k < 2 {
				o = max(o, 0)
			} else {
				o = min(o, 0)
			}
		}

		o = min(max(o, -top), top)

		if 2*o*sum[k]-o*o*cnt[k] <= 0 {
			o = 0
		}

		c.p.offset[k] = int32(o)
		c.gain += 2*o*sum[k] - o*o*cnt[k]

		abs := o
		if abs < 0 {
			abs = -abs
		}

		c.bins += abs + 1

		if !edge && o != 0 {
			c.bins++
		}
	}
}

// saoGather walks one coding tree block, summing its error per band and per
// category of every edge class.
func (e *intraEncoder[P]) saoGather(cIdx, x0, y0 int) {
	st := &e.saoStat
	*st = saoStats{}

	stride := e.stride(cIdx)
	w, h := e.planeSize(cIdx)
	shift := e.bitDepth - 5

	sw, sh := 0, 0
	if cIdx > 0 {
		sw, sh = e.shiftW, e.shiftH
	}

	cx0, cy0 := x0>>sw, y0>>sh
	cx1, cy1 := min(cx0+64>>sw, w), min(cy0+64>>sh, h)

	src, rec := e.src[cIdx], e.recon[cIdx]

	for y := cy0; y < cy1; y++ {
		row := y * stride

		// Only the outermost row and column reach outside the plane.
		inner := y > 0 && y < h-1

		for x := cx0; x < cx1; x++ {
			v := int32(rec[row+x])
			d := int64(src[row+x]) - int64(v)

			b := &st.band[v>>shift]
			b.sum += d
			b.cnt++

			if !inner || x == 0 || x == w-1 {
				continue
			}

			for class := range 4 {
				a, o := eoOffsets[class][0], eoOffsets[class][1]

				va := int32(rec[(y+a[1])*stride+x+a[0]])
				vb := int32(rec[(y+o[1])*stride+x+o[0]])

				cat := sign(v-va) + sign(v-vb)
				if cat == 0 {
					continue
				}

				k := cat + 2
				if cat > 0 {
					k = cat + 1
				}

				c := &st.edge[class][k]
				c.sum += d
				c.cnt++
			}
		}
	}
}

// planeSize is the sample count of a component either way.
func (e *intraEncoder[P]) planeSize(cIdx int) (int, int) {
	if cIdx == 0 {
		return e.width, e.height
	}

	return e.width >> e.shiftW, e.height >> e.shiftH
}

// writeSAO is the sao syntax of 7.3.8.3, in front of one coding tree block.
func (e *intraEncoder[P]) writeSAO(w *cabacWriter, ctb, cols int) {
	if !e.saoOn {
		return
	}

	if ctb%cols > 0 {
		w.encodeBin(ctxSAOMergeFlag, 0)
	}

	if ctb >= cols {
		w.encodeBin(ctxSAOMergeFlag, 0)
	}

	nComp := 1
	if e.s.chromaArrayType() != 0 {
		nComp = 3
	}

	top := saoMaxOffset(e.bitDepth)

	for cIdx := range nComp {
		if cIdx == 0 && !e.saoLuma {
			continue
		}

		if cIdx > 0 && !e.saoChroma {
			continue
		}

		p := &e.sao[ctb][cIdx]

		if cIdx != 2 {
			w.encodeBin(ctxSAOTypeIDX, boolToBit(p.typeIdx != saoOff))

			if p.typeIdx != saoOff {
				w.encodeBypass(boolToBit(p.typeIdx == saoEdge))
			}
		}

		if p.typeIdx == saoOff {
			continue
		}

		for k := range 4 {
			v := p.offset[k]
			if v < 0 {
				v = -v
			}

			for i := int32(0); i < v; i++ {
				w.encodeBypass(1)
			}

			if v < top {
				w.encodeBypass(0)
			}
		}

		if p.typeIdx == saoBand {
			for k := range 4 {
				if p.offset[k] != 0 {
					w.encodeBypass(boolToBit(p.offset[k] < 0))
				}
			}

			w.encodeBypassBits(uint32(p.band), 5)

			continue
		}

		if cIdx != 2 {
			w.encodeBypassBits(uint32(p.class), 2)
		}
	}
}

// applySAO is 8.7.3 over the reconstruction, so it stays what a decoder makes.
func (e *intraEncoder[P]) applySAO() {
	if !e.saoOn {
		return
	}

	cols := ctbCount(e.width)

	nComp := 1
	if e.s.chromaArrayType() != 0 {
		nComp = 3
	}

	for cIdx := range nComp {
		if cIdx == 0 && !e.saoLuma || cIdx > 0 && !e.saoChroma {
			continue
		}

		stride := e.stride(cIdx)
		w, h := e.planeSize(cIdx)

		e.saoSrc = regrow(e.saoSrc, len(e.recon[cIdx]))
		copy(e.saoSrc, e.recon[cIdx])

		maxV := int32(1)<<e.bitDepth - 1
		shift := e.bitDepth - 5

		sw, sh := 0, 0
		if cIdx > 0 {
			sw, sh = e.shiftW, e.shiftH
		}

		for ctb := range e.sao {
			p := &e.sao[ctb][cIdx]
			if p.typeIdx == saoOff {
				continue
			}

			x0, y0 := ctb%cols*64>>sw, ctb/cols*64>>sh
			a, b := eoOffsets[p.class][0], eoOffsets[p.class][1]

			for y := y0; y < min(y0+64>>sh, h); y++ {
				for x := x0; x < min(x0+64>>sw, w); x++ {
					v := int32(e.saoSrc[y*stride+x])

					if p.typeIdx == saoBand {
						if k := (int(v>>shift) - p.band) & 31; k < 4 {
							e.recon[cIdx][y*stride+x] = P(clip3(v+p.offset[k], 0, maxV))
						}

						continue
					}

					if x+a[0] < 0 || x+a[0] >= w || y+a[1] < 0 || y+a[1] >= h ||
						x+b[0] < 0 || x+b[0] >= w || y+b[1] < 0 || y+b[1] >= h {
						continue
					}

					va := int32(e.saoSrc[(y+a[1])*stride+x+a[0]])
					vb := int32(e.saoSrc[(y+b[1])*stride+x+b[0]])

					cat := sign(v-va) + sign(v-vb)
					if cat == 0 {
						continue
					}

					k := cat + 2
					if cat > 0 {
						k = cat + 1
					}

					e.recon[cIdx][y*stride+x] = P(clip3(v+p.offset[k], 0, maxV))
				}
			}
		}
	}
}
