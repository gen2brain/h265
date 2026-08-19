package hevc

import (
	"math"
	"slices"
)

// codedSize rounds a picture dimension up to the minimum coding block size,
// which 7.4.3.2 requires the coded picture to be a multiple of.
func codedSize(n int) int {
	return (n + 15) &^ 15
}

// nals is the parameter sets a slice coded by this encoder needs, and the
// slice behind them.
func (e *intraEncoder[P]) nals(width, height int, rbsp []byte) []NALUnit {
	cw, ch := codedSize(width), codedSize(height)
	h := encoderHeaders{
		width: cw, height: ch, cropRight: cw - width, cropBottom: ch - height,
		chromaFormat: e.s.chromaFormatIDC,
		subWidthC:    int(e.s.subWidthC), subHeightC: int(e.s.subHeightC),
		bitDepth: e.bitDepth,
		levelIDC: pcmLevelIDC(cw * ch),
		ctbLog2:  6, maxTrHierIntra: 2,
		wavefront: e.wavefront,
	}

	return append(h.parameterSets(), NALUnit{Type: NALIdrNLP, RBSP: rbsp})
}

// intraEncoder codes one picture as a single intra slice of 64x64 coding tree
// blocks, at any sampling of 6.2 and any sample size the transforms reach. A
// picture allocates only the bitstream it returns.
type intraEncoder[P pixel] struct {
	width, height int

	// qp is SliceQpY; qpY and qpC are the Qp' of 8.6.1 behind it, and
	// qpDeblockC the chroma QP 8.7.2 filters by, which takes no offset.
	qp, qpY, qpC int
	qpDeblockC   int
	bitDepth     int
	lambda       int64
	// lambdaBase is the same weight at eight bits, which is what rdoq weighs by.
	lambdaBase int64

	src   [3][]P
	recon [3][]P

	// shiftW and shiftH are SubWidthC and SubHeightC of 6.2 as the shifts they
	// only ever are, and strideC the chroma stride they leave.
	shiftW, shiftH int
	strideC        int

	// modes and depth are per 16x16 block, for 8.4.2 and 9.3.4.2.2. coded is
	// which 4x4 of each plane has been reconstructed, luma then chroma, which
	// is what the reference samples of 8.4.4.2.2 may be read from. edges is
	// where a transform block begins, which is what 8.7.2 filters.
	modes []int
	depth []uint8
	coded [2][]uint8
	edges []uint8

	bits  putBits
	cabac cabacWriter
	s     sps
	p     pps

	// hint is the last slice's length, so the next one is allocated once
	// rather than grown into.
	hint int

	// threads is what the rows may spread over, wavefront whether they may.
	threads   int
	wavefront bool

	scratch lossyBlockScratch[P]
	before  cuState[P]
	kept    cuState[P]
	tuBase  cuState[P]
	tuKept  cuState[P]
	tu      cuTransform
}

// cuTransform holds both ways of coding a 32x32 coding unit's transform tree:
// one unit of its own size, or four 16x16 ones.
type cuTransform struct {
	split bool
	y32   [32 * 32]int32
	y     [4][16 * 16]int32

	// c32 and c are the chroma levels, Cb then Cr, with the two blocks 4:2:2
	// stacks laid one after the other.
	c32 [2][32 * 32]int32
	c   [2][4][16 * 16]int32

	// whole and quad are the coded block flags, taken where the levels are made.
	whole  bool
	quad   [4]bool
	wholeC [2][2]bool
	quadC  [4][2][2]bool
}

// flat reports whether the unit took one transform and left no residual in it.
func (t *cuTransform) flat() bool {
	return !t.split && !t.whole && !anyCBF(t.wholeC)
}

// anyCBF reports whether either component of either stacked block is coded.
func anyCBF(f [2][2]bool) bool {
	return f[0][0] || f[0][1] || f[1][0] || f[1][1]
}

// cuState is everything coding one 32x32 block changes, so that an arm of the
// size decision can be taken back or put back.
type cuState[P pixel] struct {
	cabac      cabacWriter
	bits       []byte
	cur, nbits uint8
	y          [32 * 32]P
	cb, cr     [32 * 32]P
	modes      [4]int
	depth      [4]uint8
	codedY     [64]uint8
	codedC     [64]uint8
	edges      [64]uint8
}

// lossyBlock names one transform block and how it is coded.
type lossyBlock[P pixel] struct {
	cIdx    int
	x, y, n int
	mode    int
	dst     bool

	// pred is a prediction already made, which the mode search keeps from its sweep.
	pred []P
}

// lossyBlockScratch is the working memory one block needs. No two blocks are
// ever in flight at once, and the largest is 32x32.
type lossyBlockScratch[P pixel] struct {
	pred      [32 * 32]P
	modePred  [modeShortlist][16 * 16]P
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
	split bool
	y     [4][4 * 4]int32
	y8    [8 * 8]int32
	cbfY  [4]bool
	cbfY8 bool

	// c8 is the chroma of the whole 8x8 block and c that of its four 4x4 ones,
	// which only 4:4:4 splits chroma down to.
	c8    [2][8 * 8]int32
	c     [2][4][4 * 4]int32
	cbfC8 [2][2]bool
	cbfC  [4][2][2]bool
}

// slice codes the whole picture. The planes are the coded picture, so a caller
// with one that does not fill the coding grid pads it first.
func (e *intraEncoder[P]) slice(y, cb, cr []P, width, height, qp int) ([]byte, error) {
	e.reset(y, cb, cr, width, height, qp)

	rows, cols := ctbCount(height), ctbCount(width)
	e.wavefront = e.threads > 1 && rows > 1 && cols > 1

	if e.wavefront {
		subs, err := e.encodeWavefront(rows, cols, min(e.threads, rows))
		if err != nil {
			return nil, err
		}

		e.deblockRecon()

		out := e.sliceRBSP(subs)
		e.hint = len(out) + len(out)/8

		return out, nil
	}

	subs := make([][]byte, rows)

	var sync [nContexts]uint8

	for k := range rows {
		e.startRow(k, &sync)

		if err := e.ctbRow(k, rows, cols, &sync); err != nil {
			return nil, err
		}

		if e.wavefront {
			subs[k] = e.cabac.bytes()
		}
	}

	if !e.wavefront {
		subs = [][]byte{e.cabac.bytes()}
	}

	e.deblockRecon()

	out := e.sliceRBSP(subs)
	e.hint = len(out) + len(out)/8

	return out, nil
}

// sliceRBSP puts the segment header in front of the substreams. 7.4.7.1 counts
// the entry points in the slice data bytes a decoder sees.
func (e *intraEncoder[P]) sliceRBSP(subs [][]byte) []byte {
	var w putBits

	w.bit(1)
	w.bit(0)
	w.ue(0)
	w.ue(uint32(sliceI))
	w.se(int32(e.qp - 26))

	// 7.3.6.1 carries slice_loop_filter_across_slices_enabled_flag only while
	// a loop filter is on.
	w.bit(1)

	if e.wavefront {
		sizes := escapedSizes(subs)

		w.ue(uint32(len(sizes)))

		if len(sizes) > 0 {
			bits := 1
			for max := slices.Max(sizes); max>>bits != 0; bits++ {
			}

			w.ue(uint32(bits - 1))

			for _, n := range sizes {
				w.bits(uint64(n-1), bits)
			}
		}
	}

	w.rbspTrailingBits()

	out := w.bytes()
	for _, s := range subs {
		out = append(out, s...)
	}

	return out
}

// escapedSizes is the length of every substream but the last, in the bytes the
// emulation prevention of 7.3.1.1 leaves. A header ends on a set bit, so the
// run of zeros never carries into the data.
func escapedSizes(subs [][]byte) []uint32 {
	if len(subs) < 2 {
		return nil
	}

	out := make([]uint32, len(subs)-1)
	zeros := 0

	for i, sub := range subs {
		var n uint32

		for _, b := range sub {
			if zeros == 2 && b <= 3 {
				n++

				zeros = 0
			}

			n++

			if b == 0 {
				zeros++
			} else {
				zeros = 0
			}
		}

		if i < len(out) {
			out[i] = n
		}
	}

	return out
}

// ctbCount is how many coding tree blocks of 64 samples a dimension takes.
func ctbCount(n int) int { return (n + 63) / 64 }

// startRow begins a substream. Without the wavefront the rows are one stream.
func (e *intraEncoder[P]) startRow(k int, sync *[nContexts]uint8) {
	if !e.wavefront {
		if k == 0 {
			e.bits = putBits{data: make([]byte, 0, e.hint)}
			e.cabac.init(&e.bits, int32(e.qp), sliceI, false)
		}

		return
	}

	e.bits = putBits{}
	e.cabac.init(&e.bits, int32(e.qp), sliceI, false)

	// 9.3.1 hands a row the contexts the row above left after its second block.
	if k > 0 {
		e.cabac.state = *sync
	}
}

// ctbRow codes one row. The end_of_subset_one_bit of 7.3.8.1 closes every row
// but the last.
func (e *intraEncoder[P]) ctbRow(k, rows, cols int, sync *[nContexts]uint8) error {
	for x := range cols {
		if err := e.tree(x*64, k*64, 6, 0); err != nil {
			return err
		}

		last := k == rows-1 && x == cols-1
		e.cabac.encodeTerminate(boolToBit(last))

		if e.wavefront && x == min(1, cols-1) {
			*sync = e.cabac.state
		}

		if e.wavefront && !last && x == cols-1 {
			e.cabac.encodeTerminate(1)
		}
	}

	return nil
}

func (e *intraEncoder[P]) reset(y, cb, cr []P, width, height, qp int) {
	e.width, e.height = width, height
	e.s = chromaSPS(len(cb), width*height)
	e.shiftW, e.shiftH = int(e.s.subWidthC)-1, int(e.s.subHeightC)-1
	e.strideC = width >> e.shiftW

	e.bitDepth = max(e.bitDepth, 8)
	if _, ok := any(y).([]uint8); ok {
		e.bitDepth = 8
	}

	e.s.bitDepthLuma, e.s.bitDepthChroma = uint8(e.bitDepth), uint8(e.bitDepth)

	off := 6 * (e.bitDepth - 8)
	cat := e.s.chromaArrayType()

	e.qp = qp
	e.qpY = qp + off
	e.qpDeblockC = int(chromaQP(clip3(int32(qp), 0, 57), cat))
	e.qpC = int(chromaQP(clip3(int32(qp), int32(-off), 57), cat)) + off
	e.lambda = lossyLambda(e.qpY)
	e.lambdaBase = lossyLambda(qp)
	e.src = [3][]P{y, cb, cr}
	e.p = pps{}

	e.recon[0] = regrow(e.recon[0], len(y))
	e.recon[1] = regrow(e.recon[1], len(cb))
	e.recon[2] = regrow(e.recon[2], len(cr))
	e.modes = regrow(e.modes, width/16*height/16)
	e.depth = regrow(e.depth, len(e.modes))
	e.coded[0] = regrow(e.coded[0], width/4*height/4)
	e.coded[1] = regrow(e.coded[1], len(cb)/16)
	e.edges = regrow(e.edges, width/4*height/4)

	e.bits = putBits{}
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

func (e *intraEncoder[P]) stride(cIdx int) int {
	if cIdx == 0 {
		return e.width
	}

	return e.strideC
}

// chromaSPS is the sampling the chroma planes describe by their size, which
// 6.2 fixes: a quarter of the luma is 4:2:0, a half 4:2:2, all of it 4:4:4 and
// none of it monochrome.
func chromaSPS(chroma, luma int) sps {
	switch {
	case chroma == luma:
		return sps{chromaFormatIDC: 3, subWidthC: 1, subHeightC: 1}
	case chroma*2 == luma:
		return sps{chromaFormatIDC: 2, subWidthC: 2, subHeightC: 1}
	case chroma*4 == luma:
		return sps{chromaFormatIDC: 1, subWidthC: 2, subHeightC: 2}
	default:
		return sps{chromaFormatIDC: 0, subWidthC: 1, subHeightC: 1}
	}
}

// chromaTBs is how many transform blocks a component takes over one luma
// transform block. 7.3.8.8 stacks two of them in 4:2:2.
func (e *intraEncoder[P]) chromaTBs() int {
	switch e.s.chromaArrayType() {
	case 0:
		return 0
	case 2:
		return 2
	default:
		return 1
	}
}

func (e *intraEncoder[P]) blockQP(cIdx int) int {
	if cIdx == 0 {
		return e.qpY
	}

	return e.qpC
}

// tree is the coding quadtree of 7.3.8.4. A full 32x32 chooses its own size; a
// block at the picture edge splits without a flag.
func (e *intraEncoder[P]) tree(x0, y0, log2Size, d int) error {
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
func (e *intraEncoder[P]) cuSize(x0, y0, d int) error {
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
func (e *intraEncoder[P]) cuDistortion(x0, y0, n int) int64 {
	dist := e.distortion(0, x0, y0, n, e.recon[0][y0*e.width+x0:], e.width)

	cs := e.strideC
	cx, cy, cn := x0>>e.shiftW, y0>>e.shiftH, n>>e.shiftW

	for c := 1; c < 3; c++ {
		for t := range e.chromaTBs() {
			y := cy + t*cn

			dist += e.distortion(c, cx, y, cn, e.recon[c][y*cs+cx:], cs)
		}
	}

	return dist
}

func (e *intraEncoder[P]) save(s *cuState[P], x0, y0, base int) {
	s.cabac = e.cabac
	s.bits = append(s.bits[:0], e.bits.data[base:]...)
	s.cur, s.nbits = e.bits.cur, e.bits.nbits

	bw := e.width / 16
	saveRect(s.y[:], e.recon[0], e.width, x0, y0, 32, 32)
	saveRect(s.modes[:], e.modes, bw, x0/16, y0/16, 2, 2)
	saveRect(s.depth[:], e.depth, bw, x0/16, y0/16, 2, 2)
	saveRect(s.codedY[:], e.coded[0], e.width/4, x0/4, y0/4, 8, 8)
	saveRect(s.edges[:], e.edges, e.width/4, x0/4, y0/4, 8, 8)

	if e.s.chromaArrayType() == 0 {
		return
	}

	cs, cw, ch := e.strideC, 32>>e.shiftW, 32>>e.shiftH
	cx, cy := x0>>e.shiftW, y0>>e.shiftH
	saveRect(s.cb[:], e.recon[1], cs, cx, cy, cw, ch)
	saveRect(s.cr[:], e.recon[2], cs, cx, cy, cw, ch)
	saveRect(s.codedC[:], e.coded[1], cs/4, cx/4, cy/4, cw/4, ch/4)
}

func (e *intraEncoder[P]) load(s *cuState[P], x0, y0, base int) {
	e.cabac = s.cabac
	e.bits.data = append(e.bits.data[:base], s.bits...)
	e.bits.cur, e.bits.nbits = s.cur, s.nbits

	bw := e.width / 16
	loadRect(e.recon[0], s.y[:], e.width, x0, y0, 32, 32)
	loadRect(e.modes, s.modes[:], bw, x0/16, y0/16, 2, 2)
	loadRect(e.depth, s.depth[:], bw, x0/16, y0/16, 2, 2)
	loadRect(e.coded[0], s.codedY[:], e.width/4, x0/4, y0/4, 8, 8)
	loadRect(e.edges, s.edges[:], e.width/4, x0/4, y0/4, 8, 8)

	if e.s.chromaArrayType() == 0 {
		return
	}

	cs, cw, ch := e.strideC, 32>>e.shiftW, 32>>e.shiftH
	cx, cy := x0>>e.shiftW, y0>>e.shiftH
	loadRect(e.recon[1], s.cb[:], cs, cx, cy, cw, ch)
	loadRect(e.recon[2], s.cr[:], cs, cx, cy, cw, ch)
	loadRect(e.coded[1], s.codedC[:], cs/4, cx/4, cy/4, cw/4, ch/4)
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

func (e *intraEncoder[P]) splitCtx(x0, y0, d int) int {
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
func (e *intraEncoder[P]) leaf(x0, y0, d, mode int) error {
	blocksWide := e.width / 16
	cand := lossyMPM(e.modes, blocksWide, x0/16, y0/16, 4)

	if mode < 0 {
		mode = e.lumaMode(x0, y0, cand)
	}

	var tus [4]lossyTU8Plan

	for i := range 4 {
		tus[i] = e.tu8(x0+i&1*8, y0+i>>1*8, mode)
	}

	var root [2][2]bool

	for i := range 4 {
		x, y := x0+i&1*8, y0+i>>1*8

		for c := range 2 {
			root[c][0] = root[c][0] || tus[i].cbfC8[c][0] || tus[i].cbfC8[c][1]
		}

		if !tus[i].split {
			e.markTU(x, y, 8)

			continue
		}

		for j := range 4 {
			e.markTU(x+j&1*4, y+j>>1*4, 4)
		}
	}

	e.cabac.encodeBin(ctxPartMode, 1)
	lossyIntraLumaMode(&e.cabac, mode, cand)
	e.chromaPredMode(&e.cabac)
	e.cabac.encodeBin(ctxSplitTransformFlag+1, 1)
	e.chromaCBF(&e.cabac, 0, root, root, e.chromaSecond(16, true))

	for i := range 4 {
		if err := e.tu8Code(&e.cabac, &tus[i], mode, root); err != nil {
			return err
		}
	}

	idx := y0/16*blocksWide + x0/16
	e.modes[idx] = mode
	e.depth[idx] = uint8(d)

	return nil
}

// cu32 codes a 32x32 coding unit, choosing between one transform unit of its
// own size and four 16x16 ones. A negative mode is searched for.
func (e *intraEncoder[P]) cu32(x0, y0, d, mode int) error {
	blocksWide := e.width / 16
	cand := lossyMPM(e.modes, blocksWide, x0/16, y0/16, 4)

	if mode < 0 {
		mode = e.lumaMode(x0, y0, cand)
	}

	t := &e.tu
	base := len(e.bits.data)

	e.save(&e.tuBase, x0, y0, base)

	coef, cbf := e.codeBlock(lossyBlock[P]{x: x0, y: y0, n: 32, mode: mode}, true)
	copy(t.y32[:], coef)
	t.whole = cbf
	t.wholeC = e.codeChroma(x0, y0, 5, mode, t.c32[0][:], t.c32[1][:])

	t.split = false
	whole := e.rdCost(e.cuDistortion(x0, y0, 32), e.tu32Rate(mode))

	e.save(&e.tuKept, x0, y0, base)
	e.load(&e.tuBase, x0, y0, base)

	for i := range 4 {
		x, y := x0+i&1*16, y0+i>>1*16

		coef, cbf = e.codeBlock(lossyBlock[P]{x: x, y: y, n: 16, mode: mode}, true)
		copy(t.y[i][:], coef)
		t.quad[i] = cbf
		t.quadC[i] = e.codeChroma(x, y, 4, mode, t.c[0][i][:], t.c[1][i][:])
	}

	t.split = true
	if whole <= e.rdCost(e.cuDistortion(x0, y0, 32), e.tu32Rate(mode)) {
		t.split = false

		e.load(&e.tuKept, x0, y0, base)
	}

	if t.split {
		for i := range 4 {
			e.markTU(x0+i&1*16, y0+i>>1*16, 16)
		}
	} else {
		e.markTU(x0, y0, 32)
	}

	lossyIntraLumaMode(&e.cabac, mode, cand)
	e.chromaPredMode(&e.cabac)

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

// chromaPredMode is intra_chroma_pred_mode of 7.3.8.5, which Table 8-2 reads
// as the luma mode itself. Monochrome codes none.
func (e *intraEncoder[P]) chromaPredMode(w *cabacWriter) {
	if e.chromaTBs() > 0 {
		w.encodeBin(ctxIntraChromaPredMode, 0)
	}
}

// chromaMode is the mode the chroma blocks predict with, which Table 8-3
// remaps for 4:2:2.
func (e *intraEncoder[P]) chromaMode(luma int) int {
	if e.s.chromaFormatIDC == 2 {
		return int(chroma422Map[luma])
	}

	return luma
}

// chromaLog2 is the size of one chroma transform block under a luma block of
// log2 samples, which only 4:4:4 leaves alone.
func (e *intraEncoder[P]) chromaLog2(log2 int) int {
	return log2 - e.shiftW
}

// chromaAt reports whether 7.3.8.8 carries chroma at a luma block of n, which
// below eight samples only 4:4:4 does.
func (e *intraEncoder[P]) chromaAt(n int) bool {
	return e.chromaTBs() > 0 && (n > 4 || e.s.chromaArrayType() == 3)
}

// chromaSecond is the condition of 7.3.8.8 under which 4:2:2 gives the second
// of its two stacked blocks a coded block flag of its own.
func (e *intraEncoder[P]) chromaSecond(n int, split bool) bool {
	return e.s.chromaArrayType() == 2 && (!split || n == 8)
}

// codeChroma codes the chroma transform blocks under a luma block of log2
// samples at (x, y), and returns their coded block flags.
func (e *intraEncoder[P]) codeChroma(x, y, log2, mode int, cb, cr []int32) [2][2]bool {
	var cbf [2][2]bool

	cn := 1 << e.chromaLog2(log2)
	cx, cy := x>>e.shiftW, y>>e.shiftH
	cmode := e.chromaMode(mode)
	dst := [2][]int32{cb, cr}

	for c := range 2 {
		for t := range e.chromaTBs() {
			coef, f := e.codeBlock(lossyBlock[P]{cIdx: c + 1, x: cx, y: cy + t*cn,
				n: cn, mode: cmode}, true)

			copy(dst[c][t*cn*cn:], coef)
			cbf[c][t] = f
		}
	}

	return cbf
}

// chromaCBF writes cbf_cb and cbf_cr of 7.3.8.8, which up gates below the root.
func (e *intraEncoder[P]) chromaCBF(w *cabacWriter, depth int, cbf, up [2][2]bool, second bool) {
	if e.chromaTBs() == 0 {
		return
	}

	for c := range 2 {
		if depth != 0 && !up[c][0] {
			continue
		}

		w.encodeBin(ctxCBFCBCR+depth, boolToBit(cbf[c][0]))

		if second {
			w.encodeBin(ctxCBFCBCR+depth, boolToBit(cbf[c][1]))
		}
	}
}

// chromaResidual writes the levels of the chroma blocks under one luma block.
// 7.3.8.10 codes both Cb blocks before either Cr one.
func (e *intraEncoder[P]) chromaResidual(w *cabacWriter, log2, mode int, cb, cr []int32,
	cbf [2][2]bool,
) error {
	clog2 := e.chromaLog2(log2)
	n := 1 << (2 * clog2)
	cmode := e.chromaMode(mode)
	src := [2][]int32{cb, cr}

	for c := range 2 {
		for t := range e.chromaTBs() {
			if !cbf[c][t] {
				continue
			}

			if err := e.residual(w, src[c][t*n:(t+1)*n], clog2, c+1, cmode); err != nil {
				return err
			}
		}
	}

	return nil
}

// tu32Rate is what the chosen transform tree costs, coded into a writer that
// only counts.
func (e *intraEncoder[P]) tu32Rate(mode int) int64 {
	w := e.cabac.counter()
	_ = e.tu32Tree(&w, mode)

	return w.rate
}

// tu32Tree is the transform_tree of 7.3.8.8 for a 32x32 coding unit.
func (e *intraEncoder[P]) tu32Tree(w *cabacWriter, mode int) error {
	t := &e.tu

	root := t.wholeC
	if t.split {
		root = [2][2]bool{}

		for i := range 4 {
			for c := range 2 {
				root[c][0] = root[c][0] || t.quadC[i][c][0] || t.quadC[i][c][1]
			}
		}
	}

	w.encodeBin(ctxSplitTransformFlag, boolToBit(t.split))
	e.chromaCBF(w, 0, root, root, e.chromaSecond(32, t.split))

	if !t.split {
		if err := e.codedResidual(w, t.y32[:], t.whole, 5, 0, mode, 0); err != nil {
			return err
		}

		return e.chromaResidual(w, 5, mode, t.c32[0][:], t.c32[1][:], t.wholeC)
	}

	for i := range 4 {
		w.encodeBin(ctxSplitTransformFlag+1, 0)
		e.chromaCBF(w, 1, t.quadC[i], root, e.chromaSecond(16, false))

		if err := e.codedResidual(w, t.y[i][:], t.quad[i], 4, 0, mode, 1); err != nil {
			return err
		}

		if err := e.chromaResidual(w, 4, mode, t.c[0][i][:], t.c[1][i][:], t.quadC[i]); err != nil {
			return err
		}
	}

	return nil
}

// codedResidual writes cbf_luma and the levels behind it. Table 9-49 gives the
// flag its own context at the root of a transform tree.
func (e *intraEncoder[P]) codedResidual(w *cabacWriter, coef []int32, cbf bool,
	log2Size, cIdx, mode, trafoDepth int,
) error {
	w.encodeBin(ctxCBFLuma+boolToInt(trafoDepth == 0), boolToBit(cbf))

	if !cbf {
		return nil
	}

	return e.residual(w, coef, log2Size, cIdx, mode)
}

func (e *intraEncoder[P]) residual(w *cabacWriter, coef []int32, log2Size, cIdx, mode int) error {
	return encodeResidual(w, &e.s, &e.p, coef,
		residualBlock{log2Size: log2Size, cIdx: cIdx, predModeIntra: mode, intra: true})
}

// tu8 codes one 8x8 quadrant of a 16x16 coding unit, choosing between one 8x8
// transform and four 4x4 ones on their coded cost.
func (e *intraEncoder[P]) tu8(x, y, mode int) lossyTU8Plan {
	w := e.width

	var before, kept tu8State[P]

	e.saveTU8(&before, x, y)

	quarters := [4]lossyBlock[P]{}
	for j := range 4 {
		quarters[j] = lossyBlock[P]{x: x + j&1*4, y: y + j>>1*4, n: 4, mode: mode, dst: true}
	}

	whole := lossyBlock[P]{x: x, y: y, n: 8, mode: mode}

	split := lossyTU8Plan{split: true}

	for j := range 4 {
		coef, cbf := e.codeBlock(quarters[j], true)
		copy(split.y[j][:], coef)
		split.cbfY[j] = cbf
	}

	splitCost := e.rdCost(e.distortion(0, x, y, 8, e.recon[0][y*w+x:], w),
		e.tu8Rate(&split, mode))

	e.saveTU8(&kept, x, y)
	e.loadTU8(&before, x, y)

	var unsplit lossyTU8Plan

	coef, cbf := e.codeBlock(whole, true)
	copy(unsplit.y8[:], coef)
	unsplit.cbfY8 = cbf

	chosen := unsplit
	if splitCost < e.rdCost(e.distortion(0, x, y, 8, e.recon[0][y*w+x:], w),
		e.tu8Rate(&unsplit, mode)) {
		chosen = split

		e.loadTU8(&kept, x, y)
	}

	if e.chromaTBs() == 0 {
		return chosen
	}

	// The luma arms are weighed on luma alone, so the chroma is coded once the
	// split it has to follow is settled.
	if !chosen.split || !e.chromaAt(4) {
		chosen.cbfC8 = e.codeChroma(x, y, 3, mode, chosen.c8[0][:], chosen.c8[1][:])

		return chosen
	}

	for j := range 4 {
		bx, by := x+j&1*4, y+j>>1*4
		chosen.cbfC[j] = e.codeChroma(bx, by, 2, mode, chosen.c[0][j][:], chosen.c[1][j][:])

		for c := range 2 {
			chosen.cbfC8[c][0] = chosen.cbfC8[c][0] || chosen.cbfC[j][c][0]
		}
	}

	return chosen
}

// tu8Code writes one 8x8 quadrant of a 16x16 coding unit: its split flag, the
// chroma flags 7.3.8.8 puts at this depth, and the levels behind them.
func (e *intraEncoder[P]) tu8Code(w *cabacWriter, p *lossyTU8Plan, mode int, up [2][2]bool) error {
	w.encodeBin(ctxSplitTransformFlag+2, boolToBit(p.split))
	e.chromaCBF(w, 1, p.cbfC8, up, e.chromaSecond(8, p.split))

	if !p.split {
		if err := e.codedResidual(w, p.y8[:], p.cbfY8, 3, 0, mode, 1); err != nil {
			return err
		}

		return e.chromaResidual(w, 3, mode, p.c8[0][:], p.c8[1][:], p.cbfC8)
	}

	deep := e.chromaAt(4)

	for j := range 4 {
		if deep {
			e.chromaCBF(w, 2, p.cbfC[j], p.cbfC8, false)
		}

		if err := e.codedResidual(w, p.y[j][:], p.cbfY[j], 2, 0, mode, 2); err != nil {
			return err
		}

		if !deep {
			continue
		}

		if err := e.chromaResidual(w, 2, mode, p.c[0][j][:], p.c[1][j][:], p.cbfC[j]); err != nil {
			return err
		}
	}

	if deep {
		return nil
	}

	// 7.3.8.10 codes the one chroma block of the whole quadrant behind the last
	// of its luma blocks.
	return e.chromaResidual(w, 3, mode, p.c8[0][:], p.c8[1][:], p.cbfC8)
}

// tu8State is the reconstruction one 8x8 transform unit leaves behind, so the
// arm that wins does not have to be coded a second time.
type tu8State[P pixel] struct {
	y     [8 * 8]P
	coded [4]uint8
}

func (e *intraEncoder[P]) saveTU8(s *tu8State[P], x, y int) {
	saveRect(s.y[:], e.recon[0], e.width, x, y, 8, 8)
	saveRect(s.coded[:], e.coded[0], e.width/4, x/4, y/4, 2, 2)
}

func (e *intraEncoder[P]) loadTU8(s *tu8State[P], x, y int) {
	loadRect(e.recon[0], s.y[:], e.width, x, y, 8, 8)
	loadRect(e.coded[0], s.coded[:], e.width/4, x/4, y/4, 2, 2)
}

func (e *intraEncoder[P]) lumaMode(x, y int, cand [3]int) int {
	b := lossyBlock[P]{x: x, y: y, n: 16}
	e.prepareRef(b)

	var (
		short [modeShortlist]int
		score [modeShortlist]int64
		slot  [modeShortlist]int
	)

	for i := range short {
		short[i], score[i], slot[i] = intraPlanar, 1<<62, i
	}

	pred := e.scratch.pred[:16*16]

	// The smoothing has one outcome at a fixed size, so it is built once.
	filtered := false

	for mode := intraPlanar; mode <= 34; mode++ {
		ref := &e.scratch.base

		if filterFlag(mode, 16, 0, &e.s) && !e.s.intraSmoothingDisabled {
			if !filtered {
				e.scratch.ref.copyFrom(&e.scratch.base)
				filterRef(&e.scratch.ref, mode, 0, e.bitDepth, &e.s)

				filtered = true
			}

			ref = &e.scratch.ref
		}

		intraPredict(pred, 0, 16, ref, mode, 0, e.bitDepth)

		s := e.satd(x, y, pred, 16, score[modeShortlist-1])

		for i := range short {
			if s >= score[i] {
				continue
			}

			// The prediction goes in the slot the evicted mode gives up.
			free := slot[modeShortlist-1]

			copy(short[i+1:], short[i:len(short)-1])
			copy(score[i+1:], score[i:len(score)-1])
			copy(slot[i+1:], slot[i:len(slot)-1])
			short[i], score[i], slot[i] = mode, s, free
			copy(e.scratch.modePred[free][:], pred)

			break
		}
	}

	bestMode, bestCost := short[0], int64(-1)

	for i, mode := range short {
		b.mode = mode
		b.pred = e.scratch.modePred[slot[i]][:]
		coef, trial, cbf := e.blockData(b, false)

		// The rate only adds, so a mode whose distortion alone has lost is not
		// coded to find out what it costs.
		dist := e.distortion(0, x, y, 16, trial, 16)
		if bestCost >= 0 && e.rdCost(dist, 0) >= bestCost {
			continue
		}

		cost := e.rdCost(dist, e.modeRate(cand, mode, coef, cbf))
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
func (e *intraEncoder[P]) satd(x, y int, pred []P, n int, limit int64) int64 {
	src := e.src[0]
	stride := e.width

	var sum int64

	// The kernels are eight bit; anything deeper takes the Go path.
	s8, _ := any(src).([]uint8)
	p8, _ := any(pred).([]uint8)

	for by := 0; by < n; by += 8 {
		for bx := 0; bx < n; bx += 8 {
			if sum >= limit {
				return sum
			}

			so, po := (y+by)*stride+x+bx, by*n+bx

			if k := satd16x8Asm; k != nil && s8 != nil && bx+16 <= n {
				sum += k(s8[so:], stride, p8[po:], n)
				bx += 8

				continue
			}

			sum += satd8x8Go(src[so:], stride, pred[po:], n)
		}
	}

	return sum
}

// satd8x8Go is one 8x8 of satd, the rows transformed and then the columns.
func satd8x8Go[P pixel](src []P, srcStride int, pred []P, predStride int) int64 {
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
func (e *intraEncoder[P]) modeRate(cand [3]int, mode int, coef []int32, cbf bool) int64 {
	w := e.cabac.counter()

	lossyIntraLumaMode(&w, mode, cand)

	w.encodeBin(ctxCBFLuma, boolToBit(cbf))

	if cbf {
		_ = encodeResidual(&w, &e.s, &e.p, coef,
			residualBlock{log2Size: 4, predModeIntra: mode, intra: true})
	}

	return w.rate
}

func (e *intraEncoder[P]) tu8Rate(plan *lossyTU8Plan, mode int) int64 {
	w := e.cabac.counter()

	w.encodeBin(ctxSplitTransformFlag+2, boolToBit(plan.split))

	if plan.split {
		for i := range plan.y {
			e.rateResidual(&w, plan.y[i][:], plan.cbfY[i], 2, mode)
		}
	} else {
		e.rateResidual(&w, plan.y8[:], plan.cbfY8, 3, mode)
	}

	return w.rate
}

// modeShortlist is how many of the 35 intra modes are coded in full. Six and
// eight measure the same; four is worse on a large picture.
const modeShortlist = 6

// lambdaShift is the fraction lossyLambda carries.
const lambdaShift = 8

// rdCost weighs squared error against bits, both at rateShift.
func (e *intraEncoder[P]) rdCost(dist, rate int64) int64 {
	return dist<<(rateShift+lambdaShift) + e.lambda*rate
}

func (e *intraEncoder[P]) rateResidual(w *cabacWriter, coef []int32, cbf bool, log2Size, mode int) {
	w.encodeBin(ctxCBFLuma, boolToBit(cbf))

	if cbf {
		_ = encodeResidual(w, &e.s, &e.p, coef,
			residualBlock{log2Size: log2Size, predModeIntra: mode, intra: true})
	}
}

// distortion is the squared error between the source and block, which is a
// trial reconstruction or a window on the picture reconstruction.
func (e *intraEncoder[P]) distortion(cIdx, x, y, n int, block []P, blockStride int) int64 {
	stride := e.stride(cIdx)
	src := e.src[cIdx]

	if k := sse8Asm; k != nil {
		if s8, ok := any(src).([]uint8); ok {
			b8, _ := any(block).([]uint8)

			return k(s8[y*stride+x:], stride, b8, blockStride, n)
		}
	}

	return sse8Go(src[y*stride+x:], stride, block, blockStride, n)
}

// sse8Go is the squared error of an n by n block.
func sse8Go[P pixel](src []P, srcStride int, block []P, blockStride, n int) int64 {
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
func (e *intraEncoder[P]) codeBlock(b lossyBlock[P], rdoq bool) ([]int32, bool) {
	e.prepareRef(b)
	coef, block, cbf := e.blockData(b, rdoq)

	stride := e.stride(b.cIdx)
	for j := range b.n {
		copy(e.recon[b.cIdx][(b.y+j)*stride+b.x:], block[j*b.n:(j+1)*b.n])
	}

	e.markCoded(b.cIdx, b.x, b.y, b.n)

	return coef, cbf
}

// markCoded records that a block of the plane has been reconstructed, at the
// 4x4 granularity the reference samples are looked up in.
func (e *intraEncoder[P]) markCoded(cIdx, x, y, n int) {
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
func (e *intraEncoder[P]) blockData(b lossyBlock[P], rdoq bool) ([]int32, []P, bool) {
	n := b.n
	count := n * n
	stride := e.stride(b.cIdx)
	qp := e.blockQP(b.cIdx)

	pred := e.scratch.pred[:count]
	residual := e.scratch.residual[:count]
	coef := e.scratch.coef[:count]
	reconCoef := e.scratch.reconCoef[:count]

	if b.pred != nil {
		copy(pred, b.pred)
	} else {
		e.scratch.ref.copyFrom(&e.scratch.base)
		filterRef(&e.scratch.ref, b.mode, b.cIdx, e.bitDepth, &e.s)
		intraPredict(pred, 0, n, &e.scratch.ref, b.mode, b.cIdx, e.bitDepth)
	}

	src := e.src[b.cIdx]

	for j := range n {
		for i := range n {
			residual[j*n+i] = int32(src[(b.y+j)*stride+b.x+i]) - int32(pred[j*n+i])
		}
	}

	if b.dst {
		forwardTransformDST4(reconCoef, residual, e.bitDepth)
	} else {
		forwardTransform(reconCoef, residual, n, e.bitDepth)
	}

	if rdoq {
		e.rdoq(coef, reconCoef, n, qp, b.cIdx, b.mode)
	} else {
		quantize(coef, reconCoef, n, qp, e.bitDepth)
	}

	if !hasCoefficients(coef) {
		return coef, pred, false
	}

	copy(reconCoef, coef)
	dequant(reconCoef, nil, n, qp, e.bitDepth, false)
	inverseTransform(reconCoef, n, b.dst, e.bitDepth, false, &e.scratch.transform)
	addResidual(pred, n, 0, 0, n, residualShiftBits(e.bitDepth, false), reconCoef, e.bitDepth)

	return coef, pred, true
}

// prepareRef builds the reference samples of 8.4.4.2.2, which do not depend on
// the prediction mode.
func (e *intraEncoder[P]) prepareRef(b lossyBlock[P]) {
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

	e.scratch.base.substitute(avail, e.bitDepth)
}

// lambdaScale is the constant in front of the quantiser's own curve, at Q8,
// which is the usual 0.57.
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

func hasCoefficients(coef []int32) bool {
	for _, v := range coef {
		if v != 0 {
			return true
		}
	}

	return false
}
