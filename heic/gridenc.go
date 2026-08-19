package heic

import (
	"image"

	"github.com/gen2brain/h265/hevc"
)

// samples is what a plane holds either side of eight bits.
type samples interface{ ~uint8 | ~uint16 }

// gridSource is the converted picture a grid is cut from.
type gridSource struct {
	frame  hevc.Frame
	alpha  []uint8
	alpha6 []uint16
	stored image.Point
	sw, sh int
}

// picture writes one item, or a grid of tiles and the item assembling them.
func (f *heicFile) picture(src *gridSource, base hevc.EncoderOptions, tile int) error {
	if tile == 0 {
		return f.single(src, base)
	}

	cols := (src.stored.X + tile - 1) / tile
	rows := (src.stored.Y + tile - 1) / tile

	if rows > 256 || cols > 256 || rows*cols*2+2 > 0xffff {
		return ErrUnsupported
	}

	// 6.6.2.3.1 clips the tiles to the size the grid names, so all are equal.
	desc := []byte{0, 1, byte(rows - 1), byte(cols - 1)}
	desc = append(desc, u32(uint32(f.size.X))...)
	desc = append(desc, u32(uint32(f.size.Y))...)

	f.grid, f.tile = true, tile
	f.add(encItem{id: 1, typ: "grid", name: "grid", data: desc, ref: "dimg"}, nil)

	base.Width, base.Height = tile, tile

	for i := range rows * cols {
		x0, y0 := i%cols*tile, i/cols*tile

		sample, params, err := codeItem(base, src.tile(x0, y0, tile, false))
		if err != nil {
			return err
		}

		id := uint16(2 + i)
		f.items[0].to = append(f.items[0].to, id)
		f.add(encItem{id: id, typ: "hvc1", name: "tile", hidden: true,
			data: sample}, params)
	}

	if src.alpha == nil && src.alpha6 == nil {
		return nil
	}

	// Annex F ties the alpha to each tile rather than to the grid.
	alpha := base
	alpha.Chroma = hevc.ChromaMono

	for i := range rows * cols {
		x0, y0 := i%cols*tile, i/cols*tile

		sample, params, err := codeItem(alpha, src.tile(x0, y0, tile, true))
		if err != nil {
			return err
		}

		f.add(encItem{id: uint16(2 + rows*cols + i), typ: "hvc1", name: "alpha",
			hidden: true, mono: true, data: sample,
			ref: "auxl", to: []uint16{uint16(2 + i)}}, params)
	}

	return nil
}

// single writes the one item a picture that fits a level takes.
func (f *heicFile) single(src *gridSource, base hevc.EncoderOptions) error {
	base.Width, base.Height = src.stored.X, src.stored.Y

	sample, params, err := codeItem(base, src.frame)
	if err != nil {
		return err
	}

	f.add(encItem{id: 1, typ: "hvc1", name: "image", data: sample}, params)

	if src.alpha == nil && src.alpha6 == nil {
		return nil
	}

	alpha := base
	alpha.Chroma = hevc.ChromaMono

	sample, params, err = codeItem(alpha, hevc.Frame{
		Y: src.alpha, Y16: src.alpha6, StrideY: src.stored.X,
	})
	if err != nil {
		return err
	}

	f.add(encItem{id: 2, typ: "hvc1", name: "alpha", hidden: true, mono: true,
		data: sample, ref: "auxl", to: []uint16{1}}, params)

	return nil
}

// tile cuts one tile out, repeating the edge where the grid reaches past it.
func (g *gridSource) tile(x0, y0, n int, alpha bool) hevc.Frame {
	if alpha {
		if g.alpha6 != nil {
			return hevc.Frame{StrideY: n,
				Y16: cutTile(g.alpha6, g.stored.X, g.stored.X, g.stored.Y, x0, y0, n, n)}
		}

		return hevc.Frame{StrideY: n,
			Y: cutTile(g.alpha, g.stored.X, g.stored.X, g.stored.Y, x0, y0, n, n)}
	}

	f := g.frame
	w, h := g.stored.X, g.stored.Y
	cw, ch := w/g.sw, h/g.sh
	cn := n / g.sw
	cnh := n / g.sh

	if f.Y16 != nil {
		out := hevc.Frame{StrideY: n,
			Y16: cutTile(f.Y16, f.StrideY, w, h, x0, y0, n, n)}

		if f.Cb16 != nil {
			out.StrideC = cn
			out.Cb16 = cutTile(f.Cb16, f.StrideC, cw, ch, x0/g.sw, y0/g.sh, cn, cnh)
			out.Cr16 = cutTile(f.Cr16, f.StrideC, cw, ch, x0/g.sw, y0/g.sh, cn, cnh)
		}

		return out
	}

	out := hevc.Frame{StrideY: n, Y: cutTile(f.Y, f.StrideY, w, h, x0, y0, n, n)}

	if f.Cb != nil {
		out.StrideC = cn
		out.Cb = cutTile(f.Cb, f.StrideC, cw, ch, x0/g.sw, y0/g.sh, cn, cnh)
		out.Cr = cutTile(f.Cr, f.StrideC, cw, ch, x0/g.sw, y0/g.sh, cn, cnh)
	}

	return out
}

// cutTile copies a tw by th rectangle out of a plane, clamping to its edge.
func cutTile[P samples](src []P, stride, w, h, x0, y0, tw, th int) []P {
	out := make([]P, tw*th)

	for y := range th {
		row := src[min(y0+y, h-1)*stride:]
		dst := out[y*tw:]

		n := min(tw, w-x0)
		if n > 0 {
			copy(dst[:n], row[x0:x0+n])
		} else {
			n = 0
		}

		for x := n; x < tw; x++ {
			dst[x] = row[w-1]
		}
	}

	return out
}
