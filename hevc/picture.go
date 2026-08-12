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
	BitDepth     int
	POC          int

	Y, Cb, Cr       []uint8
	Y16, Cb16, Cr16 []uint16

	StrideY, StrideC int
	WidthC, HeightC  int

	Col  []colMotion
	ColW int
}

func newPicture(s *sps) *Picture {
	p := &Picture{
		Width:        int(s.picWidthInLumaSamples),
		Height:       int(s.picHeightInLumaSamples),
		ChromaFormat: int(s.chromaFormatIDC),
		BitDepth:     int(s.bitDepthLuma),
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
	p.Col = make([]colMotion, p.ColW*((p.Height+15)/16))

	if s.bitDepthLuma > 8 {
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
