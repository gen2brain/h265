package hevc

// Decoder decodes a HEVC bitstream one NAL unit at a time.
type Decoder struct {
	vps map[uint8]*vps
	sps map[uint32]*sps
	pps map[uint32]*pps

	cur      *Picture
	curOut   bool
	ctu      *ctuDecoder
	prevSlic *sliceHeader

	dpb    []dpbPicture
	poc    pocState
	curRPS refPicSet

	// 8.1.3: leading pictures associated with an intra random access point
	// that starts decoding reference pictures that were never decoded, and
	// are discarded rather than decoded.
	seenPicture bool
	skipRASL    bool

	maxReorder   int
	maxLatency   int
	maxDecPicBuf int
}

// DecodeNAL consumes one NAL unit and returns whatever pictures that completes,
// in output order. Reordering means a picture may surface several NAL units
// after the one that finished it.
func (d *Decoder) DecodeNAL(nal NALUnit) ([]*Picture, error) {
	if d.sps == nil {
		d.vps = make(map[uint8]*vps)
		d.sps = make(map[uint32]*sps)
		d.pps = make(map[uint32]*pps)
	}

	switch nal.Type {
	case NALVPS:
		v, err := parseVPS(nal.RBSP)
		if err != nil {
			return nil, err
		}

		d.vps[v.id] = v

		return nil, nil

	case NALSPS:
		s, err := parseSPS(nal.RBSP)
		if err != nil {
			return nil, err
		}

		d.sps[s.id] = s

		return nil, nil

	case NALPPS:
		p, err := parsePPS(nal.RBSP)
		if err != nil {
			return nil, err
		}

		d.pps[p.id] = p

		return nil, nil
	}

	if nal.Type == NALEOS || nal.Type == NALEOB {
		d.seenPicture = false

		return nil, nil
	}

	if !nal.Type.IsVCL() {
		return nil, nil
	}

	if nal.Type.IsIRAP() {
		d.skipRASL = !d.seenPicture || nal.Type.IsIDR() ||
			(nal.Type >= NALBlaWLP && nal.Type <= NALBlaNLP)
		d.seenPicture = true
	}

	if d.skipRASL && (nal.Type == NALRaslN || nal.Type == NALRaslR) {
		return nil, nil
	}

	d.seenPicture = true

	return d.decodeSlice(nal)
}

// Flush ends the sequence and returns every picture still held back for
// reordering, in output order.
func (d *Decoder) Flush() []*Picture {
	out := d.finishPicture()

	for {
		p := d.dpbBump()
		if p == nil {
			break
		}

		out = append(out, p)
	}

	d.cur, d.ctu, d.prevSlic, d.dpb = nil, nil, nil, nil

	return out
}

// finishPicture runs the loop filters over the picture the decoder has been
// filling, files it in the buffer and applies the additional bumping of
// C.5.2.3.
func (d *Decoder) finishPicture() []*Picture {
	if d.cur == nil {
		return nil
	}

	if d.ctu != nil {
		d.ctu.storeColMotion()
		d.ctu.deblock()
		d.ctu.applySAO()
	}

	d.dpbStore(d.cur, d.curOut)

	d.cur, d.ctu = nil, nil

	return d.dpbDrain(false)
}

func (d *Decoder) decodeSlice(nal NALUnit) ([]*Picture, error) {
	p, err := d.ppsForSlice(nal)
	if err != nil {
		return nil, err
	}

	s, ok := d.sps[p.spsID]
	if !ok {
		return nil, ErrInvalid
	}

	sh, err := parseSliceHeader(nal.RBSP, nal.Type, s, p)
	if err != nil {
		return nil, err
	}

	if sh.dependentSliceSegment {
		if d.prevSlic == nil {
			return nil, ErrInvalid
		}

		sh.inherit(d.prevSlic)
	} else {
		indep := *sh
		d.prevSlic = &indep
	}

	if err := p.resolveTileGeometry(s); err != nil {
		return nil, err
	}

	var done []*Picture

	if sh.firstSliceSegmentInPic {
		prior := d.cur != nil

		done = append(done, d.finishPicture()...)

		d.maxReorder = int(s.maxNumReorderPics)
		d.maxLatency = int(s.maxLatencyIncrease)
		d.maxDecPicBuf = int(s.maxDecPicBuffering) + 1

		// 8.1.3 and 8.3.1: only a random access point that starts decoding
		// restarts the count; one in mid-stream keeps it.
		noRaslOutput := nal.Type.IsIRAP() && d.skipRASL

		if noRaslOutput {
			d.poc = pocState{}
		}

		poc := derivePOC(&d.poc, nal.Type, int32(sh.picOrderCntLsb),
			s.log2MaxPocLsb, noRaslOutput)
		d.poc.update(nal.Type, nal.TemporalID, poc, int32(sh.picOrderCntLsb), s.log2MaxPocLsb)

		rps := deriveRefPicSet(poc, s.log2MaxPocLsb, &sh.stRPS, &sh.ltRPS, sh.ltRPS.numSps)
		d.resolveLongTerm(&rps, s.log2MaxPocLsb)

		// C.5.2.2: a random access point that starts decoding either discards
		// the buffer or releases all of it, and a clean one always discards.
		// Otherwise the reference picture set decides what stays.
		switch {
		case noRaslOutput && prior:
			if !sh.noOutputOfPriorPics && nal.Type != NALCra {
				for {
					q := d.dpbBump()
					if q == nil {
						break
					}

					done = append(done, q)
				}
			}

			d.dpb = nil
		default:
			d.dpbMark(&rps)
			d.dpbRemoveUnused()
			done = append(done, d.dpbDrain(true)...)
		}

		if sh.sliceType != sliceI {
			d.generateUnavailable(&rps, s)
		}

		d.curRPS = rps

		d.cur = newPicture(s)
		d.cur.POC = int(poc)
		d.curOut = sh.picOutputFlag
		d.ctu = newCTUDecoder(d.ctu, s, p, sh, d.cur)
		d.ctu.poc = poc
	}

	if d.cur == nil || d.ctu == nil {
		return nil, ErrInvalid
	}

	d.ctu.sh = sh

	// 7.4.7.1 signals the active reference counts and the list modification in
	// every slice header, so the lists belong to the slice, not the picture.
	d.buildRefLists(sh)

	if err := d.ctu.decodeSliceData(nal, sh); err != nil {
		return nil, err
	}

	return done, nil
}

func (d *Decoder) ppsForSlice(nal NALUnit) (*pps, error) {
	var c getBits
	c.init(nal.RBSP)

	c.bit()

	if nal.Type >= NALBlaWLP && nal.Type <= 23 {
		c.bit()
	}

	id := c.ue()
	if c.err {
		return nil, ErrInvalid
	}

	p, ok := d.pps[id]
	if !ok {
		return nil, ErrInvalid
	}

	return p, nil
}

// decodeSliceData is 7.3.8.1. Tiles and wavefronts split the slice segment into
// substreams that each restart the arithmetic decoder at an entry point.
func (d *ctuDecoder) decodeSliceData(nal NALUnit, sh *sliceHeader) error {
	w := int(d.s.picWidthInCtbs)
	total := w * int(d.s.picHeightInCtbs)

	starts := d.substreamStarts(nal, sh)

	d.sliceAddrRs = int(sh.sliceSegmentAddress)
	if sh.dependentSliceSegment {
		d.sliceAddrRs = d.depSliceAddrRs
	} else {
		d.depSliceAddrRs = int(sh.sliceSegmentAddress)
	}

	d.simpleAvail = d.sliceAddrRs == 0 && !d.p.tilesEnabled

	sub := 0

	start := int(d.rsToTs[sh.sliceSegmentAddress])
	tileStart := start == 0 || d.tileID[start] != d.tileID[start-1]

	if err := d.startSubstream(nal, sh, starts, sub, tileStart); err != nil {
		return err
	}

	wpp := d.p.entropyCodingSync

	d.sliceLF[int32(d.sliceAddrRs)] = sh.loopFilterAcross

	d.slices = append(d.slices, sh)
	cur := int32(len(d.slices) - 1)

	for ts := start; ts < total; ts++ {
		rs := int(d.tsToRs[ts])

		d.ctbSliceAddr[rs] = int32(d.sliceAddrRs)
		d.ctbSlice[rs] = cur
		x := rs % w << d.s.ctbLog2SizeY
		y := rs / w << d.s.ctbLog2SizeY

		if wpp && rs%w == 0 {
			// 9.3.1 syncs from the block above-right, but only when 6.4.1
			// makes it available; a new slice starting on a row boundary has
			// to initialise instead.
			top := rs - w + 1
			availT := rs >= w && top >= d.sliceAddrRs &&
				d.tileID[d.rsToTs[rs]] == d.tileID[d.rsToTs[top]]

			switch {
			case availT && d.hasSaved:
				d.c.state = d.saved
			case ts > start:
				d.c.initContexts(sh.qpY, sh.sliceType, sh.cabacInit)
			}

			d.qpYPrev = sh.qpY
		}

		if err := d.codingTreeUnit(x, y); err != nil {
			return err
		}

		if wpp && rs%w == 1 {
			d.saved = d.c.state
			d.hasSaved = true
		}

		if d.c.decodeTerminate() != 0 {
			if d.p.dependentSliceSegmentsEnabled {
				d.depSaved = d.c.state
				d.hasDepSaved = true
			}

			return nil
		}

		endOfRow := wpp && rs%w == w-1
		endOfTile := ts+1 < total && d.tileID[ts+1] != d.tileID[ts]

		if endOfRow || endOfTile {
			if d.c.decodeTerminate() == 0 {
				return ErrInvalid
			}

			sub++

			if err := d.startSubstream(nal, sh, starts, sub, false); err != nil {
				return err
			}

			if endOfTile {
				d.c.initContexts(sh.qpY, sh.sliceType, sh.cabacInit)
				d.hasSaved = false
			}

			d.qpYPrev = sh.qpY
		}
	}

	return nil
}

// substreamStarts converts the entry point offsets, which count bytes of the
// NAL payload, into indices into the RBSP.
func (d *ctuDecoder) substreamStarts(nal NALUnit, sh *sliceHeader) []int {
	starts := make([]int, 0, len(sh.entryPointOffsets)+1)
	starts = append(starts, sh.dataOffset)

	base := nal.NALOffset(sh.dataOffset)

	for _, off := range sh.entryPointOffsets {
		starts = append(starts, nal.RBSPOffset(base+int(off)))
	}

	return starts
}

// startSubstream is 9.3.1. A dependent slice segment carries on with the
// contexts the previous segment ended with, unless it opens a tile, which
// always initialises them.
func (d *ctuDecoder) startSubstream(nal NALUnit, sh *sliceHeader, starts []int, i int,
	tileStart bool,
) error {
	if i >= len(starts) {
		return ErrInvalid
	}

	if err := d.c.init(nal.RBSP, starts[i]); err != nil {
		return err
	}

	// 8.6.1 restarts qPY_PREV at a slice or a tile, so a dependent segment
	// carrying on inside a tile keeps the quantisation parameter it inherited.
	carry := i == 0 && sh.dependentSliceSegment && d.hasDepSaved && !tileStart

	if i == 0 && !carry {
		d.c.initContexts(sh.qpY, sh.sliceType, sh.cabacInit)
	}

	if carry {
		d.c.state = d.depSaved

		return nil
	}

	d.qpYCur = sh.qpY
	d.qpYPrev = sh.qpY

	return nil
}

// codingTreeUnit is 7.3.8.2.
func (d *ctuDecoder) codingTreeUnit(x, y int) error {
	if d.sh.saoLuma || d.sh.saoChroma {
		d.parseSAO(x, y)
	}

	return d.codingQuadtree(x, y, int(d.s.ctbLog2SizeY), 0)
}

func (d *Decoder) buildRefLists(sh *sliceHeader) {
	for l := range 2 {
		d.ctu.refPOC[l], d.ctu.refPics[l], d.ctu.refLong[l] = nil, nil, nil

		if sh.sliceType == sliceI || (l == 1 && sh.sliceType != sliceB) {
			continue
		}

		active := int(sh.numRefIdxL0Active)
		entries := sh.listModification.listEntryL0
		modify := sh.listModification.flagL0

		if l == 1 {
			active = int(sh.numRefIdxL1Active)
			entries = sh.listModification.listEntryL1
			modify = sh.listModification.flagL1
		}

		pocs := buildRefPicList(&d.curRPS, active, modify, entries, l == 1)

		d.ctu.refPOC[l] = pocs
		d.ctu.refPics[l] = make([]*Picture, len(pocs))
		d.ctu.refLong[l] = make([]bool, len(pocs))

		for i, p := range pocs {
			d.ctu.refPics[l][i] = d.dpbFind(p)
			d.ctu.refLong[l][i] = d.dpbLongTerm(p)
		}
	}

	d.ctu.noBackwardPred = true

	for l := range 2 {
		for _, p := range d.ctu.refPOC[l] {
			if p > int32(d.ctu.poc) {
				d.ctu.noBackwardPred = false
			}
		}
	}

	d.ctu.colPic = nil

	if sh.temporalMvp {
		l := 1
		if sh.collocatedFromL0 {
			l = 0
		}

		if int(sh.collocatedRefIdx) < len(d.ctu.refPics[l]) {
			d.ctu.colPic = d.ctu.refPics[l][sh.collocatedRefIdx]
		}
	}
}
