package hevc

import "slices"

const (
	partMode2Nx2N = iota
	partMode2NxN
	partModeNx2N
	partModeNxN
	partMode2NxnU
	partMode2NxnD
	partModenLx2N
	partModenRx2N
)

// Table 8-10 for ChromaArrayType 1, indexed by qPi - 30.
var chromaQPTable = [14]int8{29, 30, 31, 32, 33, 33, 34, 34, 35, 35, 36, 36, 37, 37}

func chromaQP(qPi int32, chromaArrayType uint32) int32 {
	if chromaArrayType != 1 {
		return min(qPi, 51)
	}

	switch {
	case qPi < 30:
		return qPi
	case qPi > 43:
		return qPi - 6
	default:
		return int32(chromaQPTable[qPi-30])
	}
}

type ctuDecoder struct {
	c   cabac
	s   *sps
	p   *pps
	sh  *sliceHeader
	pic *Picture

	minTbLog2  int
	minTbWidth int
	minTbAddr  []int32

	intraMode  []uint8
	intraModeC []uint8
	cuDepth    []uint8
	qpY        []int8
	skipped    []bool
	noFilter   []bool
	bypass     bool

	qpYPrev  int32
	qpYCur   int32
	qpYPred  int32
	qgX, qgY int
	qpCoded  bool
	qpDelta  int32
	statCoef [4]uint8
	scaling  [maxScalingListSizes][maxScalingListMats][]uint8

	// scalingFrom is the list d.scaling was derived from. Parsing a parameter
	// set yields a new one, so the pointer changing is the only invalidation.
	scalingFrom *scalingList
	saoType     [3]int
	eoClass     [2]int
	sao         [][3]saoParams

	blk       []blockInfo
	mvField   []mvInfo
	mvPoc     [][2]int32
	mvLong    [][2]bool
	mvValid   []bool
	mvWidth   int
	curX      int
	curY      int
	cuX       int
	cuY       int
	cuSize    int
	puW       int
	puH       int
	partIdx   int
	curIntra  bool
	lastMerge bool

	refPOC         [2][]int32
	refLong        [2][]bool
	refPics        [2][]*Picture
	poc            int32
	colPic         *Picture
	noBackwardPred bool

	rsToTs  []int32
	tsToRs  []int32
	tileID  []int32
	scanFor scanGeometry
	built   bool

	saved    [nContexts]uint8
	hasSaved bool

	depSaved    [nContexts]uint8
	hasDepSaved bool

	threads        int
	sliceAddrRs    int
	simpleAvail    bool
	ctbSliceAddr   []int32
	sliceLF        []bool
	ctbSlice       []int32
	slices         []*sliceHeader
	depSliceAddrRs int

	coef    [32 * 32]int32
	scratch transformScratch

	// Scratch for one prediction unit, the largest being 64x64 luma. Motion
	// compensation runs for every unit, so none of this may be allocated.
	saoSrc8  []uint8
	saoSrc16 []uint16

	mcBuf   [2][64 * 64]int16
	mcTmp   [(64 + 7) * 64]int32
	mcTmp16 [(64 + 7) * 64]int16
	mcPad8  [(64 + 7) * (64 + 7)]uint8
	mcPad16 [(64 + 7) * (64 + 7)]uint16
	mergeCd [8]mvInfo
	ref     refSamples
	avail   [4*32 + 1]bool
}

// scanGeometry is everything the tile scan and the z-scan address table are
// built from. Every picture of a sequence shares it, and the tables cost more
// than the rest of the per-picture setup put together.
type scanGeometry struct {
	widthInCtbs  uint32
	heightInCtbs uint32
	ctbLog2SizeY uint8
	minTbLog2    uint8
	cols         []uint32
	rows         []uint32
}

func geometryOf(s *sps, p *pps) scanGeometry {
	return scanGeometry{
		widthInCtbs:  s.picWidthInCtbs,
		heightInCtbs: s.picHeightInCtbs,
		ctbLog2SizeY: s.ctbLog2SizeY,
		minTbLog2:    s.minTbLog2SizeY,
		cols:         p.colWidthsInCtbs,
		rows:         p.rowHeightsInCtbs,
	}
}

func (g scanGeometry) same(o scanGeometry) bool {
	return g.widthInCtbs == o.widthInCtbs && g.heightInCtbs == o.heightInCtbs &&
		g.ctbLog2SizeY == o.ctbLog2SizeY && g.minTbLog2 == o.minTbLog2 &&
		slices.Equal(g.cols, o.cols) && slices.Equal(g.rows, o.rows)
}

// newCTUDecoder prepares the per-picture state, reusing prev's buffers. They
// are sized from the sequence and grow when a larger one arrives.
func newCTUDecoder(prev *ctuDecoder, s *sps, p *pps, sh *sliceHeader, pic *Picture) *ctuDecoder {
	d := prev
	if d == nil {
		d = &ctuDecoder{}
	}

	minTbLog2 := int(s.minTbLog2SizeY)
	tbW := (int(s.picWidthInLumaSamples) + 1<<minTbLog2 - 1) >> minTbLog2
	tbH := (int(s.picHeightInLumaSamples) + 1<<minTbLog2 - 1) >> minTbLog2
	mvW := (int(s.picWidthInLumaSamples) + 3) / 4
	mvH := (int(s.picHeightInLumaSamples) + 3) / 4
	ctbs := int(s.picWidthInCtbs) * int(s.picHeightInCtbs)

	saoSrc8, saoSrc16 := d.saoSrc8, d.saoSrc16
	scaling, scalingFrom := d.scaling, d.scalingFrom
	threads := d.threads

	*d = ctuDecoder{
		s: s, p: p, sh: sh, pic: pic,

		minTbLog2:  minTbLog2,
		minTbWidth: tbW,
		mvWidth:    mvW,

		minTbAddr:  keep(d.minTbAddr, tbW*tbH),
		intraMode:  reuse(d.intraMode, tbW*tbH),
		intraModeC: reuse(d.intraModeC, tbW*tbH),
		cuDepth:    reuse(d.cuDepth, tbW*tbH),
		qpY:        reuse(d.qpY, tbW*tbH),
		skipped:    reuse(d.skipped, tbW*tbH),
		noFilter:   reuse(d.noFilter, tbW*tbH),

		mvField: reuse(d.mvField, mvW*mvH),
		mvPoc:   reuse(d.mvPoc, mvW*mvH),
		mvLong:  reuse(d.mvLong, mvW*mvH),
		mvValid: reuse(d.mvValid, mvW*mvH),
		blk:     reuse(d.blk, mvW*mvH),

		ctbSliceAddr: reuse(d.ctbSliceAddr, ctbs),
		ctbSlice:     reuse(d.ctbSlice, ctbs),
		sao:          reuse(d.sao, ctbs),
		sliceLF:      reuse(d.sliceLF, ctbs),

		rsToTs:  d.rsToTs,
		tsToRs:  d.tsToRs,
		tileID:  d.tileID,
		scanFor: d.scanFor,
		built:   d.built,

		saoSrc8:  saoSrc8,
		saoSrc16: saoSrc16,

		scaling:     scaling,
		scalingFrom: scalingFrom,
		threads:     threads,

		qpYPrev: sh.qpY,
		qpYCur:  sh.qpY,
	}

	if g := geometryOf(s, p); !g.same(d.scanFor) {
		d.scanFor = g
		d.built = false

		d.buildTileScan()
	}

	if !d.built {
		d.built = true

		d.buildMinTbAddr(tbH)
	}

	sl := (*scalingList)(nil)

	if s.scalingListEnabled {
		sl = &s.scalingList
		if p.scalingListPresent {
			sl = &p.scalingList
		}
	}

	if sl != d.scalingFrom {
		d.scalingFrom = sl
		d.scaling = [maxScalingListSizes][maxScalingListMats][]uint8{}

		if sl != nil {
			d.scaling = sl.factors()
		}
	}

	return d
}

// keep returns a buffer of n elements without clearing it, for a table that is
// either rewritten in full or carried over.
func keep[T any](b []T, n int) []T {
	if cap(b) < n {
		return make([]T, n)
	}

	return b[:n]
}

// reuse returns a zeroed buffer of n elements, keeping b's memory when it fits.
func reuse[T any](b []T, n int) []T {
	if cap(b) < n {
		return make([]T, n)
	}

	b = b[:n]
	clear(b)

	return b
}

// buildMinTbAddr is the MinTbAddrZs derivation of 6.5.2, which the
// availability process in 6.4.1 compares against.
func (d *ctuDecoder) buildMinTbAddr(h int) {
	shift := int(d.s.ctbLog2SizeY) - d.minTbLog2

	for y := range h {
		for x := range d.minTbWidth {
			tbX := (x << d.minTbLog2) >> d.s.ctbLog2SizeY
			tbY := (y << d.minTbLog2) >> d.s.ctbLog2SizeY

			ctb := int(d.rsToTs[int(d.s.picWidthInCtbs)*tbY+tbX])

			v := ctb << (shift * 2)

			for i := range shift {
				m := 1 << i

				if m&x != 0 {
					v += m * m
				}

				if m&y != 0 {
					v += 2 * m * m
				}
			}

			d.minTbAddr[y*d.minTbWidth+x] = int32(v)
		}
	}
}

func (d *ctuDecoder) tbIndex(x, y int) int {
	return (y>>d.minTbLog2)*d.minTbWidth + x>>d.minTbLog2
}

// available is the z-scan availability of 6.4.1.
func (d *ctuDecoder) available(xCurr, yCurr, xN, yN int) bool {
	if xN < 0 || yN < 0 ||
		xN >= int(d.s.picWidthInLumaSamples) || yN >= int(d.s.picHeightInLumaSamples) {
		return false
	}

	if d.minTbAddr[d.tbIndex(xN, yN)] >= d.minTbAddr[d.tbIndex(xCurr, yCurr)] {
		return false
	}

	// With one tile and a slice starting at the first block, everything
	// earlier in z-scan order is in the same slice and the same tile.
	if d.simpleAvail {
		return true
	}

	cw := int(d.s.picWidthInCtbs)
	curRs := (yCurr>>d.s.ctbLog2SizeY)*cw + xCurr>>d.s.ctbLog2SizeY
	nbRs := (yN>>d.s.ctbLog2SizeY)*cw + xN>>d.s.ctbLog2SizeY

	if d.tileID[d.rsToTs[curRs]] != d.tileID[d.rsToTs[nbRs]] {
		return false
	}

	// 6.4.1 wants the neighbour in the same slice. Slices follow the tile scan,
	// so a raster comparison would admit one from a slice already finished.
	return d.ctbSliceAddr[nbRs] == int32(d.sliceAddrRs)
}

// intraChromaMode is Table 8-2 and, for 4:2:2, Table 8-3.
func (d *ctuDecoder) intraChromaMode(luma int) int {
	mode := luma

	if d.c.decodeBin(ctxIntraChromaPredMode) != 0 {
		idx := int(d.c.decodeBypassBits(2))

		mode = [4]int{intraPlanar, intraVer, intraHor, intraDC}[idx]
		if mode == luma {
			mode = 34
		}
	}

	if d.s.chromaFormatIDC == 2 {
		mode = int(chroma422Map[mode])
	}

	return mode
}

// Table 8-3.
var chroma422Map = [35]uint8{
	0, 1, 2, 2, 2, 2, 3, 5, 7, 8, 10, 12, 13, 15, 17, 18, 19, 20,
	21, 22, 23, 23, 24, 24, 25, 25, 26, 27, 27, 28, 28, 29, 29, 30, 31,
}

func (d *ctuDecoder) codingQuadtree(x, y, log2Size, depth int) error {
	split := log2Size > int(d.s.minCbLog2SizeY)

	if log2Size > int(d.s.minCbLog2SizeY) &&
		x+1<<log2Size <= int(d.s.picWidthInLumaSamples) &&
		y+1<<log2Size <= int(d.s.picHeightInLumaSamples) {
		ctx := 0

		if d.available(x, y, x-1, y) && int(d.cuDepth[d.tbIndex(x-1, y)]) > depth {
			ctx++
		}

		if d.available(x, y, x, y-1) && int(d.cuDepth[d.tbIndex(x, y-1)]) > depth {
			ctx++
		}

		split = d.c.decodeBin(ctxSplitCodingUnitFlag+ctx) != 0
	}

	if d.p.cuQPDeltaEnabled &&
		log2Size >= int(d.s.ctbLog2SizeY)-int(d.p.diffCuQPDeltaDepth) {
		d.qpCoded = false
		d.qpDelta = 0
		d.qgX, d.qgY = x, y
		d.qpYPred = d.predictQP(x, y)
		d.qpYCur = d.qpYPred
	}

	if !split {
		return d.codingUnit(x, y, log2Size, depth)
	}

	half := 1 << (log2Size - 1)

	for i := range 4 {
		nx, ny := x+i&1*half, y+i>>1*half

		if nx >= int(d.s.picWidthInLumaSamples) || ny >= int(d.s.picHeightInLumaSamples) {
			continue
		}

		if err := d.codingQuadtree(nx, ny, log2Size-1, depth+1); err != nil {
			return err
		}
	}

	return nil
}

// predictQP is qPY_PRED of 8.6.1.
func (d *ctuDecoder) predictQP(x, y int) int32 {
	ctbMask := ^(1<<d.s.ctbLog2SizeY - 1)

	qpA, qpB := d.qpYPrev, d.qpYPrev

	if d.available(x, y, x-1, y) && (x-1)&ctbMask == x&ctbMask && y&ctbMask == y&ctbMask {
		qpA = int32(d.qpY[d.tbIndex(x-1, y)])
	}

	if d.available(x, y, x, y-1) && x&ctbMask == x&ctbMask && (y-1)&ctbMask == y&ctbMask {
		qpB = int32(d.qpY[d.tbIndex(x, y-1)])
	}

	return (qpA + qpB + 1) >> 1
}

// parseCuQPDelta reads cu_qp_delta_abs and its sign, 9.3.3.10.
func (d *ctuDecoder) parseCuQPDelta() {
	prefix := 0
	for prefix < 5 {
		ctx := 1
		if prefix == 0 {
			ctx = 0
		}

		if d.c.decodeBin(ctxCUQPDelta+ctx) == 0 {
			break
		}

		prefix++
	}

	v := int32(prefix)

	if prefix > 4 {
		k := 0
		for d.c.decodeBypass() != 0 {
			k++
		}

		v = int32(5 + (1<<k - 1) + int(d.c.decodeBypassBits(k)))
	}

	if v != 0 && d.c.decodeBypass() != 0 {
		v = -v
	}

	d.qpDelta = v
	d.qpCoded = true

	off := 6 * (int32(d.s.bitDepthLuma) - 8)
	d.qpYCur = (d.qpYPred+d.qpDelta+52+2*off)%(52+off) - off
}

// fill writes one value across every minimum transform block a coding block
// covers, which is the granularity every per-block array uses.
func fill[T any](d *ctuDecoder, dst []T, x, y, size int, v T) {
	step := 1 << d.minTbLog2

	for j := y; j < y+size; j += step {
		for i := x; i < x+size; i += step {
			if i < int(d.s.picWidthInLumaSamples) && j < int(d.s.picHeightInLumaSamples) {
				dst[d.tbIndex(i, j)] = v
			}
		}
	}
}

// codingUnit is 7.3.8.5. QpY of 8.6.1 covers the whole coding unit, including
// one that carries no residual at all, since deblocking reads it back.
func (d *ctuDecoder) codingUnit(x, y, log2Size, depth int) error {
	if err := d.codingUnitData(x, y, log2Size, depth); err != nil {
		return err
	}

	// 8.7.2.2 marks the coding block boundary even with no transform tree.
	d.markTU(x, y, 1<<log2Size, 1<<log2Size, false)

	d.setQP(x, y, 1<<log2Size)

	return nil
}

func (d *ctuDecoder) codingUnitData(x, y, log2Size, depth int) error {
	size := 1 << log2Size

	fill(d, d.cuDepth, x, y, size, uint8(depth))

	d.bypass = d.p.transquantBypass && d.c.decodeBin(ctxCUTransquantBypassFlag) != 0
	if d.bypass {
		fill(d, d.noFilter, x, y, size, true)
	}

	skip := false

	if d.sh.sliceType != sliceI {
		ctx := 0

		if d.available(x, y, x-1, y) && d.skipped[d.tbIndex(x-1, y)] {
			ctx++
		}

		if d.available(x, y, x, y-1) && d.skipped[d.tbIndex(x, y-1)] {
			ctx++
		}

		skip = d.c.decodeBin(ctxSkipFlag+ctx) != 0
	}

	fill(d, d.skipped, x, y, size, skip)

	if skip {
		d.curIntra = false

		return d.predictionUnit(x, y, size, size, 0, partMode2Nx2N, depth, size, x, y, true)
	}

	intra := true

	if d.sh.sliceType != sliceI {
		intra = d.c.decodeBin(ctxPredModeFlag) != 0
	}

	partMode := partMode2Nx2N

	if !intra || log2Size == int(d.s.minCbLog2SizeY) {
		partMode = d.partMode(intra, log2Size)
	}

	if !intra {
		d.curIntra = false

		return d.interCU(x, y, log2Size, depth, partMode)
	}

	d.curIntra = true

	d.markPU(x, y, size, size, true)

	if partMode != partMode2Nx2N && partMode != partModeNxN {
		return ErrInvalid
	}

	if d.s.pcmEnabled && log2Size >= int(d.s.log2MinPcmCbSize) &&
		log2Size <= int(d.s.log2MaxPcmCbSize) && partMode == partMode2Nx2N &&
		d.c.decodeTerminate() != 0 {
		return d.pcmSample(x, y, log2Size)
	}

	parts := 1
	if partMode == partModeNxN {
		parts = 4
	}

	pbSize := size
	if parts == 4 {
		pbSize = size / 2
	}

	var lumaModes [4]int

	prevFlags := make([]bool, parts)
	for i := range parts {
		prevFlags[i] = d.c.decodeBin(ctxPrevIntraLumaPredFlag) != 0
	}

	for i := range parts {
		px, py := x+i&1*pbSize, y+i>>1*pbSize
		lumaModes[i] = d.intraLumaModeWithFlag(px, py, prevFlags[i])
		fill(d, d.intraMode, px, py, pbSize, uint8(lumaModes[i]))
	}

	// 7.3.8.5 codes intra_chroma_pred_mode once per prediction block when
	// ChromaArrayType is 3, and once per coding unit otherwise.
	switch {
	case d.s.chromaArrayType() == 3:
		for i := range parts {
			px, py := x+i&1*pbSize, y+i>>1*pbSize
			fill(d, d.intraModeC, px, py, pbSize, uint8(d.intraChromaMode(lumaModes[i])))
		}
	case d.s.chromaArrayType() != 0:
		fill(d, d.intraModeC, x, y, size, uint8(d.intraChromaMode(lumaModes[0])))
	default:
		fill(d, d.intraModeC, x, y, size, uint8(lumaModes[0]))
	}

	return d.transformTree(x, y, x, y, log2Size, 0, 0, lumaModes, partMode, [2]bool{}, [2]bool{})
}

// intraLumaModeWithFlag completes 8.4.2 once prev_intra_luma_pred_flag has been
// read; the flags for all partitions precede the indices in the syntax.
func (d *ctuDecoder) intraLumaModeWithFlag(x, y int, prev bool) int {
	candA, candB := intraDC, intraDC

	// 8.4.2: a neighbour that is not intra coded contributes INTRA_DC, as does
	// one above the current coding tree block row.
	if d.available(x, y, x-1, y) && d.blk[d.blkIndex(x-1, y)].intra {
		candA = int(d.intraMode[d.tbIndex(x-1, y)])
	}

	ctbMask := ^(1<<d.s.ctbLog2SizeY - 1)

	if d.available(x, y, x, y-1) && y-1 >= y&ctbMask && d.blk[d.blkIndex(x, y-1)].intra {
		candB = int(d.intraMode[d.tbIndex(x, y-1)])
	}

	var cand [3]int

	switch {
	case candA == candB && candA < 2:
		cand = [3]int{intraPlanar, intraDC, intraVer}
	case candA == candB:
		cand = [3]int{candA, 2 + (candA+29)%32, 2 + (candA-2+1)%32}
	default:
		cand[0], cand[1] = candA, candB

		switch {
		case candA != intraPlanar && candB != intraPlanar:
			cand[2] = intraPlanar
		case candA != intraDC && candB != intraDC:
			cand[2] = intraDC
		default:
			cand[2] = intraVer
		}
	}

	if prev {
		idx := 0
		if d.c.decodeBypass() != 0 {
			idx = 1
			if d.c.decodeBypass() != 0 {
				idx = 2
			}
		}

		return cand[idx]
	}

	if cand[0] > cand[1] {
		cand[0], cand[1] = cand[1], cand[0]
	}

	if cand[0] > cand[2] {
		cand[0], cand[2] = cand[2], cand[0]
	}

	if cand[1] > cand[2] {
		cand[1], cand[2] = cand[2], cand[1]
	}

	mode := int(d.c.decodeBypassBits(5))
	for i := range 3 {
		if mode >= cand[i] {
			mode++
		}
	}

	return mode
}

func (d *ctuDecoder) transformTree(x, y, xBase, yBase, log2Size, depth, blkIdx int,
	lumaModes [4]int, partMode int, cbfCbUp, cbfCrUp [2]bool,
) error {
	intraSplit := d.curIntra && partMode == partModeNxN

	maxDepth := int(d.s.maxTrHierInter)
	if d.curIntra {
		maxDepth = int(d.s.maxTrHierIntra)
	}

	if intraSplit {
		maxDepth++
	}

	// 7.3.8.8: with no inter hierarchy an inter unit that is not 2Nx2N still
	// splits once.
	interSplit := !d.curIntra && d.s.maxTrHierInter == 0 &&
		partMode != partMode2Nx2N && depth == 0

	split := log2Size > int(d.s.maxTbLog2SizeY) || (intraSplit && depth == 0) || interSplit

	if log2Size <= int(d.s.maxTbLog2SizeY) && log2Size > int(d.s.minTbLog2SizeY) &&
		depth < maxDepth && !(intraSplit && depth == 0) && !interSplit {
		split = d.c.decodeBin(ctxSplitTransformFlag+5-log2Size) != 0
	}

	cbfCb, cbfCr := cbfCbUp, cbfCrUp

	if d.s.chromaArrayType() != 0 && (log2Size > 2 || d.s.chromaArrayType() == 3) {
		// 7.3.8.8: 4:2:2 stacks two chroma transform blocks per luma block, each
		// with its own cbf, once the tree stops splitting them apart.
		second := d.s.chromaArrayType() == 2 && (!split || log2Size == 3)

		cbfCb = [2]bool{}
		if depth == 0 || cbfCbUp[0] {
			cbfCb[0] = d.c.decodeBin(ctxCBFCBCR+depth) != 0
			if second {
				cbfCb[1] = d.c.decodeBin(ctxCBFCBCR+depth) != 0
			}
		}

		cbfCr = [2]bool{}
		if depth == 0 || cbfCrUp[0] {
			cbfCr[0] = d.c.decodeBin(ctxCBFCBCR+depth) != 0
			if second {
				cbfCr[1] = d.c.decodeBin(ctxCBFCBCR+depth) != 0
			}
		}
	}

	if split {
		half := 1 << (log2Size - 1)

		for i := range 4 {
			if err := d.transformTree(x+i&1*half, y+i>>1*half, x, y, log2Size-1,
				depth+1, i, lumaModes, partMode, cbfCb, cbfCr); err != nil {
				return err
			}
		}

		return nil
	}

	// 7.3.8.8 only codes cbf_luma when the block is intra, nested, or has
	// chroma coefficients; otherwise an inter root block is inferred to have
	// luma residual.
	cbfLuma := true
	if depth != 0 || cbfCb[0] || cbfCb[1] || cbfCr[0] || cbfCr[1] || d.curIntra {
		cbfLuma = d.c.decodeBin(ctxCBFLuma+boolInt(depth == 0)) != 0
	}

	return d.transformUnit(x, y, xBase, yBase, log2Size, depth, blkIdx,
		lumaModes, partMode, cbfLuma, cbfCb, cbfCr)
}

func (d *ctuDecoder) setQP(x, y, size int) {
	fill(d, d.qpY, x, y, size, int8(d.qpYCur))

	d.qpYPrev = d.qpYCur
}

func boolInt(b bool) int {
	if b {
		return 1
	}

	return 0
}

func (d *ctuDecoder) transformUnit(x, y, xBase, yBase, log2Size, depth, blkIdx int,
	lumaModes [4]int, partMode int, cbfLuma bool, cbfCb, cbfCr [2]bool,
) error {
	// 8.4.4.2 takes the mode from IntraPredModeY at the block's own position,
	// which is not blkIdx once the transform tree splits below the partition.
	mode := lumaModes[0]
	if d.curIntra {
		mode = int(d.intraMode[d.tbIndex(x, y)])
	}

	cbfChroma := cbfCb[0] || cbfCb[1] || cbfCr[0] || cbfCr[1]

	if (cbfLuma || cbfChroma) && d.p.cuQPDeltaEnabled && !d.qpCoded {
		d.parseCuQPDelta()
	}

	d.markTU(x, y, 1<<log2Size, 1<<log2Size, cbfLuma)

	if err := d.reconstruct(x, y, log2Size, 0, mode, cbfLuma); err != nil {
		return err
	}

	if d.s.chromaArrayType() == 0 {
		return nil
	}

	sub := d.s.chromaArrayType() != 3

	if log2Size > 2 || !sub {
		c := log2Size
		cx, cy := x, y

		if sub {
			c--
			cx, cy = x/d.s.subWidthC, y/d.s.subHeightC
		}

		return d.chromaTBs(cx, cy, c, d.chromaMode(x, y), cbfCb, cbfCr)
	}

	if blkIdx != 3 {
		return nil
	}

	cx, cy := xBase/d.s.subWidthC, yBase/d.s.subHeightC

	return d.chromaTBs(cx, cy, 2, d.chromaMode(xBase, yBase), cbfCb, cbfCr)
}

// chromaTBs reconstructs the chroma transform blocks of one transform unit.
// 4:2:2 stacks two of them per component, and 7.3.8.10 codes both Cb blocks
// before either Cr block.
func (d *ctuDecoder) chromaTBs(cx, cy, c, mode int, cbfCb, cbfCr [2]bool) error {
	n := 1
	if d.s.chromaArrayType() == 2 {
		n = 2
	}

	for cIdx := 1; cIdx <= 2; cIdx++ {
		cbf := cbfCb
		if cIdx == 2 {
			cbf = cbfCr
		}

		for t := range n {
			if err := d.reconstruct(cx, cy+t<<c, c, cIdx, mode, cbf[t]); err != nil {
				return err
			}
		}
	}

	return nil
}

func (d *ctuDecoder) chromaMode(x, y int) int {
	if !d.curIntra {
		return 0
	}

	return int(d.intraModeC[d.tbIndex(x, y)])
}

func (d *ctuDecoder) reconstruct(x, y, log2Size, cIdx, mode int, cbf bool) error {
	if d.pic.BitDepth > 8 {
		plane, stride := d.pic.plane16(cIdx)

		return reconstructPlane(d, plane, stride, x, y, log2Size, cIdx, mode, cbf)
	}

	plane, stride := d.pic.plane8(cIdx)

	return reconstructPlane(d, plane, stride, x, y, log2Size, cIdx, mode, cbf)
}

func reconstructPlane[P pixel](d *ctuDecoder, plane []P, stride, x, y, log2Size, cIdx, mode int,
	cbf bool,
) error {
	n := 1 << log2Size
	bitDepth := d.pic.BitDepth

	if d.curIntra {
		gatherRef(d, plane, stride, x, y, n, cIdx)
		filterRef(&d.ref, mode, cIdx, bitDepth, d.s)
		intraPredict(plane, y*stride+x, stride, &d.ref, mode, cIdx, bitDepth)
	}

	if !cbf {
		return nil
	}

	coef := d.coef[:n*n]
	clear(coef)

	b := residualBlock{log2Size: log2Size, cIdx: cIdx, predModeIntra: mode,
		intra: d.curIntra, transquantBypass: d.bypass}

	skip, err := decodeResidual(&d.c, d.s, d.p, d.sh, coef, b, &d.statCoef)
	if err != nil {
		return err
	}

	// 8.6.3 scales by Qp'Y, which carries the bit depth offset QpY does not.
	offY := 6 * (int32(d.s.bitDepthLuma) - 8)
	offC := 6 * (int32(d.s.bitDepthChroma) - 8)

	qp := d.qpYCur + offY

	if cIdx > 0 {
		off := d.p.cbQPOffset + d.sh.cbQPOffset
		if cIdx == 2 {
			off = d.p.crQPOffset + d.sh.crQPOffset
		}

		qp = chromaQP(clip3(d.qpYCur+off, -offC, 57), d.s.chromaArrayType()) + offC
	}

	// 8.6.2: a bypassed unit takes the coefficients as the residual, with no
	// scaling and no transform.
	shift := 0

	if !d.bypass {
		k := dsp

		k.dequant(coef, d.scalingFactor(log2Size, cIdx, skip), n, int(qp), bitDepth,
			d.s.extendedPrecision)

		if skip {
			k.transformSkip(coef, n, false)
		} else {
			k.inverseTransform(coef, n, d.curIntra && cIdx == 0 && log2Size == 2, bitDepth,
				d.s.extendedPrecision, &d.scratch)
		}

		shift = residualShiftBits(bitDepth, d.s.extendedPrecision)
	}

	addResidual(plane, stride, x, y, n, shift, coef, bitDepth)

	return nil
}

// addResidual is bdShift of 8.6.2 and the sum of 8.6.6, in one pass.
func addResidual[P pixel](plane []P, stride, x, y, n, shift int, coef []int32, bitDepth int) {
	if n >= 8 {
		if p, ok := any(plane).([]uint8); ok && bitDepth == 8 {
			if k := dsp.addResidual8; k != nil {
				k(p[y*stride+x:], stride, coef[:n*n], n, shift)

				return
			}
		} else if p, ok := any(plane).([]uint16); ok {
			if k := dsp.addResidual16; k != nil {
				k(p[y*stride+x:], stride, coef[:n*n], n, shift, int32(1)<<bitDepth-1)

				return
			}
		}
	}

	addResidualGo(plane, stride, x, y, n, shift, coef, bitDepth)
}

func addResidualGo[P pixel](plane []P, stride, x, y, n, shift int, coef []int32, bitDepth int) {
	maxV := int32(1)<<bitDepth - 1

	if shift <= 0 {
		for j := range n {
			row := plane[(y+j)*stride+x:][:n]

			for i, c := range coef[j*n : j*n+n] {
				row[i] = P(clip3(int32(row[i])+c, 0, maxV))
			}
		}

		return
	}

	rnd := int32(1) << (shift - 1)

	for j := range n {
		row := plane[(y+j)*stride+x:][:n]

		for i, c := range coef[j*n : j*n+n] {
			row[i] = P(clip3(int32(row[i])+((c+rnd)>>shift), 0, maxV))
		}
	}
}

func gatherRef[P pixel](d *ctuDecoder, plane []P, stride, x, y, n, cIdx int) {
	sw, sh := 1, 1
	if cIdx > 0 {
		sw, sh = d.s.subWidthC, d.s.subHeightC
	}

	w, h := d.pic.Width/sw, d.pic.Height/sh

	d.ref.n = n

	// Availability is uniform across a minimum transform block, so it is
	// derived once per block rather than once per reference sample.
	lastTb, lastOK := -1, false

	for i := range 4*n + 1 {
		var nx, ny int

		switch {
		case i < 2*n:
			nx, ny = x-1, y+2*n-1-i
		case i == 2*n:
			nx, ny = x-1, y-1
		default:
			nx, ny = x+i-2*n-1, y-1
		}

		ok := nx >= 0 && ny >= 0 && nx < w && ny < h

		if ok {
			tb := d.tbIndex(nx*sw, ny*sh)

			if tb != lastTb {
				lastTb = tb
				lastOK = d.available(x*sw, y*sh, nx*sw, ny*sh)

				// 8.4.4.2.2: with constrained intra prediction, samples from
				// inter-coded neighbours count as unavailable.
				if lastOK && d.p.constrainedIntraPred &&
					!d.blk[d.blkIndex(nx*sw, ny*sh)].intra {
					lastOK = false
				}
			}

			ok = lastOK
		}

		d.avail[i] = ok
		if ok {
			d.ref.s[i] = int32(plane[ny*stride+nx])
		}
	}

	d.ref.substitute(d.avail[:4*n+1], d.pic.BitDepth)
}

// buildTileScan is 6.5.1, the raster to tile scan conversion and the tile
// each coding tree block belongs to.
func (d *ctuDecoder) buildTileScan() {
	w, h := int(d.s.picWidthInCtbs), int(d.s.picHeightInCtbs)

	colBd := make([]int, len(d.p.colWidthsInCtbs)+1)
	for i, v := range d.p.colWidthsInCtbs {
		colBd[i+1] = colBd[i] + int(v)
	}

	rowBd := make([]int, len(d.p.rowHeightsInCtbs)+1)
	for i, v := range d.p.rowHeightsInCtbs {
		rowBd[i+1] = rowBd[i] + int(v)
	}

	d.rsToTs = make([]int32, w*h)
	d.tsToRs = make([]int32, w*h)
	d.tileID = make([]int32, w*h)

	for rs := range w * h {
		x, y := rs%w, rs/w

		var tbX, tbY int

		for i := range len(colBd) - 1 {
			if x >= colBd[i] {
				tbX = i
			}
		}

		for i := range len(rowBd) - 1 {
			if y >= rowBd[i] {
				tbY = i
			}
		}

		ts := 0

		for i := range tbX {
			ts += int(d.p.rowHeightsInCtbs[tbY]) * int(d.p.colWidthsInCtbs[i])
		}

		for i := range tbY {
			ts += w * int(d.p.rowHeightsInCtbs[i])
		}

		ts += (y-rowBd[tbY])*int(d.p.colWidthsInCtbs[tbX]) + x - colBd[tbX]

		d.rsToTs[rs] = int32(ts)
		d.tsToRs[ts] = int32(rs)
		d.tileID[ts] = int32(tbY*len(d.p.colWidthsInCtbs) + tbX)
	}
}

// partMode is 7.3.8.5's part_mode, whose binarization depends on the
// prediction mode, the coding block size and whether asymmetric splits are on.
func (d *ctuDecoder) partMode(intra bool, log2Size int) int {
	if intra {
		if d.c.decodeBin(ctxPartMode) != 0 {
			return partMode2Nx2N
		}

		return partModeNxN
	}

	if d.c.decodeBin(ctxPartMode) != 0 {
		return partMode2Nx2N
	}

	minCb := log2Size == int(d.s.minCbLog2SizeY)

	// Table 9-34. At the smallest coding block size the third bin separates
	// Nx2N from NxN, and the asymmetric modes are not available at all.
	if d.c.decodeBin(ctxPartMode+1) != 0 {
		if minCb || !d.s.ampEnabled {
			return partMode2NxN
		}

		if d.c.decodeBin(ctxPartMode+3) != 0 {
			return partMode2NxN
		}

		if d.c.decodeBypass() != 0 {
			return partMode2NxnD
		}

		return partMode2NxnU
	}

	if minCb {
		if log2Size == 3 {
			return partModeNx2N
		}

		if d.c.decodeBin(ctxPartMode+2) != 0 {
			return partModeNx2N
		}

		return partModeNxN
	}

	if !d.s.ampEnabled {
		return partModeNx2N
	}

	if d.c.decodeBin(ctxPartMode+3) != 0 {
		return partModeNx2N
	}

	if d.c.decodeBypass() != 0 {
		return partModenRx2N
	}

	return partModenLx2N
}

var partShape = [8][4][4]int{
	partMode2Nx2N: {{0, 0, 4, 4}},
	partMode2NxN:  {{0, 0, 4, 2}, {0, 2, 4, 2}},
	partModeNx2N:  {{0, 0, 2, 4}, {2, 0, 2, 4}},
	partModeNxN:   {{0, 0, 2, 2}, {2, 0, 2, 2}, {0, 2, 2, 2}, {2, 2, 2, 2}},
	partMode2NxnU: {{0, 0, 4, 1}, {0, 1, 4, 3}},
	partMode2NxnD: {{0, 0, 4, 3}, {0, 3, 4, 1}},
	partModenLx2N: {{0, 0, 1, 4}, {1, 0, 3, 4}},
	partModenRx2N: {{0, 0, 3, 4}, {3, 0, 1, 4}},
}

var partCount = [8]int{1, 2, 2, 4, 2, 2, 2, 2}

func (d *ctuDecoder) interCU(x, y, log2Size, depth, partMode int) error {
	size := 1 << log2Size
	q := size / 4

	for i := range partCount[partMode] {
		s := partShape[partMode][i]

		if err := d.predictionUnit(x+s[0]*q, y+s[1]*q, s[2]*q, s[3]*q,
			i, partMode, depth, size, x, y, false); err != nil {
			return err
		}
	}

	// The bin is rqt_root_cbf: one means residual data follows. It is
	// inferred to one for a merged 2Nx2N unit.
	if !(partMode == partMode2Nx2N && d.lastMerge) {
		if d.c.decodeBin(ctxNoResidualDataFlag) == 0 {
			return nil
		}
	}

	var modes [4]int

	return d.transformTree(x, y, x, y, log2Size, 0, 0, modes, partMode, [2]bool{}, [2]bool{})
}

// scalingFactor picks the matrix of 8.6.3, which is flat when scaling lists
// are off and when a large block skips the transform.
func (d *ctuDecoder) scalingFactor(log2Size, cIdx int, skip bool) []uint8 {
	if !d.s.scalingListEnabled || (skip && log2Size > 2) {
		return nil
	}

	sizeID := log2Size - 2

	matrixID := cIdx
	if !d.curIntra {
		matrixID += 3
	}

	if sizeID == 3 && cIdx > 0 {
		matrixID = 0
		if !d.curIntra {
			matrixID = 3
		}
	}

	return d.scaling[sizeID][matrixID]
}

func readPCM[P pixel](g *getBits, plane []P, stride, x, y, w, h, depth, bitDepth int) {
	shift := bitDepth - depth

	for j := range h {
		row := (y + j) * stride
		for i := range w {
			plane[row+x+i] = P(g.bits(depth) << shift)
		}
	}
}

// pcmSample is 7.3.8.7 and 8.4.4.1: the samples are read from the bitstream at
// the byte boundary the arithmetic decoder left off, which then restarts.
func (d *ctuDecoder) pcmSample(x, y, log2Size int) error {
	off := d.c.pcmOffset()
	if off < 0 || off >= len(d.c.data) {
		return ErrInvalid
	}

	size := 1 << log2Size

	fill(d, d.intraMode, x, y, size, uint8(intraDC))
	d.markPU(x, y, size, size, true)

	if d.s.pcmLoopFilterDisabled {
		fill(d, d.noFilter, x, y, size, true)
	}

	var g getBits

	g.init(d.c.data[off:])

	depth := int(d.s.pcmBitDepthLuma)

	if d.pic.BitDepth > 8 {
		plane, stride := d.pic.plane16(0)
		readPCM(&g, plane, stride, x, y, size, size, depth, d.pic.BitDepth)
	} else {
		plane, stride := d.pic.plane8(0)
		readPCM(&g, plane, stride, x, y, size, size, depth, d.pic.BitDepth)
	}

	if d.s.chromaArrayType() != 0 {
		sw, sh := d.s.subWidthC, d.s.subHeightC
		depth = int(d.s.pcmBitDepthChroma)

		for cIdx := 1; cIdx <= 2; cIdx++ {
			if d.pic.BitDepth > 8 {
				plane, stride := d.pic.plane16(cIdx)
				readPCM(&g, plane, stride, x/sw, y/sh, size/sw, size/sh, depth, d.pic.BitDepth)
			} else {
				plane, stride := d.pic.plane8(cIdx)
				readPCM(&g, plane, stride, x/sw, y/sh, size/sw, size/sh, depth, d.pic.BitDepth)
			}
		}
	}

	if g.err {
		return ErrInvalid
	}

	return d.c.init(d.c.data, off+(g.pos()+7)/8)
}
