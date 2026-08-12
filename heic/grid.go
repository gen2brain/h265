package heic

import (
	"github.com/gen2brain/h265/hevc"
)

type gridInfo struct {
	rows, cols int
	w, h       int
}

func parseGrid(b []byte) (gridInfo, error) {
	r := &reader{b: b}
	r.u8()

	flags := r.u8()

	var g gridInfo

	g.rows = int(r.u8()) + 1
	g.cols = int(r.u8()) + 1

	if flags&1 != 0 {
		g.w, g.h = int(r.u32()), int(r.u32())
	} else {
		g.w, g.h = int(r.u16()), int(r.u16())
	}

	if r.err || g.w == 0 || g.h == 0 {
		return g, ErrInvalid
	}

	return g, nil
}

func (f *file) gridOf(it *item) (gridInfo, []uint32, error) {
	data, err := f.meta.data(it, f.data)
	if err != nil {
		return gridInfo{}, nil, err
	}

	g, err := parseGrid(data)
	if err != nil {
		return gridInfo{}, nil, err
	}

	tiles := f.meta.refsTo("dimg", it.id)
	if len(tiles) != g.rows*g.cols {
		return gridInfo{}, nil, ErrInvalid
	}

	return g, tiles, nil
}

// decodeImage decodes an item, stitching the tiles first when it is a grid.
func (f *file) decodeImage(it *item) (*hevc.Picture, error) {
	if it.unsupported {
		return nil, ErrUnsupported
	}

	if it.typ != "grid" {
		var d hevc.Decoder

		return f.decodeItem(&d, it)
	}

	g, tiles, err := f.gridOf(it)
	if err != nil {
		return nil, err
	}

	if n := f.limit(); n > 0 && g.w*g.h > n {
		return nil, ErrUnsupported
	}

	return f.decodeTiles(g, tiles)
}

// decodeTiles decodes the tiles one at a time and copies each into the output
// as it arrives, so only one tile is held at once and one decoder serves them
// all.
func (f *file) decodeTiles(g gridInfo, tiles []uint32) (*hevc.Picture, error) {
	var (
		d   hevc.Decoder
		out *hevc.Picture
		tw  int
		th  int
	)

	for i, id := range tiles {
		t := f.meta.items[id]
		if t == nil {
			return nil, ErrInvalid
		}

		pic, err := f.decodeItem(&d, t)
		if err != nil {
			return nil, err
		}

		if out == nil {
			tw, th = pic.CropW, pic.CropH
			if tw*g.cols < g.w || th*g.rows < g.h {
				return nil, ErrInvalid
			}

			out = newGrid(pic, g)
		}

		if pic.CropW != tw || pic.CropH != th ||
			pic.ChromaFormat != out.ChromaFormat || pic.BitDepth != out.BitDepth {
			return nil, ErrInvalid
		}

		blit(out, pic, g, i, tw, th)
	}

	if out == nil {
		return nil, ErrInvalid
	}

	return out, nil
}

// newGrid allocates the stitched picture, which may be smaller than the tiles
// cover.
func newGrid(first *hevc.Picture, g gridInfo) *hevc.Picture {
	sw, sh := subsampling(first.ChromaFormat)

	out := &hevc.Picture{
		Width:        g.w,
		Height:       g.h,
		CropW:        g.w,
		CropH:        g.h,
		ChromaFormat: first.ChromaFormat,
		BitDepth:     first.BitDepth,
		StrideY:      g.w,
	}

	if first.ChromaFormat != 0 {
		out.WidthC = (g.w + sw - 1) / sw
		out.HeightC = (g.h + sh - 1) / sh
		out.StrideC = out.WidthC
	}

	if first.BitDepth > 8 {
		out.Y16 = make([]uint16, out.StrideY*g.h)
		out.Cb16 = make([]uint16, out.StrideC*out.HeightC)
		out.Cr16 = make([]uint16, out.StrideC*out.HeightC)

		return out
	}

	out.Y = make([]uint8, out.StrideY*g.h)
	out.Cb = make([]uint8, out.StrideC*out.HeightC)
	out.Cr = make([]uint8, out.StrideC*out.HeightC)

	return out
}

func subsampling(chromaFormat int) (int, int) {
	switch chromaFormat {
	case 1:
		return 2, 2
	case 2:
		return 2, 1
	}

	return 1, 1
}

// blit copies tile i of the grid into its place in out.
func blit(out, p *hevc.Picture, g gridInfo, i, tw, th int) {
	sw, sh := subsampling(out.ChromaFormat)
	row, col := i/g.cols, i%g.cols

	for pl := range 3 {
		sx, sy := col*tw, row*th
		cw, ch := tw, th
		ow, oh := g.w, g.h
		ss, ds := p.StrideY, out.StrideY
		sox, soy := p.CropX, p.CropY

		if pl != 0 {
			if out.ChromaFormat == 0 {
				return
			}

			sx, sy = sx/sw, sy/sh
			cw, ch = cw/sw, ch/sh
			ow, oh = out.WidthC, out.HeightC
			ss, ds = p.StrideC, out.StrideC
			sox, soy = sox/sw, soy/sh
		}

		cw = min(cw, ow-sx)
		ch = min(ch, oh-sy)

		if cw <= 0 || ch <= 0 {
			continue
		}

		if out.BitDepth > 8 {
			src, dst := planes16(p, out, pl)
			for y := range ch {
				copy(dst[(sy+y)*ds+sx:][:cw], src[(soy+y)*ss+sox:][:cw])
			}

			continue
		}

		src, dst := planes8(p, out, pl)
		for y := range ch {
			copy(dst[(sy+y)*ds+sx:][:cw], src[(soy+y)*ss+sox:][:cw])
		}
	}
}

func planes8(src, dst *hevc.Picture, pl int) ([]uint8, []uint8) {
	switch pl {
	case 0:
		return src.Y, dst.Y
	case 1:
		return src.Cb, dst.Cb
	default:
		return src.Cr, dst.Cr
	}
}

func planes16(src, dst *hevc.Picture, pl int) ([]uint16, []uint16) {
	switch pl {
	case 0:
		return src.Y16, dst.Y16
	case 1:
		return src.Cb16, dst.Cb16
	default:
		return src.Cr16, dst.Cr16
	}
}

// gridAlpha assembles a grid's alpha from the auxiliary items on its tiles.
func (f *file) gridAlpha(it *item) (*hevc.Picture, error) {
	g, tiles, err := f.gridOf(it)
	if err != nil {
		return nil, err
	}

	alpha := make([]uint32, len(tiles))

	for i, id := range tiles {
		a := f.alphaOf(id)
		if a == nil {
			return nil, nil
		}

		alpha[i] = a.id
	}

	return f.decodeTiles(g, alpha)
}
