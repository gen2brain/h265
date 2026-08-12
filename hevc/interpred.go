package hevc

// Table 8-11.
// lumaFilter16 is the same table at the width the kernels multiply in.
var lumaFilter16 = func() [4][8]int16 {
	var f [4][8]int16

	for i, row := range lumaFilter {
		for j, v := range row {
			f[i][j] = int16(v)
		}
	}

	return f
}()

var lumaFilter = [4][8]int32{
	{0, 0, 0, 64, 0, 0, 0, 0},
	{-1, 4, -10, 58, 17, -5, 1, 0},
	{-1, 4, -11, 40, 40, -11, 4, -1},
	{0, 1, -5, 17, 58, -10, 4, -1},
}

// Table 8-12.
// chromaFilter16 is the same table at the width the kernels multiply in.
var chromaFilter16 = func() [8][4]int16 {
	var f [8][4]int16

	for i, row := range chromaFilter {
		for j, v := range row {
			f[i][j] = int16(v)
		}
	}

	return f
}()

var chromaFilter = [8][4]int32{
	{0, 64, 0, 0},
	{-2, 58, 10, -2},
	{-4, 54, 16, -2},
	{-6, 46, 28, -4},
	{-4, 36, 36, -4},
	{-4, 28, 46, -6},
	{-2, 16, 54, -4},
	{-2, 10, 58, -2},
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}

	if v > hi {
		return hi
	}

	return v
}

// mcLuma is the luma sample interpolation of 8.5.3.3.3.1. It writes the
// 14-bit intermediate the weighted prediction process consumes.
func mcLuma[P pixel](dst []int16, dstStride int, src []P, srcStride, picW, picH,
	x, y, xFrac, yFrac, w, h, bitDepth int, scratch []int32, tmp16 []int16, pad []P,
) {
	shift1 := min(4, bitDepth-8)
	shift2 := 6
	shift3 := max(2, 14-bitDepth)

	// The eight-tap support reaches three samples back and four forward. When
	// it leaves the picture the region is copied out once with the edge
	// clamping of 8.5.3.3.3, so the filters below index the source directly.
	ox, ew := 0, w
	if xFrac != 0 {
		ox, ew = -3, w+7
	}

	oy, eh := 0, h
	if yFrac != 0 {
		oy, eh = -3, h+7
	}

	if x+ox < 0 || y+oy < 0 || x+ox+ew > picW || y+oy+eh > picH {
		emulate(pad, src, srcStride, picW, picH, x+ox, y+oy, ew, eh)

		src, srcStride = pad, ew
		x, y = -ox, -oy
	}

	at := func(px, py int) int32 {
		return int32(src[py*srcStride+px])
	}

	// The kernels cover eight-bit samples at widths they can step through
	// whole; anything else falls to the loops below.
	asm := mcTapAsm
	if bitDepth != 8 || w%8 != 0 {
		asm = nil
	}

	p8, _ := any(src).([]uint8)
	if p8 == nil {
		asm = nil
	}

	switch {
	case xFrac == 0 && yFrac == 0:
		if p8 != nil && w%8 == 0 && mcCopyAsm != nil {
			mcCopyAsm(dst, dstStride, p8[y*srcStride+x:], srcStride, w, h, shift3)

			return
		}

		for j := range h {
			for i := range w {
				dst[j*dstStride+i] = int16(at(x+i, y+j) << shift3)
			}
		}

	case yFrac == 0:
		if asm != nil {
			asm(dst, dstStride, p8[y*srcStride+x-3:], srcStride, 1, w, h,
				lumaFilter16[xFrac][:])

			return
		}

		f := &lumaFilter[xFrac]

		for j := range h {
			for i := range w {
				var v int32
				for k := range 8 {
					v += f[k] * at(x+i+k-3, y+j)
				}

				dst[j*dstStride+i] = int16(v >> shift1)
			}
		}

	case xFrac == 0:
		if asm != nil {
			asm(dst, dstStride, p8[(y-3)*srcStride+x:], srcStride, srcStride, w, h,
				lumaFilter16[yFrac][:])

			return
		}

		f := &lumaFilter[yFrac]

		for j := range h {
			for i := range w {
				var v int32
				for k := range 8 {
					v += f[k] * at(x+i, y+j+k-3)
				}

				dst[j*dstStride+i] = int16(v >> shift1)
			}
		}

	default:
		if asm != nil && mcTapV16Asm != nil {
			t := tmp16[:(h+7)*w]

			asm(t, w, p8[(y-3)*srcStride+x-3:], srcStride, 1, w, h+7,
				lumaFilter16[xFrac][:])
			mcTapV16Asm(dst, dstStride, t, w, w, h, shift2, lumaFilter16[yFrac][:])

			return
		}

		fx, fy := &lumaFilter[xFrac], &lumaFilter[yFrac]

		tmp := scratch[:(h+7)*w]

		for j := range h + 7 {
			for i := range w {
				var v int32
				for k := range 8 {
					v += fx[k] * at(x+i+k-3, y+j-3)
				}

				tmp[j*w+i] = v >> shift1
			}
		}

		for j := range h {
			for i := range w {
				var v int32
				for k := range 8 {
					v += fy[k] * tmp[(j+k)*w+i]
				}

				dst[j*dstStride+i] = int16(v >> shift2)
			}
		}
	}
}

// mcChroma is the chroma sample interpolation of 8.5.3.3.3.2.
func mcChroma[P pixel](dst []int16, dstStride int, src []P, srcStride, picW, picH,
	x, y, xFrac, yFrac, w, h, bitDepth int, scratch []int32, tmp16 []int16, pad []P,
) {
	shift1 := min(4, bitDepth-8)
	shift2 := 6
	shift3 := max(2, 14-bitDepth)

	// The four-tap support reaches one sample back and two forward.
	ox, ew := 0, w
	if xFrac != 0 {
		ox, ew = -1, w+3
	}

	oy, eh := 0, h
	if yFrac != 0 {
		oy, eh = -1, h+3
	}

	if x+ox < 0 || y+oy < 0 || x+ox+ew > picW || y+oy+eh > picH {
		emulate(pad, src, srcStride, picW, picH, x+ox, y+oy, ew, eh)

		src, srcStride = pad, ew
		x, y = -ox, -oy
	}

	at := func(px, py int) int32 {
		return int32(src[py*srcStride+px])
	}

	p8, _ := any(src).([]uint8)

	asm := mcTapAsm
	if bitDepth != 8 || w%8 != 0 || p8 == nil {
		asm = nil
	}

	switch {
	case xFrac == 0 && yFrac == 0:
		if p8 != nil && w%8 == 0 && bitDepth == 8 && mcCopyAsm != nil {
			mcCopyAsm(dst, dstStride, p8[y*srcStride+x:], srcStride, w, h, shift3)

			return
		}

		for j := range h {
			for i := range w {
				dst[j*dstStride+i] = int16(at(x+i, y+j) << shift3)
			}
		}

	case yFrac == 0:
		if asm != nil {
			asm(dst, dstStride, p8[y*srcStride+x-1:], srcStride, 1, w, h,
				chromaFilter16[xFrac][:])

			return
		}

		f := &chromaFilter[xFrac]

		for j := range h {
			for i := range w {
				var v int32
				for k := range 4 {
					v += f[k] * at(x+i+k-1, y+j)
				}

				dst[j*dstStride+i] = int16(v >> shift1)
			}
		}

	case xFrac == 0:
		if asm != nil {
			asm(dst, dstStride, p8[(y-1)*srcStride+x:], srcStride, srcStride, w, h,
				chromaFilter16[yFrac][:])

			return
		}

		f := &chromaFilter[yFrac]

		for j := range h {
			for i := range w {
				var v int32
				for k := range 4 {
					v += f[k] * at(x+i, y+j+k-1)
				}

				dst[j*dstStride+i] = int16(v >> shift1)
			}
		}

	default:
		if asm != nil && mcTapV16Asm != nil {
			t := tmp16[:(h+3)*w]

			asm(t, w, p8[(y-1)*srcStride+x-1:], srcStride, 1, w, h+3,
				chromaFilter16[xFrac][:])
			mcTapV16Asm(dst, dstStride, t, w, w, h, shift2, chromaFilter16[yFrac][:])

			return
		}

		fx, fy := &chromaFilter[xFrac], &chromaFilter[yFrac]

		tmp := scratch[:(h+3)*w]

		for j := range h + 3 {
			for i := range w {
				var v int32
				for k := range 4 {
					v += fx[k] * at(x+i+k-1, y+j-1)
				}

				tmp[j*w+i] = v >> shift1
			}
		}

		for j := range h {
			for i := range w {
				var v int32
				for k := range 4 {
					v += fy[k] * tmp[(j+k)*w+i]
				}

				dst[j*dstStride+i] = int16(v >> shift2)
			}
		}
	}
}

// emulate copies a region out of a reference picture, repeating the edge
// samples where it falls outside, so a filter reading it needs no bounds
// check.
func emulate[P pixel](dst []P, src []P, srcStride, picW, picH, x, y, w, h int) {
	for j := range h {
		sy := clampInt(y+j, 0, picH-1)
		row := src[sy*srcStride:]
		out := dst[j*w : j*w+w]

		// The inside part is a copy; only the overhang repeats.
		lo := clampInt(-x, 0, w)
		hi := clampInt(picW-x, 0, w)

		for i := range lo {
			out[i] = row[0]
		}

		if hi > lo {
			copy(out[lo:hi], row[x+lo:x+hi])
		}

		for i := hi; i < w; i++ {
			out[i] = row[picW-1]
		}
	}
}

// predUni is the default uni-prediction of 8.5.3.3.4.2.
func predUni[P pixel](dst []P, dstOff, dstStride int, src []int16, srcStride, w, h, bitDepth int) {
	shift := max(2, 14-bitDepth)

	if k := predUniAsm; k != nil && bitDepth == 8 {
		if p, ok := any(dst).([]uint8); ok {
			if n := w &^ 7; n != 0 {
				k(p[dstOff:], dstStride, src, srcStride, n, h, shift)

				if n == w {
					return
				}

				dstOff += n
				src = src[n:]
				w -= n
			}
		}
	}

	predUniGo(dst, dstOff, dstStride, src, srcStride, w, h, bitDepth, shift)
}

func predUniGo[P pixel](dst []P, dstOff, dstStride int, src []int16, srcStride, w, h, bitDepth, shift int) {
	off := int32(1) << (shift - 1)
	maxV := int32(1)<<bitDepth - 1

	for j := range h {
		for i := range w {
			v := (int32(src[j*srcStride+i]) + off) >> shift
			dst[dstOff+j*dstStride+i] = P(clip3(v, 0, maxV))
		}
	}
}

// predBi is the default bi-prediction of 8.5.3.3.4.2.
func predBi[P pixel](dst []P, dstOff, dstStride int, a, b []int16, srcStride, w, h, bitDepth int) {
	shift := max(2, 14-bitDepth) + 1

	if k := predBiAsm; k != nil && bitDepth == 8 {
		if p, ok := any(dst).([]uint8); ok {
			if n := w &^ 7; n != 0 {
				k(p[dstOff:], dstStride, a, b, srcStride, n, h, shift)

				if n == w {
					return
				}

				dstOff += n
				a, b = a[n:], b[n:]
				w -= n
			}
		}
	}

	predBiGo(dst, dstOff, dstStride, a, b, srcStride, w, h, bitDepth, shift)
}

func predBiGo[P pixel](dst []P, dstOff, dstStride int, a, b []int16, srcStride, w, h,
	bitDepth, shift int,
) {
	off := int32(1) << (shift - 1)
	maxV := int32(1)<<bitDepth - 1

	for j := range h {
		for i := range w {
			v := (int32(a[j*srcStride+i]) + int32(b[j*srcStride+i]) + off) >> shift
			dst[dstOff+j*dstStride+i] = P(clip3(v, 0, maxV))
		}
	}
}

// weightUni is the explicit weighted uni-prediction of 8.5.3.3.4.3.
func weightUni[P pixel](dst []P, dstOff, dstStride int, src []int16, srcStride,
	w, h, weight, offset, denom, bitDepth int, highPrecision bool,
) {
	shift1 := max(2, 14-bitDepth)
	log2Wd := denom + shift1
	maxV := int32(1)<<bitDepth - 1

	o := int32(offset)
	if !highPrecision {
		o <<= bitDepth - 8
	}

	for j := range h {
		for i := range w {
			v := int32(src[j*srcStride+i])

			if log2Wd >= 1 {
				v = ((v*int32(weight) + 1<<(log2Wd-1)) >> log2Wd) + o
			} else {
				v = v*int32(weight) + o
			}

			dst[dstOff+j*dstStride+i] = P(clip3(v, 0, maxV))
		}
	}
}

// weightBi is the explicit weighted bi-prediction of 8.5.3.3.4.3.
func weightBi[P pixel](dst []P, dstOff, dstStride int, a, b []int16, srcStride,
	w, h, w0, w1, o0, o1, denom, bitDepth int, highPrecision bool,
) {
	shift1 := max(2, 14-bitDepth)
	log2Wd := denom + shift1
	maxV := int32(1)<<bitDepth - 1

	oa, ob := int32(o0), int32(o1)
	if !highPrecision {
		oa <<= bitDepth - 8
		ob <<= bitDepth - 8
	}

	for j := range h {
		for i := range w {
			v := int32(a[j*srcStride+i])*int32(w0) + int32(b[j*srcStride+i])*int32(w1) +
				(oa+ob+1)<<log2Wd

			dst[dstOff+j*dstStride+i] = P(clip3(v>>(log2Wd+1), 0, maxV))
		}
	}
}
