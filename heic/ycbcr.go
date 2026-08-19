package heic

import "image"

// toYCbCr converts img to the 8-bit planes the encoder codes at the given
// chroma sampling, at a size that sampling resolves: a dimension the chroma
// cannot divide gains a column or row that repeats its edge, which the ispe
// property then hides. Chroma is the average of each group. Alpha is
// composited over black unless keepAlpha, which leaves the colour for an
// auxiliary item to be read against.
func toYCbCr(img image.Image, sub image.YCbCrSubsampleRatio, keepAlpha bool) *image.YCbCr {
	b := img.Bounds()
	sw, sh := ratioSub(sub)
	even := image.Rect(0, 0, roundUp(b.Dx(), sw), roundUp(b.Dy(), sh))

	if ycc, ok := img.(*image.YCbCr); ok {
		if ycc.SubsampleRatio == sub && ycc.Rect == even {
			return ycc
		}

		return ycbcrTo(ycc, even, sub)
	}

	if a, ok := img.(*image.NYCbCrA); ok {
		return ycbcrTo(&a.YCbCr, even, sub)
	}

	return rgbTo(img, even, sub, keepAlpha)
}

// toYCbCr420 is toYCbCr at the sampling this package has always written.
func toYCbCr420(img image.Image, keepAlpha bool) *image.YCbCr {
	return toYCbCr(img, image.YCbCrSubsampleRatio420, keepAlpha)
}

// toGray is the luma plane on its own, for a picture coded without chroma.
func toGray(img image.Image, keepAlpha bool) []byte {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	out := make([]byte, w*h)

	switch src := img.(type) {
	case *image.Gray:
		for y := range h {
			copy(out[y*w:], src.Pix[src.PixOffset(b.Min.X, b.Min.Y+y):][:w])
		}

		return out
	case *image.YCbCr:
		for y := range h {
			copy(out[y*w:], src.Y[src.YOffset(b.Min.X, b.Min.Y+y):][:w])
		}

		return out
	}

	buf := make([]byte, 4*w)

	for y := range h {
		px := readRow(img, b, y, buf, keepAlpha)

		for x := range w {
			p := px[4*x:]
			out[y*w+x], _, _ = rgbToYCbCr(p[0], p[1], p[2])
		}
	}

	return out
}

// toDeep converts img to sixteen bit planes at a sampling and a sample size,
// the way toYCbCr does at eight. The colour comes through image.Color, which
// hands back sixteen bits whatever the source holds.
func toDeep(img image.Image, sub image.YCbCrSubsampleRatio, depth int,
	keepAlpha bool,
) (even image.Rectangle, y, cb, cr []uint16) {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	sw, sh := ratioSub(sub)
	even = image.Rect(0, 0, roundUp(w, sw), roundUp(h, sh))

	y = make([]uint16, even.Dx()*even.Dy())
	cb = make([]uint16, even.Dx()/sw*even.Dy()/sh)
	cr = make([]uint16, len(cb))

	for cy := range even.Dy() / sh {
		for cx := range even.Dx() / sw {
			var sumCb, sumCr, n int32

			for j := range sh {
				for i := range sw {
					px, py := min(cx*sw+i, w-1), min(cy*sh+j, h-1)
					r, g, bl, a := img.At(b.Min.X+px, b.Min.Y+py).RGBA()

					if keepAlpha {
						r, g, bl = unpremul16(r, a), unpremul16(g, a), unpremul16(bl, a)
					}

					l, pcb, pcr := rgbToYCbCrDeep(r, g, bl, depth)
					y[(cy*sh+j)*even.Dx()+cx*sw+i] = l
					sumCb, sumCr, n = sumCb+int32(pcb), sumCr+int32(pcr), n+1
				}
			}

			cb[cy*(even.Dx()/sw)+cx] = uint16((sumCb + n/2) / n)
			cr[cy*(even.Dx()/sw)+cx] = uint16((sumCr + n/2) / n)
		}
	}

	return even, y, cb, cr
}

// alphaDeep is alphaPlane at a sample size above eight.
func alphaDeep(img image.Image, even image.Rectangle, depth int) []uint16 {
	if o, ok := img.(interface{ Opaque() bool }); ok && o.Opaque() {
		return nil
	}

	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	out := make([]uint16, even.Dx()*even.Dy())
	top := uint16(1<<depth - 1)
	opaque := true

	for j := range even.Dy() {
		for i := range even.Dx() {
			_, _, _, a := img.At(b.Min.X+min(i, w-1), b.Min.Y+min(j, h-1)).RGBA()

			v := uint16(a >> (16 - depth))
			out[j*even.Dx()+i] = v

			if v != top {
				opaque = false
			}
		}
	}

	if opaque {
		return nil
	}

	return out
}

// rgbToYCbCrDeep is the full range BT.601 matrix at a sample size, taking the
// sixteen bit components image.Color hands back.
func rgbToYCbCrDeep(r, g, b uint32, depth int) (uint16, uint16, uint16) {
	sh := 16 - depth
	top := int32(1)<<depth - 1
	half := int32(1) << (depth - 1)
	r1, g1, b1 := int32(r>>sh), int32(g>>sh), int32(b>>sh)

	y := (19595*r1 + 38470*g1 + 7471*b1 + 1<<15) >> 16
	cb := (-11056*r1-21712*g1+32768*b1+1<<15)>>16 + half
	cr := (32768*r1-27440*g1-5328*b1+1<<15)>>16 + half

	return uint16(min(max(y, 0), top)), uint16(min(max(cb, 0), top)),
		uint16(min(max(cr, 0), top))
}

func unpremul16(c, a uint32) uint32 {
	if a == 0 {
		return 0
	}

	return min(c*0xffff/a, 0xffff)
}

// ratioSub is SubWidthC and SubHeightC for the samplings this package writes.
func ratioSub(sub image.YCbCrSubsampleRatio) (int, int) {
	switch sub {
	case image.YCbCrSubsampleRatio444:
		return 1, 1
	case image.YCbCrSubsampleRatio422:
		return 2, 1
	default:
		return 2, 2
	}
}

func roundUp(n, to int) int {
	return (n + to - 1) / to * to
}

// alphaPlane is the alpha of img at luma resolution, padded the way the luma
// is, or nil for a picture that is opaque and needs no auxiliary item.
func alphaPlane(img image.Image, even image.Rectangle) []uint8 {
	if o, ok := img.(interface{ Opaque() bool }); ok && o.Opaque() {
		return nil
	}

	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	out := make([]uint8, even.Dx()*even.Dy())
	opaque := true

	for y := range even.Dy() {
		sy := b.Min.Y + min(y, h-1)
		row := out[y*even.Dx():][:even.Dx()]

		alphaRow(img, b.Min.X, sy, w, row)
		fillEdge(row, w)

		for _, a := range row {
			if a != 0xff {
				opaque = false

				break
			}
		}
	}

	if opaque {
		return nil
	}

	return out
}

func alphaRow(img image.Image, x, y, w int, dst []uint8) {
	switch src := img.(type) {
	case *image.NRGBA:
		p := src.Pix[src.PixOffset(x, y):]

		for i := range w {
			dst[i] = p[4*i+3]
		}
	case *image.RGBA:
		p := src.Pix[src.PixOffset(x, y):]

		for i := range w {
			dst[i] = p[4*i+3]
		}
	case *image.NYCbCrA:
		p := src.A[src.AOffset(x, y):]

		copy(dst[:w], p[:w])
	default:
		for i := range w {
			_, _, _, a := img.At(x+i, y).RGBA()
			dst[i] = uint8(a >> 8)
		}
	}
}

// ycbcrTo resamples any subsampling to another through COffset, which resolves
// the source ratio for us, so a matching one copies and the rest average.
func ycbcrTo(src *image.YCbCr, even image.Rectangle, sub image.YCbCrSubsampleRatio) *image.YCbCr {
	out := image.NewYCbCr(even, sub)
	sw, sh := ratioSub(sub)
	b := src.Rect
	w, h := b.Dx(), b.Dy()

	for y := range even.Dy() {
		row := out.Y[y*out.YStride:][:even.Dx()]
		copy(row, src.Y[src.YOffset(b.Min.X, b.Min.Y+min(y, h-1)):][:w])
		fillEdge(row, w)
	}

	for cy := range even.Dy() / sh {
		for cx := range even.Dx() / sw {
			var cb, cr, n int32

			for j := range sh {
				y := b.Min.Y + min(cy*sh+j, h-1)

				for i := range sw {
					o := src.COffset(b.Min.X+min(cx*sw+i, w-1), y)
					cb, cr, n = cb+int32(src.Cb[o]), cr+int32(src.Cr[o]), n+1
				}
			}

			out.Cb[cy*out.CStride+cx] = byte((cb + n/2) / n)
			out.Cr[cy*out.CStride+cx] = byte((cr + n/2) / n)
		}
	}

	return out
}

func rgbTo(img image.Image, even image.Rectangle, sub image.YCbCrSubsampleRatio,
	keepAlpha bool,
) *image.YCbCr {
	out := image.NewYCbCr(even, sub)
	sw, sh := ratioSub(sub)
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()

	rows := make([][]byte, sh)
	px := make([][]byte, sh)

	for j := range rows {
		rows[j] = make([]byte, 4*w)
	}

	for y := 0; y < even.Dy(); y += sh {
		for j := range sh {
			px[j] = readRow(img, b, min(y+j, h-1), rows[j], keepAlpha)
		}

		for x := 0; x < even.Dx(); x += sw {
			var cb, cr, n int32

			for j := range sh {
				row := out.Y[(y+j)*out.YStride:]

				for i := range sw {
					p := px[j][4*min(x+i, w-1):]
					l, pcb, pcr := rgbToYCbCr(p[0], p[1], p[2])
					row[x+i] = l
					cb, cr, n = cb+int32(pcb), cr+int32(pcr), n+1
				}
			}

			out.Cb[y/sh*out.CStride+x/sw] = byte((cb + n/2) / n)
			out.Cr[y/sh*out.CStride+x/sw] = byte((cr + n/2) / n)
		}
	}

	return out
}

func fillEdge(row []byte, w int) {
	for i := w; i < len(row); i++ {
		row[i] = row[w-1]
	}
}

// readRow returns row y of img as RGB samples with a four byte pixel stride. Composited over black, unless keepAlpha
// asks for the colour an alpha item will be read against, which is what a
// non-premultiplied source already holds.
func readRow(img image.Image, b image.Rectangle, y int, dst []byte, keepAlpha bool) []byte {
	w := b.Dx()

	switch src := img.(type) {
	case *image.RGBA:
		p := src.Pix[src.PixOffset(b.Min.X, b.Min.Y+y):]

		if !keepAlpha {
			return p
		}

		for x, o := 0, 0; x < w; x, o = x+1, o+4 {
			a := uint32(p[o+3])

			for c := range 3 {
				dst[o+c] = unpremul(uint32(p[o+c]), a)
			}
		}
	case *image.NRGBA:
		p := src.Pix[src.PixOffset(b.Min.X, b.Min.Y+y):]

		if keepAlpha {
			return p
		}

		for x, o := 0, 0; x < w; x, o = x+1, o+4 {
			a := uint32(p[o+3])
			dst[o] = byte(uint32(p[o]) * a / 255)
			dst[o+1] = byte(uint32(p[o+1]) * a / 255)
			dst[o+2] = byte(uint32(p[o+2]) * a / 255)
		}
	case *image.Gray:
		p := src.Pix[src.PixOffset(b.Min.X, b.Min.Y+y):]

		for x, o := 0, 0; x < w; x, o = x+1, o+4 {
			v := p[x]
			dst[o], dst[o+1], dst[o+2] = v, v, v
		}
	default:
		for x, o := 0, 0; x < w; x, o = x+1, o+4 {
			r, g, bl, a := img.At(b.Min.X+x, b.Min.Y+y).RGBA()

			if keepAlpha {
				dst[o], dst[o+1], dst[o+2] = unpremul(r, a), unpremul(g, a), unpremul(bl, a)

				continue
			}

			dst[o], dst[o+1], dst[o+2] = byte(r>>8), byte(g>>8), byte(bl>>8)
		}
	}

	return dst
}

// unpremul takes a premultiplied component back to the colour an alpha channel
// is read against. Where nothing is left of the alpha, neither is the colour.
func unpremul(c, a uint32) byte {
	if a == 0 {
		return 0
	}

	return byte(min(c*0xffff/a, 0xffff) >> 8)
}

// rgbToYCbCr is the full range BT.601 matrix, which is what image.YCbCr holds
// and what the nclx description this package writes declares.
func rgbToYCbCr(r, g, b byte) (byte, byte, byte) {
	r1, g1, b1 := int32(r), int32(g), int32(b)

	y := (19595*r1 + 38470*g1 + 7471*b1 + 1<<15) >> 16
	cb := (-11056*r1 - 21712*g1 + 32768*b1 + 257<<15) >> 16
	cr := (32768*r1 - 27440*g1 - 5328*b1 + 257<<15) >> 16

	return byte(y), byte(min(max(cb, 0), 255)), byte(min(max(cr, 0), 255))
}
