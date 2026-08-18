package heic

import "image"

// toYCbCr420 converts img to the 8-bit 4:2:0 planes the encoder codes, at a
// size the chroma resolves: an odd dimension gains a column or row that repeats
// its edge, which the ispe property then hides. Chroma is the average of each
// 2x2 group and alpha is composited over black.
func toYCbCr420(img image.Image) *image.YCbCr {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	even := image.Rect(0, 0, w+w&1, h+h&1)

	if ycc, ok := img.(*image.YCbCr); ok {
		if ycc.SubsampleRatio == image.YCbCrSubsampleRatio420 && ycc.Rect == even {
			return ycc
		}

		return ycbcrTo420(ycc, even)
	}

	if a, ok := img.(*image.NYCbCrA); ok {
		return ycbcrTo420(&a.YCbCr, even)
	}

	return rgbTo420(img, even)
}

// ycbcrTo420 resamples any subsampling to 4:2:0 through COffset, which resolves
// the source ratio for us, so 4:2:0 copies and everything else averages.
func ycbcrTo420(src *image.YCbCr, even image.Rectangle) *image.YCbCr {
	out := image.NewYCbCr(even, image.YCbCrSubsampleRatio420)
	b := src.Rect
	w, h := b.Dx(), b.Dy()

	for y := range even.Dy() {
		row := out.Y[y*out.YStride:][:even.Dx()]
		copy(row, src.Y[src.YOffset(b.Min.X, b.Min.Y+min(y, h-1)):][:w])
		fillEdge(row, w)
	}

	for cy := range (even.Dy() + 1) / 2 {
		for cx := range (even.Dx() + 1) / 2 {
			var cb, cr, n int32

			for j := range 2 {
				y := b.Min.Y + min(2*cy+j, h-1)

				for i := range 2 {
					o := src.COffset(b.Min.X+min(2*cx+i, w-1), y)
					cb, cr, n = cb+int32(src.Cb[o]), cr+int32(src.Cr[o]), n+1
				}
			}

			out.Cb[cy*out.CStride+cx] = byte((cb + n/2) / n)
			out.Cr[cy*out.CStride+cx] = byte((cr + n/2) / n)
		}
	}

	return out
}

func rgbTo420(img image.Image, even image.Rectangle) *image.YCbCr {
	out := image.NewYCbCr(even, image.YCbCrSubsampleRatio420)
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	rows := [2][]byte{make([]byte, 4*w), make([]byte, 4*w)}

	for y := 0; y < even.Dy(); y += 2 {
		var px [2][]byte
		px[0] = readRow(img, b, min(y, h-1), rows[0])
		px[1] = readRow(img, b, min(y+1, h-1), rows[1])

		for x := 0; x < even.Dx(); x += 2 {
			var cb, cr, n int32

			for j := range 2 {
				row := out.Y[(y+j)*out.YStride:]

				for i := range 2 {
					p := px[j][4*min(x+i, w-1):]
					l, pcb, pcr := rgbToYCbCr(p[0], p[1], p[2])
					row[x+i] = l
					cb, cr, n = cb+int32(pcb), cr+int32(pcr), n+1
				}
			}

			out.Cb[y/2*out.CStride+x/2] = byte((cb + n/2) / n)
			out.Cr[y/2*out.CStride+x/2] = byte((cr + n/2) / n)
		}
	}

	return out
}

func fillEdge(row []byte, w int) {
	for i := w; i < len(row); i++ {
		row[i] = row[w-1]
	}
}

// readRow returns row y of img as RGB samples with a four byte pixel stride.
func readRow(img image.Image, b image.Rectangle, y int, dst []byte) []byte {
	w := b.Dx()

	switch src := img.(type) {
	case *image.RGBA:
		return src.Pix[src.PixOffset(b.Min.X, b.Min.Y+y):]
	case *image.NRGBA:
		p := src.Pix[src.PixOffset(b.Min.X, b.Min.Y+y):]

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
			r, g, bl, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			dst[o], dst[o+1], dst[o+2] = byte(r>>8), byte(g>>8), byte(bl>>8)
		}
	}

	return dst
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
