package hevc

// Table 8-11.
var lumaFilter = [4][8]int32{
	{0, 0, 0, 64, 0, 0, 0, 0},
	{-1, 4, -10, 58, 17, -5, 1, 0},
	{-1, 4, -11, 40, 40, -11, 4, -1},
	{0, 1, -5, 17, 58, -10, 4, -1},
}

// Table 8-12.
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
// 14-bit intermediate the weighted prediction process consumes, and clamps
// reference reads to the picture as the spec's Clip3 on the coordinates does.
func mcLuma[P pixel](dst []int16, dstStride int, src []P, srcStride, picW, picH,
	x, y, xFrac, yFrac, w, h, bitDepth int, scratch []int32,
) {
	shift1 := bitDepth - 8
	shift2 := 6
	shift3 := 14 - bitDepth

	at := func(px, py int) int32 {
		px = clampInt(px, 0, picW-1)
		py = clampInt(py, 0, picH-1)

		return int32(src[py*srcStride+px])
	}

	switch {
	case xFrac == 0 && yFrac == 0:
		for j := range h {
			for i := range w {
				dst[j*dstStride+i] = int16(at(x+i, y+j) << shift3)
			}
		}

	case yFrac == 0:
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
	x, y, xFrac, yFrac, w, h, bitDepth int, scratch []int32,
) {
	shift1 := bitDepth - 8
	shift2 := 6
	shift3 := 14 - bitDepth

	at := func(px, py int) int32 {
		px = clampInt(px, 0, picW-1)
		py = clampInt(py, 0, picH-1)

		return int32(src[py*srcStride+px])
	}

	switch {
	case xFrac == 0 && yFrac == 0:
		for j := range h {
			for i := range w {
				dst[j*dstStride+i] = int16(at(x+i, y+j) << shift3)
			}
		}

	case yFrac == 0:
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

// predUni is the default uni-prediction of 8.5.3.3.4.2.
func predUni[P pixel](dst []P, dstOff, dstStride int, src []int16, srcStride, w, h, bitDepth int) {
	shift := 14 - bitDepth

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
	shift := 15 - bitDepth
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
	shift1 := 14 - bitDepth
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
	shift1 := 14 - bitDepth
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
