package hevc

// Picture is one decoded picture. Planes hold either 8-bit or 16-bit samples
// depending on the bit depth, with Stride in samples.
type Picture struct {
	Width, Height int

	// CropW and CropH are the dimensions after the conformance window, which
	// is what a caller should display. Width and Height stay as decoded,
	// since prediction reads the whole plane.
	CropX, CropY int
	CropW, CropH int

	ChromaFormat int

	// BitDepth and BitDepthC are the luma and chroma sample depths, which
	// 7.4.3.2 lets differ. Both planes are stored 16-bit if either exceeds 8.
	BitDepth  int
	BitDepthC int

	POC int

	Y, Cb, Cr       []uint8
	Y16, Cb16, Cr16 []uint16

	StrideY, StrideC int
	WidthC, HeightC  int

	Col  []colMotion
	ColW int

	pool *picPool
	refs int32
}

// Release hands the picture's memory back to the decoder that produced it, to
// be reused by a later picture. It is optional: one that is never released is
// collected as any other value would be. Reading the planes afterwards is a
// mistake; releasing twice is not, and does nothing.
func (p *Picture) Release() {
	p.release()
}

func (p *Picture) acquire() {
	if p != nil {
		p.refs++
	}
}

func (p *Picture) release() {
	if p == nil || p.refs == 0 {
		return
	}

	p.refs--
	if p.refs > 0 {
		return
	}

	g := p.geom()

	b := picBufs{
		col: p.Col,
		y:   p.Y, cb: p.Cb, cr: p.Cr,
		y16: p.Y16, cb16: p.Cb16, cr16: p.Cr16,
	}

	pool := p.pool
	p.pool = nil

	// Dropping the planes turns a read after release into a panic rather than
	// a later picture's samples.
	p.Col = nil
	p.Y, p.Cb, p.Cr = nil, nil, nil
	p.Y16, p.Cb16, p.Cr16 = nil, nil, nil

	pool.put(g, b)
}

// picBufs is the sample memory of one picture, the only part worth recycling.
type picBufs struct {
	col             []colMotion
	y, cb, cr       []uint8
	y16, cb16, cr16 []uint16
}

// picGeom is everything that fixes a picture's buffer sizes, so a recycled one
// needs no reallocation.
type picGeom struct {
	strideY, height  int
	strideC, heightC int
	colLen           int
	deep             bool
}

func (p *Picture) geom() picGeom {
	return picGeom{
		strideY: p.StrideY, height: p.Height,
		strideC: p.StrideC, heightC: p.HeightC,
		colLen: len(p.Col),
		deep:   p.Y16 != nil,
	}
}

// picPoolDepth bounds how many pictures of one shape are kept, so a sequence
// that changes resolution cannot make the pool grow without end.
const picPoolDepth = 8

// picPool holds the pictures nobody references any more. A decoder keeps one,
// so everything it recycles came from the same sequence.
type picPool struct {
	free map[picGeom][]picBufs
}

func (pl *picPool) get(g picGeom) (picBufs, bool) {
	if pl == nil {
		return picBufs{}, false
	}

	free := pl.free[g]
	if len(free) == 0 {
		return picBufs{}, false
	}

	b := free[len(free)-1]
	free[len(free)-1] = picBufs{}
	pl.free[g] = free[:len(free)-1]

	return b, true
}

func (pl *picPool) put(g picGeom, b picBufs) {
	if pl == nil {
		return
	}

	if pl.free == nil {
		pl.free = make(map[picGeom][]picBufs)
	}

	if len(pl.free[g]) < picPoolDepth {
		pl.free[g] = append(pl.free[g], b)
	}
}

func newPicture(pool *picPool, s *sps) *Picture {
	p := &Picture{
		Width:        int(s.picWidthInLumaSamples),
		Height:       int(s.picHeightInLumaSamples),
		ChromaFormat: int(s.chromaFormatIDC),
		BitDepth:     int(s.bitDepthLuma),
		BitDepthC:    int(s.bitDepthChroma),
	}

	p.CropX = int(s.confWinLeft) * s.subWidthC
	p.CropY = int(s.confWinTop) * s.subHeightC
	p.CropW = int(s.croppedWidth())
	p.CropH = int(s.croppedHeight())

	p.StrideY = p.Width
	p.WidthC = p.Width / s.subWidthC
	p.HeightC = p.Height / s.subHeightC
	p.StrideC = p.WidthC

	if s.chromaFormatIDC == 0 {
		p.WidthC, p.HeightC, p.StrideC = 0, 0, 0
	}

	p.ColW = (p.Width + 15) / 16

	g := picGeom{
		strideY: p.StrideY, height: p.Height,
		strideC: p.StrideC, heightC: p.HeightC,
		colLen: p.ColW * ((p.Height + 15) / 16),
		deep:   max(s.bitDepthLuma, s.bitDepthChroma) > 8,
	}

	// A recycled picture keeps only its buffers; everything derived from the
	// sequence has already been filled in above, and the samples are zeroed so
	// a reused one is indistinguishable from a fresh one.
	if r, ok := pool.get(g); ok {
		p.Col, p.Y, p.Cb, p.Cr = r.col, r.y, r.cb, r.cr
		p.Y16, p.Cb16, p.Cr16 = r.y16, r.cb16, r.cr16

		clear(p.Col)
		clear(p.Y)
		clear(p.Cb)
		clear(p.Cr)
		clear(p.Y16)
		clear(p.Cb16)
		clear(p.Cr16)

		p.pool, p.refs = pool, 1

		return p
	}

	p.pool, p.refs = pool, 1

	p.Col = make([]colMotion, g.colLen)

	if g.deep {
		p.Y16 = make([]uint16, p.StrideY*p.Height)
		p.Cb16 = make([]uint16, p.StrideC*p.HeightC)
		p.Cr16 = make([]uint16, p.StrideC*p.HeightC)

		return p
	}

	p.Y = make([]uint8, p.StrideY*p.Height)
	p.Cb = make([]uint8, p.StrideC*p.HeightC)
	p.Cr = make([]uint8, p.StrideC*p.HeightC)

	return p
}

// deep reports whether the planes hold 16-bit samples.
func (p *Picture) deep() bool {
	return p.Y16 != nil
}

// depth is the sample depth of one component.
func (p *Picture) depth(cIdx int) int {
	if cIdx == 0 {
		return p.BitDepth
	}

	return p.BitDepthC
}

func (p *Picture) plane8(cIdx int) ([]uint8, int) {
	switch cIdx {
	case 0:
		return p.Y, p.StrideY
	case 1:
		return p.Cb, p.StrideC
	default:
		return p.Cr, p.StrideC
	}
}

func (p *Picture) plane16(cIdx int) ([]uint16, int) {
	switch cIdx {
	case 0:
		return p.Y16, p.StrideY
	case 1:
		return p.Cb16, p.StrideC
	default:
		return p.Cr16, p.StrideC
	}
}

// The collocated motion field, subsampled to 16x16 as 8.5.3.2.9 allows.
// Reference pictures are held by POC so the collocated picture's own lists are
// not needed later.
type colMotion struct {
	info    mvInfo
	refPoc  [2]int32
	refLong [2]bool
	intra   bool
}

func (p *Picture) colIndex(x, y int) int {
	return (y>>4)*p.ColW + x>>4
}
