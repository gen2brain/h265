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
		return f.decodeItem(it)
	}

	g, tiles, err := f.gridOf(it)
	if err != nil {
		return nil, err
	}

	if n := f.limit(); n > 0 && g.w*g.h > n {
		return nil, ErrUnsupported
	}

	pics := make([]*hevc.Picture, len(tiles))

	for i, id := range tiles {
		t := f.meta.items[id]
		if t == nil {
			return nil, ErrInvalid
		}

		if pics[i], err = f.decodeItem(t); err != nil {
			return nil, err
		}
	}

	first := pics[0]
	tw, th := first.CropW, first.CropH

	for _, p := range pics {
		if p.CropW != tw || p.CropH != th ||
			p.ChromaFormat != first.ChromaFormat || p.BitDepth != first.BitDepth {
			return nil, ErrInvalid
		}
	}

	if tw*g.cols < g.w || th*g.rows < g.h {
		return nil, ErrInvalid
	}

	return stitch(pics, g, tw, th), nil
}

// stitch lays the tiles out row-major into one picture the size the grid
// declares, which may be smaller than the tiles cover.
func stitch(pics []*hevc.Picture, g gridInfo, tw, th int) *hevc.Picture {
	first := pics[0]

	sw, sh := 1, 1

	switch first.ChromaFormat {
	case 1:
		sw, sh = 2, 2
	case 2:
		sw = 2
	}

	out := &hevc.Picture{
		Width:        g.w,
		Height:       g.h,
		CropW:        g.w,
		CropH:        g.h,
		ChromaFormat: first.ChromaFormat,
		BitDepth:     first.BitDepth,
	}

	out.StrideY = g.w
	out.WidthC = (g.w + sw - 1) / sw
	out.HeightC = (g.h + sh - 1) / sh
	out.StrideC = out.WidthC

	if first.ChromaFormat == 0 {
		out.WidthC, out.HeightC, out.StrideC = 0, 0, 0
	}

	if first.BitDepth > 8 {
		out.Y16 = make([]uint16, out.StrideY*g.h)
		out.Cb16 = make([]uint16, out.StrideC*out.HeightC)
		out.Cr16 = make([]uint16, out.StrideC*out.HeightC)
	} else {
		out.Y = make([]uint8, out.StrideY*g.h)
		out.Cb = make([]uint8, out.StrideC*out.HeightC)
		out.Cr = make([]uint8, out.StrideC*out.HeightC)
	}

	for i, p := range pics {
		row, col := i/g.cols, i%g.cols

		for pl := range 3 {
			sx, sy := col*tw, row*th
			cw, ch := tw, th
			ow, oh := g.w, g.h
			ss, ds := p.StrideY, out.StrideY

			if pl != 0 {
				if first.ChromaFormat == 0 {
					continue
				}

				sx, sy = sx/sw, sy/sh
				cw, ch = cw/sw, ch/sh
				ow, oh = out.WidthC, out.HeightC
				ss, ds = p.StrideC, out.StrideC
			}

			cw = min(cw, ow-sx)
			ch = min(ch, oh-sy)

			if cw <= 0 || ch <= 0 {
				continue
			}

			sox, soy := p.CropX, p.CropY
			if pl != 0 {
				sox, soy = sox/sw, soy/sh
			}

			if first.BitDepth > 8 {
				src, dst := planes16(p, out, pl)
				for y := range ch {
					s := (soy+y)*ss + sox
					d := (sy+y)*ds + sx
					copy(dst[d:d+cw], src[s:s+cw])
				}

				continue
			}

			src, dst := planes8(p, out, pl)
			for y := range ch {
				s := (soy+y)*ss + sox
				d := (sy+y)*ds + sx
				copy(dst[d:d+cw], src[s:s+cw])
			}
		}
	}

	return out
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

	pics := make([]*hevc.Picture, len(tiles))

	for i, id := range tiles {
		a := f.alphaOf(id)
		if a == nil {
			return nil, nil
		}

		if pics[i], err = f.decodeItem(a); err != nil {
			return nil, err
		}
	}

	first := pics[0]
	tw, th := first.CropW, first.CropH

	for _, p := range pics {
		if p.CropW != tw || p.CropH != th || p.ChromaFormat != 0 ||
			p.BitDepth != first.BitDepth {
			return nil, ErrInvalid
		}
	}

	return stitch(pics, g, tw, th), nil
}
