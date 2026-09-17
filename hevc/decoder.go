/*
Package hevc decodes an HEVC (H.265) bitstream, and encodes an intra-only one.

[Decoder.DecodeNAL] takes one complete NAL unit at a time. When its last slice
finishes reconstruction, a picture is filtered and made eligible for output in
that same call. A stream that codes out of display order can still hold pictures
back under sps_max_num_reorder_pics and release them by picture order count.
[Decoder.Flush] drains what is left at the end of the sequence.

The first slice's [NALUnit.Tag] follows its picture through output reordering.
[Decoder.Reset] discards state without emitting pictures, for example after
unrepaired transport loss.

	var d hevc.Decoder

	for _, nal := range hevc.SplitAnnexB(data) {
		pics, err := d.DecodeNAL(nal)
		for _, p := range pics {
			p.Release()
		}
		if err != nil {
			return err
		}
	}

[SplitAnnexB] frames a start-code delimited stream and [SplitHVCC] a
length-prefixed one.

# Pictures

A [Picture] holds its planes as either 8-bit or 16-bit samples, in Y/Cb/Cr or
Y16/Cb16/Cr16, chosen by the sequence rather than by the plane: both are 16-bit
if either [Picture.BitDepth] or [Picture.BitDepthC] exceeds 8, which 7.4.3.2
allows to differ. Width and Height stay as decoded because prediction reads the
whole plane; CropX, CropY, CropW and CropH are what a caller should display.

[Picture.Release] hands the planes back to the decoder to be reused by a later
picture. It is optional, since a picture that is never released is collected
like any other value, but it keeps a long sequence from allocating a fresh set
of planes per frame. Reading the planes afterwards is a mistake; releasing twice
is not.

# Threading

[Decoder.Threads] bounds the goroutines a picture may be spread over, across
wavefront rows and the loop filter row bands. Zero means GOMAXPROCS and one
decodes serially. A picture without entropy_coding_sync_enabled_flag, or one a
single block wide, is serial whatever the bound.

# Encoding

[Encoder] writes self-contained intra IDR access units from 8-bit 4:2:0 frames
whose dimensions are non-zero and even. A picture that does not fill the coding
grid is padded to it and cropped back by a conformance window. Every frame is
coded on its own, so [Encoder.Flush] never has anything left to return.

	enc, err := hevc.NewEncoder(hevc.EncoderOptions{Width: 1920, Height: 1080, QP: 26})
	if err != nil {
		return err
	}

	nals, err := enc.Encode(hevc.Frame{Y: y, Cb: cb, Cr: cr, StrideY: ys, StrideC: cs})

[MarshalAnnexB] frames the result for a file and [MarshalNAL] writes one unit
for a length-prefixed container, whose configuration record repeats the
[ProfileTierLevel] of the sequence parameter set.

A picture is one slice of 64x64 coding tree blocks, coded as 32x32 units and as
16x16 ones along an edge a 32x32 does not fit. Prediction searches all 35 intra
modes and the 8x8 transform blocks choose between one transform and four.
[EncoderOptions.Lossless] codes the samples as PCM instead and ignores QP.

# Errors

[ErrInvalid] means the bitstream is malformed. [ErrUnsupported] means it is
valid and declares a coding tool this decoder does not implement, which is
refused rather than decoded into a picture that merely looks plausible. Those
tools are cross-component prediction, implicit and explicit RDPCM, and CABAC
bypass alignment; everything else in the range extensions is applied.
*/
package hevc

// Decoder decodes a HEVC bitstream one NAL unit at a time.
type Decoder struct {
	vps map[uint8]*vps
	sps map[uint32]*sps
	pps map[uint32]*pps

	cur      *Picture
	curOut   bool
	ctu      *ctuDecoder
	ctuPrev  *ctuDecoder
	prevSlic *sliceHeader

	dpb            []dpbPicture
	pool           picPool
	threads        int
	frameSizeLimit int
	poc            pocState
	curRPS         refPicSet

	// 8.1.3: leading pictures associated with an intra random access point
	// that starts decoding reference pictures that were never decoded, and
	// are discarded rather than decoded.
	seenPicture bool
	skipRASL    bool

	maxReorder    int
	maxLatency    int
	maxDecPicBuf  int
	curType       NALType
	curTemporalID uint8
}

// FrameSizeLimit refuses a sequence whose pictures are larger than n samples,
// with ErrUnsupported. Zero, the default, accepts anything the level allows.
func (d *Decoder) FrameSizeLimit(n int) { d.frameSizeLimit = n }

// DecodeNAL consumes one complete NAL unit and returns zero or more pictures in
// output order. The final slice completes and filters its picture immediately;
// SPS reordering limits decide whether it can be returned now or must wait for
// later pictures or Flush. Earlier slices can be submitted in separate calls.
// Each picture carries the Tag of its first slice, including a zero tag.
//
// Input bytes may be reused after this call. Returned pictures, including any
// returned with an error, belong to the caller until Release. An error resets
// the decoder; supply parameter sets again before resuming at a random access point.
func (d *Decoder) DecodeNAL(nal NALUnit) (out []*Picture, err error) {
	defer func() {
		if err != nil {
			d.Reset()
		}
	}()
	if nal.LayerID != 0 {
		return nil, ErrUnsupported
	}
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

	// NoRaslOutputFlag belongs to the picture. A later slice of the same CRA
	// must not undo the random-access decision made before its first slice.
	if nal.Type.IsIRAP() && len(nal.RBSP) != 0 && nal.RBSP[0]&0x80 != 0 {
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

// Reset discards all decoding state without emitting output: parameter sets,
// references, reordering and POC history, unfinished pictures, and cached buffers.
// Thread and size limits are preserved. Pictures already returned to the caller
// remain valid until Release, and cannot repopulate the reset decoder's pool.
// A Decoder and its pictures must not be used concurrently.
func (d *Decoder) Reset() {
	if d.cur != nil {
		d.cur.release()
	}
	d.clearDPB()
	d.pool.reset()
	*d = Decoder{
		pool:           d.pool,
		threads:        d.threads,
		frameSizeLimit: d.frameSizeLimit,
	}
}

// Flush ends the sequence and returns every picture still held back for
// reordering, in output order. An incomplete final picture is discarded.
// References and POC history are cleared; parameter sets and configured limits
// are retained. Use Reset instead to discard pending output and parameter sets.
func (d *Decoder) Flush() []*Picture {
	out := d.finishPicture()

	for {
		p := d.dpbBump()
		if p == nil {
			break
		}

		out = append(out, p)
	}

	d.cur, d.ctu, d.prevSlic = nil, nil, nil
	d.curRPS, d.poc = refPicSet{}, pocState{}
	d.seenPicture, d.skipRASL = false, false
	d.clearDPB()

	return out
}

// pictureComplete checks reconstruction coverage, not just successful parsing
// of the last slice. A slice may end legally before the picture is complete.
func (d *Decoder) pictureComplete() bool {
	return d.ctu != nil && d.ctu.nextCTU ==
		int(d.ctu.s.picWidthInCtbs)*int(d.ctu.s.picHeightInCtbs)
}

// finishPicture runs the loop filters over the picture the decoder has been
// filling, files it in the buffer and applies the additional bumping of
// C.5.2.3.
func (d *Decoder) finishPicture() []*Picture {
	if d.cur == nil {
		return nil
	}
	if !d.pictureComplete() {
		// Flush cannot report an error. Never turn an incomplete final picture
		// into output or a reference; older complete pictures may still drain.
		d.cur.release()
		d.cur, d.ctu, d.ctuPrev, d.prevSlic = nil, nil, nil, nil
		return nil
	}

	if d.ctu != nil {
		d.ctu.storeColMotion()
		d.ctu.deblock()
		d.ctu.applySAO()
	}

	d.dpbStore(d.cur, d.curOut)

	// ctu is the signal that a picture is in progress; ctuPrev keeps its
	// buffers and its scan tables for the next one.
	d.cur, d.ctu = nil, nil

	return d.dpbDrain(false)
}

func (d *Decoder) decodeSlice(nal NALUnit) ([]*Picture, error) {
	p, first, err := d.ppsForSlice(nal)
	if err != nil {
		return nil, err
	}

	s, ok := d.sps[p.spsID]
	if !ok {
		return nil, ErrInvalid
	}

	// 7.4.3.2.1 activates one pair of parameter sets for the whole picture.
	if !first && d.ctu != nil {
		if p.id != d.ctu.p.id {
			return nil, ErrInvalid
		}

		s, p = d.ctu.s, d.ctu.p
	}
	if !first && d.ctu == nil {
		return nil, ErrInvalid
	}

	if n := d.frameSizeLimit; n > 0 &&
		int(s.picWidthInLumaSamples)*int(s.picHeightInLumaSamples) > n {
		return nil, ErrUnsupported
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
		if d.cur != nil {
			// Complete pictures are finished by their final slice. A new first
			// slice while cur is live therefore abandons an incomplete picture.
			return nil, ErrInvalid
		}
		prior := len(d.dpb) != 0

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

			d.clearDPB()
		default:
			d.dpbMark(&rps)
			d.dpbRemoveUnused()
			done = append(done, d.dpbDrain(true)...)
		}

		if sh.sliceType != sliceI {
			d.generateUnavailable(&rps, s)
		}

		d.curRPS = rps

		d.cur = newPicture(&d.pool, s)
		d.cur.POC = int(poc)
		d.cur.Tag = nal.Tag
		d.curType, d.curTemporalID = nal.Type, nal.TemporalID
		d.curOut = sh.picOutputFlag
		d.ctu = newCTUDecoder(d.ctuPrev, s, p, sh, d.cur)
		d.ctu.threads = d.waveThreads()
		d.ctuPrev = d.ctu
		d.ctu.poc = poc
	}

	if d.cur == nil || d.ctu == nil {
		return done, ErrInvalid
	}
	if nal.Type != d.curType || nal.TemporalID != d.curTemporalID ||
		sh.picOrderCntLsb != d.ctu.sh.picOrderCntLsb || sh.picOutputFlag != d.curOut {
		return done, ErrInvalid
	}

	d.ctu.sh = sh

	// 7.4.7.1 signals the active reference counts and the list modification in
	// every slice header, so the lists belong to the slice, not the picture.
	d.buildRefLists(sh)

	err = d.ctu.decodeSliceData(nal, sh)
	// CABAC has finished consuming this NAL, even for dependent segments:
	// only its probability contexts carry over to the next segment.
	d.ctu.c.data = nil
	if err == nil && d.pictureComplete() {
		// All coding-tree blocks have been reconstructed, including every WPP
		// worker. Filter and store the picture now instead of waiting for a
		// later NAL to announce the next picture. DPB bumping still controls
		// which completed pictures are eligible for display.
		done = append(done, d.finishPicture()...)
	}
	return done, err
}

func (d *Decoder) ppsForSlice(nal NALUnit) (*pps, bool, error) {
	var c getBits
	c.init(nal.RBSP)

	first := c.bit() != 0

	if nal.Type >= NALBlaWLP && nal.Type <= 23 {
		c.bit()
	}

	id := c.ue()
	if c.err {
		return nil, false, ErrInvalid
	}

	p, ok := d.pps[id]
	if !ok {
		return nil, false, ErrInvalid
	}

	return p, first, nil
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
	// 7.4.2.4.5 orders segments by tile scan. A gap means a slice was lost;
	// an overlap would reconstruct a block twice with inconsistent predictors.
	if start != d.nextCTU {
		return ErrInvalid
	}
	tileStart := start == 0 || d.tileID[start] != d.tileID[start-1]

	if err := d.startSubstream(nal, sh, starts, sub, tileStart); err != nil {
		return err
	}

	wpp := d.p.entropyCodingSync

	d.sliceLF[d.sliceAddrRs] = sh.loopFilterAcross

	d.slices = append(d.slices, sh)
	cur := int32(len(d.slices) - 1)

	if n := d.waveWorkers(sh, starts, wpp); n > 1 {
		return d.decodeWavefront(nal, sh, starts, cur, n)
	}

	for ts := start; ts < total; ts++ {
		rs := int(d.tsToRs[ts])

		d.ctbSliceAddr[rs] = int32(d.sliceAddrRs)
		d.ctbSlice[rs] = cur
		x := rs % w << d.s.ctbLog2SizeY
		y := rs / w << d.s.ctbLog2SizeY

		if wpp && rs%w == 0 {
			// 9.3.1 syncs from the block above-right, but only when 6.4.1
			// makes it available. A new slice starting on a row boundary has
			// to initialise instead, and so does every row of a picture one
			// block wide, which never has that neighbour at all.
			top := rs - w + 1
			availT := w >= 2 && rs >= w && top >= d.sliceAddrRs &&
				d.tileID[d.rsToTs[rs]] == d.tileID[d.rsToTs[top]]

			oneWide := w < 2 && rs > 0

			switch {
			case availT && d.hasSaved:
				d.c.state = d.saved
			case ts > start || oneWide:
				d.c.initContexts(sh.qpY, sh.sliceType, sh.cabacInit)
			}

			d.qpYPrev = sh.qpY
		}

		if err := d.codingTreeUnit(x, y); err != nil {
			return err
		}
		d.nextCTU = ts + 1

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

	// The final CTU also carries end_of_slice_segment_flag.
	return ErrInvalid
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
