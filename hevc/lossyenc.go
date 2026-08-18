package hevc

import "math"

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
		deblockingDisabled: true, ctbLog2: 6, maxTrHierIntra: 2,
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

	// modes and depth are per 16x16 block, for 8.4.2 and 9.3.4.2.2. coded is
	// which 4x4 of each plane has been reconstructed, luma then chroma, which
	// is what the reference samples of 8.4.4.2.2 may be read from.
	modes []int
	depth []uint8
	coded [2][]uint8

	bits  putBits
	cabac cabacWriter
	s     sps
	p     pps

	// hint is the last slice's length, so the next one is allocated once
	// rather than grown into.
	hint int

	scratch lossyBlockScratch
	before  cuState
	kept    cuState
	tuBase  cuState
	tuKept  cuState
	tu      cuTransform
}

// cuTransform holds both ways of coding a 32x32 coding unit's transform tree:
// one unit of its own size, or four 16x16 ones.
type cuTransform struct {
	split        bool
	y32          [32 * 32]int32
	cb32, cr32   [16 * 16]int32
	y            [4][16 * 16]int32
	cb, cr       [4][8 * 8]int32
	cbfCb, cbfCr bool
}

// flat reports whether the unit took one transform and left no residual in it.
func (t *cuTransform) flat() bool {
	return !t.split && !hasCoefficients(t.y32[:]) &&
		!hasCoefficients(t.cb32[:]) && !hasCoefficients(t.cr32[:])
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
	codedY     [64]uint8
	codedC     [16]uint8
}

// lossyBlock names one transform block and how it is coded.
type lossyBlock struct {
	cIdx    int
	x, y, n int
	mode    int
	dst     bool
}

// lossyBlockScratch is the working memory one block needs. No two blocks are
// ever in flight at once, and the largest is 32x32.
type lossyBlockScratch struct {
	pred      [32 * 32]uint8
	avail     [4*32 + 1]bool
	residual  [32 * 32]int32
	coef      [32 * 32]int32
	num       [32 * 32]int64
	cost      [32 * 32]int64
	costZero  [32 * 32]int64
	costSig   [32 * 32]int64
	reconCoef [32 * 32]int32
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

	out := e.cabac.bytes()
	e.hint = len(out) + len(out)/8

	return out, nil
}

func (e *intraEncoder) reset(y, cb, cr []uint8, width, height, qp int) {
	e.width, e.height = width, height
	e.qp, e.qpC = qp, int(chromaQP(int32(qp), 1))
	e.lambda = lossyLambda(qp)
	e.src = [3][]uint8{y, cb, cr}
	e.s = sps{chromaFormatIDC: 1}
	e.p = pps{}

	e.recon[0] = regrow(e.recon[0], len(y))
	e.recon[1] = regrow(e.recon[1], len(cb))
	e.recon[2] = regrow(e.recon[2], len(cr))
	e.modes = regrow(e.modes, width/16*height/16)
	e.depth = regrow(e.depth, len(e.modes))
	e.coded[0] = regrow(e.coded[0], width/4*height/4)
	e.coded[1] = regrow(e.coded[1], width/8*height/8)

	e.bits = putBits{data: make([]byte, 0, e.hint)}
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

	if e.tu.flat() {
		return nil
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
	saveRect(s.codedY[:], e.coded[0], e.width/4, x0/4, y0/4, 8, 8)
	saveRect(s.codedC[:], e.coded[1], e.width/8, x0/8, y0/8, 4, 4)
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
	loadRect(e.coded[0], s.codedY[:], e.width/4, x0/4, y0/4, 8, 8)
	loadRect(e.coded[1], s.codedC[:], e.width/8, x0/8, y0/8, 4, 4)
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
				if err := e.codedResidual(&e.cabac, tus[i].y[j][:], 2, 0, mode, 2); err != nil {
					return err
				}
			}
		} else if err := e.codedResidual(&e.cabac, tus[i].y8[:], 3, 0, mode, 1); err != nil {
			return err
		}

		if tus[i].cbfCb {
			if err := e.residual(&e.cabac, tus[i].cb[:], 2, 1, mode); err != nil {
				return err
			}
		}

		if tus[i].cbfCr {
			if err := e.residual(&e.cabac, tus[i].cr[:], 2, 2, mode); err != nil {
				return err
			}
		}
	}

	idx := y0/16*blocksWide + x0/16
	e.modes[idx] = mode
	e.depth[idx] = uint8(d)

	return nil
}

// cu32 codes a 32x32 coding unit, choosing between one transform unit of its
// own size and four 16x16 ones. A negative mode is searched for.
func (e *intraEncoder) cu32(x0, y0, d, mode int) error {
	blocksWide := e.width / 16
	cand := lossyMPM(e.modes, blocksWide, x0/16, y0/16, 4)

	if mode < 0 {
		mode = e.lumaMode(x0, y0, cand)
	}

	t := &e.tu
	base := len(e.bits.data)

	e.save(&e.tuBase, x0, y0, base)

	copy(t.y32[:], e.codeBlock(lossyBlock{x: x0, y: y0, n: 32, mode: mode}, true))
	copy(t.cb32[:], e.codeBlock(lossyBlock{cIdx: 1, x: x0 / 2, y: y0 / 2, n: 16, mode: mode}, true))
	copy(t.cr32[:], e.codeBlock(lossyBlock{cIdx: 2, x: x0 / 2, y: y0 / 2, n: 16, mode: mode}, true))

	t.split = false
	whole := e.rdCost(e.cuDistortion(x0, y0, 32), e.tu32Rate(mode))

	e.save(&e.tuKept, x0, y0, base)
	e.load(&e.tuBase, x0, y0, base)

	for i := range 4 {
		x, y := x0+i&1*16, y0+i>>1*16
		copy(t.y[i][:], e.codeBlock(lossyBlock{x: x, y: y, n: 16, mode: mode}, true))
		copy(t.cb[i][:], e.codeBlock(lossyBlock{cIdx: 1, x: x / 2, y: y / 2, n: 8, mode: mode}, true))
		copy(t.cr[i][:], e.codeBlock(lossyBlock{cIdx: 2, x: x / 2, y: y / 2, n: 8, mode: mode}, true))
	}

	t.split = true
	if whole <= e.rdCost(e.cuDistortion(x0, y0, 32), e.tu32Rate(mode)) {
		t.split = false

		e.load(&e.tuKept, x0, y0, base)
	}

	lossyIntraLumaMode(&e.cabac, mode, cand)
	e.cabac.encodeBin(ctxIntraChromaPredMode, 0)

	if err := e.tu32Tree(&e.cabac, mode); err != nil {
		return err
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

// tu32Rate is what the chosen transform tree costs, coded into a writer that
// only counts.
func (e *intraEncoder) tu32Rate(mode int) int64 {
	w := e.cabac.counter()
	_ = e.tu32Tree(&w, mode)

	return w.rate
}

// tu32Tree is the transform_tree of 7.3.8.8 for a 32x32 coding unit.
func (e *intraEncoder) tu32Tree(w *cabacWriter, mode int) error {
	t := &e.tu

	t.cbfCb, t.cbfCr = hasCoefficients(t.cb32[:]), hasCoefficients(t.cr32[:])
	if t.split {
		t.cbfCb, t.cbfCr = false, false

		for i := range 4 {
			t.cbfCb = t.cbfCb || hasCoefficients(t.cb[i][:])
			t.cbfCr = t.cbfCr || hasCoefficients(t.cr[i][:])
		}
	}

	w.encodeBin(ctxSplitTransformFlag, boolToBit(t.split))
	w.encodeBin(ctxCBFCBCR, boolToBit(t.cbfCb))
	w.encodeBin(ctxCBFCBCR, boolToBit(t.cbfCr))

	if !t.split {
		if err := e.codedResidual(w, t.y32[:], 5, 0, mode, 0); err != nil {
			return err
		}

		if t.cbfCb {
			if err := e.residual(w, t.cb32[:], 4, 1, mode); err != nil {
				return err
			}
		}

		if t.cbfCr {
			return e.residual(w, t.cr32[:], 4, 2, mode)
		}

		return nil
	}

	for i := range 4 {
		cbfCb, cbfCr := hasCoefficients(t.cb[i][:]), hasCoefficients(t.cr[i][:])
		w.encodeBin(ctxSplitTransformFlag+1, 0)

		if t.cbfCb {
			w.encodeBin(ctxCBFCBCR+1, boolToBit(cbfCb))
		}

		if t.cbfCr {
			w.encodeBin(ctxCBFCBCR+1, boolToBit(cbfCr))
		}

		if err := e.codedResidual(w, t.y[i][:], 4, 0, mode, 1); err != nil {
			return err
		}

		if cbfCb {
			if err := e.residual(w, t.cb[i][:], 3, 1, mode); err != nil {
				return err
			}
		}

		if cbfCr {
			if err := e.residual(w, t.cr[i][:], 3, 2, mode); err != nil {
				return err
			}
		}
	}

	return nil
}

// codedResidual writes cbf_luma and the levels behind it. Table 9-49 gives the
// flag its own context at the root of a transform tree.
func (e *intraEncoder) codedResidual(w *cabacWriter, coef []int32, log2Size, cIdx, mode, trafoDepth int) error {
	cbf := hasCoefficients(coef)
	w.encodeBin(ctxCBFLuma+boolToInt(trafoDepth == 0), boolToBit(cbf))

	if !cbf {
		return nil
	}

	return e.residual(w, coef, log2Size, cIdx, mode)
}

func (e *intraEncoder) residual(w *cabacWriter, coef []int32, log2Size, cIdx, mode int) error {
	return encodeResidual(w, &e.s, &e.p, coef,
		residualBlock{log2Size: log2Size, cIdx: cIdx, predModeIntra: mode, intra: true})
}

// tu8 codes one 8x8 quadrant of a 16x16 coding unit, choosing between one 8x8
// transform and four 4x4 ones on their coded cost.
func (e *intraEncoder) tu8(x, y, mode int) lossyTU8Plan {
	w := e.width

	var before, kept tu8State

	e.saveTU8(&before, x, y)

	quarters := [4]lossyBlock{}
	for j := range 4 {
		quarters[j] = lossyBlock{x: x + j&1*4, y: y + j>>1*4, n: 4, mode: mode, dst: true}
	}

	whole := lossyBlock{x: x, y: y, n: 8, mode: mode}

	split := lossyTU8Plan{split: true}

	for j := range 4 {
		copy(split.y[j][:], e.codeBlock(quarters[j], true))
	}

	splitCost := e.rdCost(e.distortion(0, x, y, 8, e.recon[0][y*w+x:], w),
		e.tu8Rate(&split, mode))

	e.saveTU8(&kept, x, y)
	e.loadTU8(&before, x, y)

	var unsplit lossyTU8Plan

	copy(unsplit.y8[:], e.codeBlock(whole, true))

	chosen := unsplit
	if splitCost < e.rdCost(e.distortion(0, x, y, 8, e.recon[0][y*w+x:], w),
		e.tu8Rate(&unsplit, mode)) {
		chosen = split

		e.loadTU8(&kept, x, y)
	}

	copy(chosen.cb[:], e.codeBlock(lossyBlock{cIdx: 1, x: x / 2, y: y / 2, n: 4, mode: mode}, true))
	copy(chosen.cr[:], e.codeBlock(lossyBlock{cIdx: 2, x: x / 2, y: y / 2, n: 4, mode: mode}, true))
	chosen.cbfCb = hasCoefficients(chosen.cb[:])
	chosen.cbfCr = hasCoefficients(chosen.cr[:])

	return chosen
}

// tu8State is the reconstruction one 8x8 transform unit leaves behind, so the
// arm that wins does not have to be coded a second time.
type tu8State struct {
	y     [8 * 8]uint8
	coded [4]uint8
}

func (e *intraEncoder) saveTU8(s *tu8State, x, y int) {
	saveRect(s.y[:], e.recon[0], e.width, x, y, 8, 8)
	saveRect(s.coded[:], e.coded[0], e.width/4, x/4, y/4, 2, 2)
}

func (e *intraEncoder) loadTU8(s *tu8State, x, y int) {
	loadRect(e.recon[0], s.y[:], e.width, x, y, 8, 8)
	loadRect(e.coded[0], s.coded[:], e.width/4, x/4, y/4, 2, 2)
}

func (e *intraEncoder) lumaMode(x, y int, cand [3]int) int {
	b := lossyBlock{x: x, y: y, n: 16}
	e.prepareRef(b)

	var (
		short [modeShortlist]int
		score [modeShortlist]int64
	)

	for i := range short {
		short[i], score[i] = intraPlanar, 1<<62
	}

	pred := e.scratch.pred[:16*16]

	for mode := intraPlanar; mode <= 34; mode++ {
		e.scratch.ref.copyFrom(&e.scratch.base)
		filterRef(&e.scratch.ref, mode, 0, 8, &e.s)
		intraPredict(pred, 0, 16, &e.scratch.ref, mode, 0, 8)

		s := e.satd(x, y, pred, 16, score[modeShortlist-1])

		for i := range short {
			if s >= score[i] {
				continue
			}

			copy(short[i+1:], short[i:len(short)-1])
			copy(score[i+1:], score[i:len(score)-1])
			short[i], score[i] = mode, s

			break
		}
	}

	bestMode, bestCost := short[0], int64(-1)

	for _, mode := range short {
		b.mode = mode
		coef, trial := e.blockData(b, false)

		cost := e.rdCost(e.distortion(0, x, y, 16, trial, 16), e.modeRate(cand, mode, coef))
		if bestCost < 0 || cost < bestCost {
			bestMode, bestCost = mode, cost
		}
	}

	return bestMode
}

// satd is the Hadamard transformed absolute difference between the source and a
// prediction, over 8x8 at a time. It stands in for what the residual will cost
// far better than the plain difference does, and for a fraction of coding it.
// The sum only grows, so one that has reached limit is returned short.
func (e *intraEncoder) satd(x, y int, pred []uint8, n int, limit int64) int64 {
	src := e.src[0]
	stride := e.width

	var sum int64

	for by := 0; by < n; by += 8 {
		for bx := 0; bx < n; bx += 8 {
			if sum >= limit {
				return sum
			}

			s := src[(y+by)*stride+x+bx:]
			p := pred[by*n+bx:]

			if k := satd16x8Asm; k != nil && bx+16 <= n {
				sum += k(s, stride, p, n)
				bx += 8

				continue
			}

			sum += satd8x8Go(s, stride, p, n)
		}
	}

	return sum
}

// satd8x8Go is one 8x8 of satd, the rows transformed and then the columns.
func satd8x8Go(src []uint8, srcStride int, pred []uint8, predStride int) int64 {
	var d [64]int32

	for j := range 8 {
		row := src[j*srcStride:]
		p := pred[j*predStride:]

		for i := range 8 {
			d[j*8+i] = int32(row[i]) - int32(p[i])
		}
	}

	for j := range 8 {
		hadamard8((*[8]int32)(d[j*8 : j*8+8]))
	}

	var sum int64

	var col [8]int32

	for i := range 8 {
		for j := range 8 {
			col[j] = d[j*8+i]
		}

		hadamard8(&col)

		for j := range 8 {
			sum += int64(absLevel(col[j]))
		}
	}

	return sum
}

func hadamard8(v *[8]int32) {
	var t [8]int32

	for i := range 4 {
		t[i] = v[i] + v[i+4]
		t[i+4] = v[i] - v[i+4]
	}

	var u [8]int32

	for _, o := range [2]int{0, 4} {
		u[o] = t[o] + t[o+2]
		u[o+1] = t[o+1] + t[o+3]
		u[o+2] = t[o] - t[o+2]
		u[o+3] = t[o+1] - t[o+3]
	}

	for _, o := range [4]int{0, 2, 4, 6} {
		v[o] = u[o] + u[o+1]
		v[o+1] = u[o] - u[o+1]
	}
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

// modeShortlist is how many of the 35 intra modes are coded in full. Six and
// eight measure the same; four is worse on a large picture.
const modeShortlist = 6

// lambdaShift is the fraction lossyLambda carries.
const lambdaShift = 8

// rdCost weighs squared error against bits, both at rateShift.
func (e *intraEncoder) rdCost(dist, rate int64) int64 {
	return dist<<(rateShift+lambdaShift) + e.lambda*rate
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

	if k := sse8Asm; k != nil {
		return k(src[y*stride+x:], stride, block, blockStride, n)
	}

	return sse8Go(src[y*stride+x:], stride, block, blockStride, n)
}

// sse8Go is the squared error of an n by n block.
func sse8Go(src []uint8, srcStride int, block []uint8, blockStride, n int) int64 {
	var dist int64

	for j := range n {
		for i := range n {
			d := int64(src[j*srcStride+i]) - int64(block[j*blockStride+i])
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

	e.markCoded(b.cIdx, b.x, b.y, b.n)

	return coef
}

// markCoded records that a block of the plane has been reconstructed, at the
// 4x4 granularity the reference samples are looked up in.
func (e *intraEncoder) markCoded(cIdx, x, y, n int) {
	coded := e.coded[min(cIdx, 1)]
	blocksWide := e.stride(cIdx) / 4

	for j := range n / 4 {
		for i := range n / 4 {
			coded[(y/4+j)*blocksWide+x/4+i] = 1
		}
	}
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

	if rdoq {
		e.rdoq(coef, reconCoef, n, qp, b.cIdx, b.mode)
	} else {
		quantize(coef, reconCoef, n, qp, 8)
	}

	if !hasCoefficients(coef) {
		return coef, pred
	}

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
	coded := e.coded[min(b.cIdx, 1)]
	blocksWide := stride / 4
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
			coded[ny/4*blocksWide+nx/4] == 0 {
			continue
		}

		e.scratch.base.s[i] = int32(recon[ny*stride+nx])
		avail[i] = true
	}

	e.scratch.base.substitute(avail, 8)
}

// lambdaScale is the constant in front of the quantiser's own curve, at Q8.
// A sweep from a quarter of it to one and a half moved the rate by less than
// the measurement noise, so it is the usual 0.57.
const lambdaScale = 146

// lossyLambda weights bits against squared error at Q8, doubling every three
// QP the way the quantiser's step does.
func lossyLambda(qp int) int64 {
	return max(1, int64(math.Round(float64(lambdaScale)*math.Exp2(float64(qp-12)/3))))
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

func boolToInt(v bool) int {
	if v {
		return 1
	}

	return 0
}

func hasCoefficients(coef []int32) bool {
	for _, v := range coef {
		if v != 0 {
			return true
		}
	}

	return false
}
