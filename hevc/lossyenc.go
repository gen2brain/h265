package hevc

func encodeIntraLossyQP(y, cb, cr []uint8, width, height, qp int) ([]NALUnit, error) {
	if !validFrame(width, height, len(y), len(cb), len(cr)) || qp < 0 || qp > 51 {
		return nil, ErrInvalid
	}

	cw, ch := codedSize(width), codedSize(height)
	py, _ := padPlane(nil, y, width, width, height, cw, ch)
	pcb, _ := padPlane(nil, cb, width/2, width/2, height/2, cw/2, ch/2)
	pcr, _ := padPlane(nil, cr, width/2, width/2, height/2, cw/2, ch/2)

	var e intraEncoder

	rbsp, err := e.slice(py, pcb, pcr, cw, ch, qp)
	if err != nil {
		return nil, err
	}

	return lossyNALs(width, height, rbsp), nil
}

// codedSize rounds a picture dimension up to the minimum coding block size,
// which 7.4.3.2 requires the coded picture to be a multiple of.
func codedSize(n int) int {
	return (n + 15) &^ 15
}

func validFrame(width, height, ny, ncb, ncr int) bool {
	return width > 0 && height > 0 && width&1 == 0 && height&1 == 0 &&
		ny == width*height && ncb == width*height/4 && ncr == width*height/4
}

func lossyNALs(width, height int, rbsp []byte) []NALUnit {
	cw, ch := codedSize(width), codedSize(height)
	h := encoderHeaders{
		width: cw, height: ch, cropRight: cw - width, cropBottom: ch - height,
		levelIDC:           pcmLevelIDC(cw * ch),
		deblockingDisabled: true, signDataHidingEnabled: true, ctbLog2: 6, maxTrHierIntra: 2,
	}

	return append(h.parameterSets(), NALUnit{Type: NALIdrNLP, RBSP: rbsp})
}

// intraEncoder codes one 8 bit 4:2:0 picture as a single intra slice of 64x64
// coding tree blocks. A picture allocates only the bitstream it returns.
type intraEncoder struct {
	width, height int
	qp, qpC       int
	lambda        int64

	src   [3][]uint8
	recon [3][]uint8

	// modes and depth are per 16x16 block, for 8.4.2 and 9.3.4.2.2. depth is
	// also the availability map, being non-zero exactly where a block is coded.
	modes  []int
	depth  []uint8
	coded8 []uint8
	coded4 []uint8

	bits  putBits
	cabac cabacWriter
	s     sps
	p     pps

	scratch lossyBlockScratch
	before  cuState
	kept    cuState
}

// cuState is everything coding one 32x32 block changes, so that an arm of the
// size decision can be taken back or put back.
type cuState struct {
	cabac      cabacWriter
	bits       []byte
	cur, nbits uint8
	y          [32 * 32]uint8
	cb, cr     [16 * 16]uint8
	modes      [4]int
	depth      [4]uint8
	coded8     [16]uint8
	coded4     [64]uint8
}

// lossyBlock names one transform block and how it is coded.
type lossyBlock struct {
	cIdx    int
	x, y, n int
	mode    int
	coded   []uint8
	dst     bool
}

// lossyBlockScratch is the working memory one block needs. No two blocks are
// ever in flight at once, and the largest is 16x16.
type lossyBlockScratch struct {
	pred      [16 * 16]uint8
	avail     [4*16 + 1]bool
	residual  [16 * 16]int32
	coef      [16 * 16]int32
	reconCoef [16 * 16]int32
	base, ref refSamples
	transform transformScratch
}

type lossyTU8Plan struct {
	split        bool
	y            [4][4 * 4]int32
	y8           [8 * 8]int32
	cb, cr       [4 * 4]int32
	cbfCb, cbfCr bool
}

// slice codes the whole picture. The planes are the coded picture, so a caller
// with one that does not fill the coding grid pads it first.
func (e *intraEncoder) slice(y, cb, cr []uint8, width, height, qp int) ([]byte, error) {
	e.reset(y, cb, cr, width, height, qp)

	for y0 := 0; y0 < height; y0 += 64 {
		for x0 := 0; x0 < width; x0 += 64 {
			if err := e.tree(x0, y0, 6, 0); err != nil {
				return nil, err
			}

			e.cabac.encodeTerminate(boolToBit(x0+64 >= width && y0+64 >= height))
		}
	}

	return e.cabac.bytes(), nil
}

func (e *intraEncoder) reset(y, cb, cr []uint8, width, height, qp int) {
	e.width, e.height = width, height
	e.qp, e.qpC = qp, int(chromaQP(int32(qp), 1))
	e.lambda = lossyLambda(qp)
	e.src = [3][]uint8{y, cb, cr}
	e.s = sps{chromaFormatIDC: 1}
	e.p = pps{signDataHidingEnabled: true}

	e.recon[0] = regrow(e.recon[0], len(y))
	e.recon[1] = regrow(e.recon[1], len(cb))
	e.recon[2] = regrow(e.recon[2], len(cr))
	e.modes = regrow(e.modes, width/16*height/16)
	e.depth = regrow(e.depth, len(e.modes))
	e.coded8 = regrow(e.coded8, width/8*height/8)
	e.coded4 = regrow(e.coded4, width/4*height/4)

	e.bits = putBits{}
	e.bits.bit(1)
	e.bits.bit(0)
	e.bits.ue(0)
	e.bits.ue(uint32(sliceI))
	e.bits.se(int32(qp - 26))
	e.bits.rbspTrailingBits()
	e.cabac.init(&e.bits, int32(qp), sliceI, false)
}

func regrow[T any](s []T, n int) []T {
	if cap(s) < n {
		return make([]T, n)
	}

	s = s[:n]
	clear(s)

	return s
}

func (e *intraEncoder) stride(cIdx int) int {
	if cIdx == 0 {
		return e.width
	}

	return e.width / 2
}

func (e *intraEncoder) blockQP(cIdx int) int {
	if cIdx == 0 {
		return e.qp
	}

	return e.qpC
}

// tree is the coding quadtree of 7.3.8.4. A full 32x32 chooses its own size; a
// block at the picture edge splits without a flag.
func (e *intraEncoder) tree(x0, y0, log2Size, d int) error {
	if log2Size == 4 {
		return e.leaf(x0, y0, d, -1)
	}

	size := 1 << log2Size

	if x0+size <= e.width && y0+size <= e.height {
		if log2Size == 5 {
			return e.cuSize(x0, y0, d)
		}

		e.cabac.encodeBin(ctxSplitCodingUnitFlag+e.splitCtx(x0, y0, d), 1)
	}

	half := size / 2

	for i := range 4 {
		x, y := x0+i&1*half, y0+i>>1*half
		if x >= e.width || y >= e.height {
			continue
		}

		if err := e.tree(x, y, log2Size-1, d+1); err != nil {
			return err
		}
	}

	return nil
}

// cuSize codes a full 32x32 block both ways and keeps the cheaper: one coding
// unit of its own, or four 16x16 ones.
func (e *intraEncoder) cuSize(x0, y0, d int) error {
	base, start := len(e.bits.data), e.cabac.rate
	ctx := ctxSplitCodingUnitFlag + e.splitCtx(x0, y0, d)

	// Both arms pick their first mode by searching the same top-left 16x16
	// against the same reconstruction, so the search is done once for both.
	mode := e.lumaMode(x0, y0, lossyMPM(e.modes, e.width/16, x0/16, y0/16, 4))

	e.save(&e.before, x0, y0, base)

	e.cabac.encodeBin(ctx, 0)

	if err := e.cu32(x0, y0, d, mode); err != nil {
		return err
	}

	whole := e.rdCost(e.cuDistortion(x0, y0, 32), e.cabac.rate-start)

	e.save(&e.kept, x0, y0, base)
	e.load(&e.before, x0, y0, base)

	e.cabac.encodeBin(ctx, 1)

	// Both halves of the cost only grow with each quadrant, so a split that has
	// already lost is abandoned where it stands.
	split := int64(0)

	for i := range 4 {
		x, y := x0+i&1*16, y0+i>>1*16
		first := -1

		if i == 0 {
			first = mode
		}

		if err := e.leaf(x, y, d+1, first); err != nil {
			return err
		}

		split += e.cuDistortion(x, y, 16)
		if e.rdCost(split, e.cabac.rate-start) > whole {
			e.load(&e.kept, x0, y0, base)

			return nil
		}
	}

	if whole <= e.rdCost(split, e.cabac.rate-start) {
		e.load(&e.kept, x0, y0, base)
	}

	return nil
}

// cuDistortion is the squared error of a block, chroma included, which is what
// the two arms of the size decision differ over.
func (e *intraEncoder) cuDistortion(x0, y0, n int) int64 {
	cs := e.width / 2
	dist := e.distortion(0, x0, y0, n, e.recon[0][y0*e.width+x0:], e.width)

	for c := 1; c < 3; c++ {
		dist += e.distortion(c, x0/2, y0/2, n/2, e.recon[c][y0/2*cs+x0/2:], cs)
	}

	return dist
}

func (e *intraEncoder) save(s *cuState, x0, y0, base int) {
	s.cabac = e.cabac
	s.bits = append(s.bits[:0], e.bits.data[base:]...)
	s.cur, s.nbits = e.bits.cur, e.bits.nbits

	cs, bw := e.width/2, e.width/16
	saveRect(s.y[:], e.recon[0], e.width, x0, y0, 32, 32)
	saveRect(s.cb[:], e.recon[1], cs, x0/2, y0/2, 16, 16)
	saveRect(s.cr[:], e.recon[2], cs, x0/2, y0/2, 16, 16)
	saveRect(s.modes[:], e.modes, bw, x0/16, y0/16, 2, 2)
	saveRect(s.depth[:], e.depth, bw, x0/16, y0/16, 2, 2)
	saveRect(s.coded8[:], e.coded8, e.width/8, x0/8, y0/8, 4, 4)
	saveRect(s.coded4[:], e.coded4, e.width/4, x0/4, y0/4, 8, 8)
}

func (e *intraEncoder) load(s *cuState, x0, y0, base int) {
	e.cabac = s.cabac
	e.bits.data = append(e.bits.data[:base], s.bits...)
	e.bits.cur, e.bits.nbits = s.cur, s.nbits

	cs, bw := e.width/2, e.width/16
	loadRect(e.recon[0], s.y[:], e.width, x0, y0, 32, 32)
	loadRect(e.recon[1], s.cb[:], cs, x0/2, y0/2, 16, 16)
	loadRect(e.recon[2], s.cr[:], cs, x0/2, y0/2, 16, 16)
	loadRect(e.modes, s.modes[:], bw, x0/16, y0/16, 2, 2)
	loadRect(e.depth, s.depth[:], bw, x0/16, y0/16, 2, 2)
	loadRect(e.coded8, s.coded8[:], e.width/8, x0/8, y0/8, 4, 4)
	loadRect(e.coded4, s.coded4[:], e.width/4, x0/4, y0/4, 8, 8)
}

// saveRect lifts a w by h rectangle out of a picture-wide map, loadRect puts
// one back.
func saveRect[T any](dst, src []T, stride, x, y, w, h int) {
	for j := range h {
		copy(dst[j*w:(j+1)*w], src[(y+j)*stride+x:])
	}
}

func loadRect[T any](dst, src []T, stride, x, y, w, h int) {
	for j := range h {
		copy(dst[(y+j)*stride+x:][:w], src[j*w:])
	}
}

func (e *intraEncoder) splitCtx(x0, y0, d int) int {
	blocksWide := e.width / 16
	ctx := 0

	if x0 > 0 && int(e.depth[y0/16*blocksWide+(x0-1)/16]) > d {
		ctx++
	}

	if y0 > 0 && int(e.depth[(y0-1)/16*blocksWide+x0/16]) > d {
		ctx++
	}

	return ctx
}

// leaf codes a 16x16 coding unit, whose transform tree splits to 8x8 and then
// to 4x4 where that is cheaper. A negative mode is searched for.
func (e *intraEncoder) leaf(x0, y0, d, mode int) error {
	blocksWide := e.width / 16
	cand := lossyMPM(e.modes, blocksWide, x0/16, y0/16, 4)

	if mode < 0 {
		mode = e.lumaMode(x0, y0, cand)
	}

	var tus [4]lossyTU8Plan

	for i := range 4 {
		tus[i] = e.tu8(x0+i&1*8, y0+i>>1*8, mode)
	}

	rootCb, rootCr := false, false

	for i := range 4 {
		rootCb = rootCb || tus[i].cbfCb
		rootCr = rootCr || tus[i].cbfCr
	}

	e.cabac.encodeBin(ctxPartMode, 1)
	lossyIntraLumaMode(&e.cabac, mode, cand)
	e.cabac.encodeBin(ctxIntraChromaPredMode, 0)
	e.cabac.encodeBin(ctxSplitTransformFlag+1, 1)
	e.cabac.encodeBin(ctxCBFCBCR, boolToBit(rootCb))
	e.cabac.encodeBin(ctxCBFCBCR, boolToBit(rootCr))

	for i := range 4 {
		e.cabac.encodeBin(ctxSplitTransformFlag+2, boolToBit(tus[i].split))

		if rootCb {
			e.cabac.encodeBin(ctxCBFCBCR+1, boolToBit(tus[i].cbfCb))
		}

		if rootCr {
			e.cabac.encodeBin(ctxCBFCBCR+1, boolToBit(tus[i].cbfCr))
		}

		if tus[i].split {
			for j := range 4 {
				if err := e.codedResidual(tus[i].y[j][:], 2, 0, mode); err != nil {
					return err
				}
			}
		} else if err := e.codedResidual(tus[i].y8[:], 3, 0, mode); err != nil {
			return err
		}

		if tus[i].cbfCb {
			if err := e.residual(tus[i].cb[:], 2, 1, mode); err != nil {
				return err
			}
		}

		if tus[i].cbfCr {
			if err := e.residual(tus[i].cr[:], 2, 2, mode); err != nil {
				return err
			}
		}
	}

	idx := y0/16*blocksWide + x0/16
	e.modes[idx] = mode
	e.depth[idx] = uint8(d)

	return nil
}

// cu32 codes a 32x32 coding unit as four 16x16 transform units, the largest
// transform the sequence allows. A negative mode is searched for.
func (e *intraEncoder) cu32(x0, y0, d, mode int) error {
	blocksWide := e.width / 16
	cand := lossyMPM(e.modes, blocksWide, x0/16, y0/16, 4)

	if mode < 0 {
		mode = e.lumaMode(x0, y0, cand)
	}

	var (
		qY       [4][16 * 16]int32
		qCb, qCr [4][8 * 8]int32
	)

	for i := range 4 {
		x, y := x0+i&1*16, y0+i>>1*16
		copy(qY[i][:], e.codeBlock(lossyBlock{x: x, y: y, n: 16, mode: mode, coded: e.depth}, true))
		e.markCoded(x, y, 16)
		copy(qCb[i][:], e.codeBlock(lossyBlock{cIdx: 1, x: x / 2, y: y / 2, n: 8, mode: mode,
			coded: e.depth}, true))
		copy(qCr[i][:], e.codeBlock(lossyBlock{cIdx: 2, x: x / 2, y: y / 2, n: 8, mode: mode,
			coded: e.depth}, true))
	}

	cbfCb, cbfCr := false, false

	for i := range 4 {
		cbfCb = cbfCb || hasCoefficients(qCb[i][:])
		cbfCr = cbfCr || hasCoefficients(qCr[i][:])
	}

	lossyIntraLumaMode(&e.cabac, mode, cand)
	e.cabac.encodeBin(ctxIntraChromaPredMode, 0)
	e.cabac.encodeBin(ctxCBFCBCR, boolToBit(cbfCb))
	e.cabac.encodeBin(ctxCBFCBCR, boolToBit(cbfCr))

	for i := range 4 {
		cbfCbLeaf := hasCoefficients(qCb[i][:])
		cbfCrLeaf := hasCoefficients(qCr[i][:])
		e.cabac.encodeBin(ctxSplitTransformFlag+1, 0)

		if cbfCb {
			e.cabac.encodeBin(ctxCBFCBCR+1, boolToBit(cbfCbLeaf))
		}

		if cbfCr {
			e.cabac.encodeBin(ctxCBFCBCR+1, boolToBit(cbfCrLeaf))
		}

		if err := e.codedResidual(qY[i][:], 4, 0, mode); err != nil {
			return err
		}

		if cbfCbLeaf {
			if err := e.residual(qCb[i][:], 3, 1, mode); err != nil {
				return err
			}
		}

		if cbfCrLeaf {
			if err := e.residual(qCr[i][:], 3, 2, mode); err != nil {
				return err
			}
		}
	}

	for j := range 2 {
		for i := range 2 {
			idx := (y0/16+j)*blocksWide + x0/16 + i
			e.modes[idx] = mode
			e.depth[idx] = uint8(d)
		}
	}

	return nil
}

// codedResidual writes cbf_luma and the levels behind it.
func (e *intraEncoder) codedResidual(coef []int32, log2Size, cIdx, mode int) error {
	cbf := hasCoefficients(coef)
	e.cabac.encodeBin(ctxCBFLuma, boolToBit(cbf))

	if !cbf {
		return nil
	}

	return e.residual(coef, log2Size, cIdx, mode)
}

func (e *intraEncoder) residual(coef []int32, log2Size, cIdx, mode int) error {
	return encodeResidual(&e.cabac, &e.s, &e.p, coef,
		residualBlock{log2Size: log2Size, cIdx: cIdx, predModeIntra: mode, intra: true})
}

// tu8 codes one 8x8 quadrant of a 16x16 coding unit, choosing between one 8x8
// transform and four 4x4 ones on their coded cost.
func (e *intraEncoder) tu8(x, y, mode int) lossyTU8Plan {
	w := e.width
	idx8 := y/8*(w/8) + x/8

	var idx4 [4]int

	for j := range 2 {
		for k := range 2 {
			idx4[j*2+k] = (y/4+j)*(w/4) + x/4 + k
		}
	}

	var base [8 * 8]uint8

	for j := range 8 {
		copy(base[j*8:], e.recon[0][(y+j)*w+x:][:8])
	}

	base8 := e.coded8[idx8]

	var base4 [4]uint8

	for j, idx := range idx4 {
		base4[j] = e.coded4[idx]
	}

	restore := func() {
		for j := range 8 {
			copy(e.recon[0][(y+j)*w+x:][:8], base[j*8:])
		}

		e.coded8[idx8] = base8

		for j, idx := range idx4 {
			e.coded4[idx] = base4[j]
		}
	}

	quarters := [4]lossyBlock{}
	for j := range 4 {
		quarters[j] = lossyBlock{x: x + j&1*4, y: y + j>>1*4, n: 4, mode: mode,
			coded: e.coded4, dst: true}
	}

	whole := lossyBlock{x: x, y: y, n: 8, mode: mode, coded: e.coded8}

	split := lossyTU8Plan{split: true}

	for j := range 4 {
		copy(split.y[j][:], e.codeBlock(quarters[j], false))
	}

	splitDist := e.distortion(0, x, y, 8, e.recon[0][y*w+x:], w)

	restore()

	var unsplit lossyTU8Plan

	copy(unsplit.y8[:], e.codeBlock(whole, false))

	unsplitDist := e.distortion(0, x, y, 8, e.recon[0][y*w+x:], w)

	chosen := split
	if e.rdCost(unsplitDist, e.tu8Rate(&unsplit, mode)) <=
		e.rdCost(splitDist, e.tu8Rate(&split, mode)) {
		chosen = unsplit
	}

	restore()

	if chosen.split {
		for j := range 4 {
			copy(chosen.y[j][:], e.codeBlock(quarters[j], true))
		}
	} else {
		copy(chosen.y8[:], e.codeBlock(whole, true))
	}

	e.coded8[idx8] = 1

	for _, idx := range idx4 {
		e.coded4[idx] = 1
	}

	copy(chosen.cb[:], e.codeBlock(lossyBlock{cIdx: 1, x: x / 2, y: y / 2, n: 4, mode: mode,
		coded: e.coded8}, true))
	copy(chosen.cr[:], e.codeBlock(lossyBlock{cIdx: 2, x: x / 2, y: y / 2, n: 4, mode: mode,
		coded: e.coded8}, true))
	chosen.cbfCb = hasCoefficients(chosen.cb[:])
	chosen.cbfCr = hasCoefficients(chosen.cr[:])

	return chosen
}

func (e *intraEncoder) markCoded(x, y, size int) {
	for j := range size / 8 {
		for i := range size / 8 {
			e.coded8[(y/8+j)*(e.width/8)+x/8+i] = 1
		}
	}

	for j := range size / 4 {
		for i := range size / 4 {
			e.coded4[(y/4+j)*(e.width/4)+x/4+i] = 1
		}
	}
}

// lumaMode picks the intra mode of a 16x16 block: all 35 are coded for their
// distortion, and the four closest are costed again with their bits.
func (e *intraEncoder) lumaMode(x, y int, cand [3]int) int {
	b := lossyBlock{x: x, y: y, n: 16, coded: e.depth}
	e.prepareRef(b)

	bestModes := [4]int{intraPlanar, intraPlanar, intraPlanar, intraPlanar}
	bestDist := [4]int64{1<<63 - 1, 1<<63 - 1, 1<<63 - 1, 1<<63 - 1}

	for mode := intraPlanar; mode <= 34; mode++ {
		b.mode = mode
		_, trial := e.blockData(b, false)
		dist := e.distortion(0, x, y, 16, trial, 16)

		for i := range bestModes {
			if dist >= bestDist[i] {
				continue
			}

			copy(bestModes[i+1:], bestModes[i:len(bestModes)-1])
			copy(bestDist[i+1:], bestDist[i:len(bestDist)-1])
			bestModes[i], bestDist[i] = mode, dist

			break
		}
	}

	bestMode, bestCost := bestModes[0], int64(-1)

	for i, mode := range bestModes {
		b.mode = mode
		coef, _ := e.blockData(b, false)

		cost := e.rdCost(bestDist[i], e.modeRate(cand, mode, coef))
		if bestCost < 0 || cost < bestCost {
			bestMode, bestCost = mode, cost
		}
	}

	return bestMode
}

// modeRate is what one mode costs, at rateShift.
func (e *intraEncoder) modeRate(cand [3]int, mode int, coef []int32) int64 {
	w := e.cabac.counter()

	lossyIntraLumaMode(&w, mode, cand)

	cbf := hasCoefficients(coef)
	w.encodeBin(ctxCBFLuma, boolToBit(cbf))

	if cbf {
		_ = encodeResidual(&w, &e.s, &e.p, coef,
			residualBlock{log2Size: 4, predModeIntra: mode, intra: true})
	}

	return w.rate
}

func (e *intraEncoder) tu8Rate(plan *lossyTU8Plan, mode int) int64 {
	w := e.cabac.counter()

	w.encodeBin(ctxSplitTransformFlag+2, boolToBit(plan.split))

	if plan.split {
		for i := range plan.y {
			e.rateResidual(&w, plan.y[i][:], 2, mode)
		}
	} else {
		e.rateResidual(&w, plan.y8[:], 3, mode)
	}

	return w.rate
}

// rdCost weighs squared error against bits, both at rateShift.
func (e *intraEncoder) rdCost(dist, rate int64) int64 {
	return dist<<rateShift + e.lambda*rate
}

func (e *intraEncoder) rateResidual(w *cabacWriter, coef []int32, log2Size, mode int) {
	cbf := hasCoefficients(coef)
	w.encodeBin(ctxCBFLuma, boolToBit(cbf))

	if cbf {
		_ = encodeResidual(w, &e.s, &e.p, coef,
			residualBlock{log2Size: log2Size, predModeIntra: mode, intra: true})
	}
}

// distortion is the squared error between the source and block, which is a
// trial reconstruction or a window on the picture reconstruction.
func (e *intraEncoder) distortion(cIdx, x, y, n int, block []uint8, blockStride int) int64 {
	stride := e.stride(cIdx)
	src := e.src[cIdx]

	var dist int64

	for j := range n {
		for i := range n {
			d := int64(src[(y+j)*stride+x+i]) - int64(block[j*blockStride+i])
			dist += d * d
		}
	}

	return dist
}

// codeBlock codes one transform block and writes its reconstruction back.
func (e *intraEncoder) codeBlock(b lossyBlock, rdoq bool) []int32 {
	e.prepareRef(b)
	coef, block := e.blockData(b, rdoq)

	stride := e.stride(b.cIdx)
	for j := range b.n {
		copy(e.recon[b.cIdx][(b.y+j)*stride+b.x:], block[j*b.n:(j+1)*b.n])
	}

	b.coded[b.y/b.n*(stride/b.n)+b.x/b.n] = 1

	return coef
}

// blockData predicts, transforms, quantises and reconstructs one block against
// the reference samples prepareRef has already built.
func (e *intraEncoder) blockData(b lossyBlock, rdoq bool) ([]int32, []uint8) {
	n := b.n
	count := n * n
	stride := e.stride(b.cIdx)
	qp := e.blockQP(b.cIdx)

	pred := e.scratch.pred[:count]
	residual := e.scratch.residual[:count]
	coef := e.scratch.coef[:count]
	reconCoef := e.scratch.reconCoef[:count]

	e.scratch.ref.copyFrom(&e.scratch.base)
	filterRef(&e.scratch.ref, b.mode, b.cIdx, 8, &e.s)
	intraPredict(pred, 0, n, &e.scratch.ref, b.mode, b.cIdx, 8)

	src := e.src[b.cIdx]

	for j := range n {
		for i := range n {
			residual[j*n+i] = int32(src[(b.y+j)*stride+b.x+i]) - int32(pred[j*n+i])
		}
	}

	if b.dst {
		forwardTransformDST4(reconCoef, residual, 8)
	} else {
		forwardTransform(reconCoef, residual, n, 8)
	}

	quantize(coef, reconCoef, n, qp, 8)

	if rdoq {
		terminalRDOQ(reconCoef, coef, n, b.mode, b.cIdx, qp)
	}

	normalizeSignDataHiding(coef, n, b.mode, b.cIdx)

	copy(reconCoef, coef)
	dequant(reconCoef, nil, n, qp, 8, false)
	inverseTransform(reconCoef, n, b.dst, 8, false, &e.scratch.transform)
	addResidual(pred, n, 0, 0, n, residualShiftBits(8, false), reconCoef, 8)

	return coef, pred
}

// prepareRef builds the reference samples of 8.4.4.2.2, which do not depend on
// the prediction mode.
func (e *intraEncoder) prepareRef(b lossyBlock) {
	n, stride := b.n, e.stride(b.cIdx)
	recon := e.recon[b.cIdx]
	avail := e.scratch.avail[:4*n+1]
	clear(avail)

	e.scratch.base.n = n
	blocksWide := stride / n
	rows := len(recon) / stride

	for i := range 4*n + 1 {
		var nx, ny int

		switch {
		case i < 2*n:
			nx, ny = b.x-1, b.y+2*n-1-i
		case i == 2*n:
			nx, ny = b.x-1, b.y-1
		default:
			nx, ny = b.x+i-2*n-1, b.y-1
		}

		if nx < 0 || ny < 0 || nx >= stride || ny >= rows ||
			b.coded[ny/n*blocksWide+nx/n] == 0 {
			continue
		}

		e.scratch.base.s[i] = int32(recon[ny*stride+nx])
		avail[i] = true
	}

	e.scratch.base.substitute(avail, 8)
}

// lossyLambda weights bits against squared error, doubling every three QP.
func lossyLambda(qp int) int64 {
	shift := max(qp-12, 0) / 3

	return max(int64(1), int64(9<<shift)/16)
}

// terminalRDOQ drops a lone trailing coefficient whose error costs less than
// its bits.
func terminalRDOQ(raw, level []int32, n, mode, cIdx, qp int) {
	scanIdx := scanIndex(log2(n), cIdx, mode, true, 1)
	sbScan := scanOrder[log2(n)-2][scanIdx]
	coeffScan := scanOrder[2][scanIdx]
	lastSB, lastPos, prevSB := -1, -1, -1

	for i, sb := range sbScan {
		for k, pos := range coeffScan {
			index := ((int(sb.y)<<2)+int(pos.y))*n + (int(sb.x) << 2) + int(pos.x)
			if level[index] == 0 {
				continue
			}

			prevSB = lastSB
			lastSB, lastPos = i, k
		}
	}

	if lastSB < 0 || prevSB < 0 || lastSB != prevSB {
		return
	}

	last := coeffScan[lastPos]
	if last.x == 0 && last.y == 0 {
		return
	}

	lastSBPos := sbScan[lastSB]

	index := ((int(lastSBPos.y)<<2)+int(last.y))*n + (int(lastSBPos.x) << 2) + int(last.x)
	if absLevel(level[index]) != 1 {
		return
	}

	v := int64(raw[index])
	if distortion := (v*v + 511) >> 9; distortion <= lossyLambda(qp)*2 {
		level[index] = 0
	}
}

// normalizeSignDataHiding gives every sub-block the parity 7.4.9.11 infers the
// sign of its first coefficient from.
func normalizeSignDataHiding(coef []int32, n, mode, cIdx int) {
	scanIdx := scanIndex(log2(n), cIdx, mode, true, 1)
	sbScan := scanOrder[log2(n)-2][scanIdx]
	coeffScan := scanOrder[2][scanIdx]

	var off [numSbCoeff]int

	for k, pos := range coeffScan {
		off[k] = int(pos.y)*n + int(pos.x)
	}

	for _, sb := range sbScan {
		firstSig, lastSig := -1, -1
		base := (int(sb.y)*n + int(sb.x)) << 2

		var sumAbs int32

		for k := range numSbCoeff {
			level := coef[base+off[k]]
			if level == 0 {
				continue
			}

			if firstSig < 0 {
				firstSig = k
			}

			lastSig = k
			sumAbs += absLevel(level)
		}

		if lastSig-firstSig <= 3 {
			continue
		}

		index := base + off[firstSig]

		negative := coef[index] < 0
		if (sumAbs&1 != 0) == negative {
			continue
		}

		switch {
		case absLevel(coef[index]) > 1 && negative:
			coef[index]++
		case absLevel(coef[index]) > 1:
			coef[index]--
		case negative:
			coef[index]--
		default:
			coef[index]++
		}
	}
}

// lossyMPM is the candidate list of 8.4.2. A block on the top edge of its
// coding tree block has no candidate above it.
func lossyMPM(modes []int, blocksWide, x, y, ctbBlocks int) [3]int {
	candA, candB := intraDC, intraDC

	if x > 0 {
		candA = modes[y*blocksWide+x-1]
	}

	if y > 0 && y%ctbBlocks != 0 {
		candB = modes[(y-1)*blocksWide+x]
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

	return cand
}

func lossyIntraLumaMode(w *cabacWriter, mode int, cand [3]int) {
	for i, m := range cand {
		if mode != m {
			continue
		}

		w.encodeBin(ctxPrevIntraLumaPredFlag, 1)

		if i == 0 {
			w.encodeBypass(0)
		} else {
			w.encodeBypass(1)
			w.encodeBypass(uint32(i - 1))
		}

		return
	}

	w.encodeBin(ctxPrevIntraLumaPredFlag, 0)

	rem := mode

	for _, m := range cand {
		if mode > m {
			rem--
		}
	}

	w.encodeBypassBits(uint32(rem), 5)
}

func hasCoefficients(coef []int32) bool {
	for _, v := range coef {
		if v != 0 {
			return true
		}
	}

	return false
}
