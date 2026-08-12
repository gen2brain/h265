package hevc

import (
	"math/rand/v2"
	"testing"
)

// naiveMC transcribes 8.5.3.3.3 as a single accumulation per output sample,
// without the separable two-pass structure the decoder uses.
func naiveMC(dst []int16, dstStride int, src []uint8, srcStride, picW, picH,
	x, y, xFrac, yFrac, w, h, bitDepth, taps int,
) {
	shift1 := bitDepth - 8
	shift2 := 6
	shift3 := 14 - bitDepth

	half := taps/2 - 1

	at := func(px, py int) int32 {
		return int32(src[clampInt(py, 0, picH-1)*srcStride+clampInt(px, 0, picW-1)])
	}

	fx := func(k int) int32 {
		if taps == 8 {
			return lumaFilter[xFrac][k]
		}

		return chromaFilter[xFrac][k]
	}

	fy := func(k int) int32 {
		if taps == 8 {
			return lumaFilter[yFrac][k]
		}

		return chromaFilter[yFrac][k]
	}

	for j := range h {
		for i := range w {
			switch {
			case xFrac == 0 && yFrac == 0:
				dst[j*dstStride+i] = int16(at(x+i, y+j) << shift3)

			case yFrac == 0:
				var v int32
				for k := range taps {
					v += fx(k) * at(x+i+k-half, y+j)
				}

				dst[j*dstStride+i] = int16(v >> shift1)

			case xFrac == 0:
				var v int32
				for k := range taps {
					v += fy(k) * at(x+i, y+j+k-half)
				}

				dst[j*dstStride+i] = int16(v >> shift1)

			default:
				var v int32
				for m := range taps {
					var row int32
					for k := range taps {
						row += fx(k) * at(x+i+k-half, y+j+m-half)
					}

					v += fy(m) * (row >> shift1)
				}

				dst[j*dstStride+i] = int16(v >> shift2)
			}
		}
	}
}

func TestMCLuma(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))

	const picW, picH = 24, 24

	src := make([]uint8, picW*picH)
	for i := range src {
		src[i] = uint8(r.IntN(256))
	}

	for _, bitDepth := range []int{8, 10, 12} {
		for _, wh := range [][2]int{{4, 4}, {8, 4}, {8, 8}, {16, 12}} {
			w, h := wh[0], wh[1]

			for xFrac := range 4 {
				for yFrac := range 4 {
					for _, xy := range [][2]int{{4, 4}, {0, 0}, {picW - 2, picH - 2}} {
						got := make([]int16, w*h)
						want := make([]int16, w*h)

						mcLuma(got, w, src, picW, picW, picH,
							xy[0], xy[1], xFrac, yFrac, w, h, bitDepth, make([]int32, 64*80))
						naiveMC(want, w, src, picW, picW, picH,
							xy[0], xy[1], xFrac, yFrac, w, h, bitDepth, 8)

						for i := range got {
							if got[i] != want[i] {
								t.Fatalf("bd=%d %dx%d frac=%d,%d at %v: [%d] = %d, want %d",
									bitDepth, w, h, xFrac, yFrac, xy, i, got[i], want[i])
							}
						}
					}
				}
			}
		}
	}
}

func TestMCChroma(t *testing.T) {
	r := rand.New(rand.NewPCG(3, 4))

	const picW, picH = 20, 20

	src := make([]uint8, picW*picH)
	for i := range src {
		src[i] = uint8(r.IntN(256))
	}

	for _, bitDepth := range []int{8, 10} {
		for _, wh := range [][2]int{{2, 2}, {4, 4}, {8, 6}} {
			w, h := wh[0], wh[1]

			for xFrac := range 8 {
				for yFrac := range 8 {
					got := make([]int16, w*h)
					want := make([]int16, w*h)

					mcChroma(got, w, src, picW, picW, picH, 3, 3, xFrac, yFrac, w, h, bitDepth, make([]int32, 64*80))
					naiveMC(want, w, src, picW, picW, picH, 3, 3, xFrac, yFrac, w, h, bitDepth, 4)

					for i := range got {
						if got[i] != want[i] {
							t.Fatalf("bd=%d %dx%d frac=%d,%d: [%d] = %d, want %d",
								bitDepth, w, h, xFrac, yFrac, i, got[i], want[i])
						}
					}
				}
			}
		}
	}
}

// TestMCFilterTaps holds each filter to the normalisation the shifts assume.
func TestMCFilterTaps(t *testing.T) {
	for i, f := range lumaFilter {
		var sum int32
		for _, c := range f {
			sum += c
		}

		if sum != 64 {
			t.Fatalf("luma filter %d sums to %d, want 64", i, sum)
		}
	}

	for i, f := range chromaFilter {
		var sum int32
		for _, c := range f {
			sum += c
		}

		if sum != 64 {
			t.Fatalf("chroma filter %d sums to %d, want 64", i, sum)
		}
	}

	for k := range 8 {
		if lumaFilter[1][k] != lumaFilter[3][7-k] {
			t.Fatalf("luma quarter-pel filters are not mirrored at tap %d", k)
		}
	}

	for k := range 4 {
		for i := 1; i < 4; i++ {
			if chromaFilter[i][k] != chromaFilter[8-i][3-k] {
				t.Fatalf("chroma filters %d and %d are not mirrored at tap %d", i, 8-i, k)
			}
		}
	}
}

func TestPredUniBi(t *testing.T) {
	r := rand.New(rand.NewPCG(5, 6))

	for _, bitDepth := range []int{8, 10, 12} {
		a := make([]int16, 64)
		b := make([]int16, 64)

		for i := range a {
			a[i] = int16(r.IntN(1 << 15))
			b[i] = int16(r.IntN(1 << 15))
		}

		dst := make([]uint16, 64)
		predUni(dst, 0, 8, a, 8, 8, 8, bitDepth)

		shift := 14 - bitDepth
		maxV := int32(1)<<bitDepth - 1

		for i := range dst {
			want := clip3((int32(a[i])+1<<(shift-1))>>shift, 0, maxV)
			if int32(dst[i]) != want {
				t.Fatalf("uni bd=%d [%d] = %d, want %d", bitDepth, i, dst[i], want)
			}
		}

		predBi(dst, 0, 8, a, b, 8, 8, 8, bitDepth)

		shift = 15 - bitDepth

		for i := range dst {
			want := clip3((int32(a[i])+int32(b[i])+1<<(shift-1))>>shift, 0, maxV)
			if int32(dst[i]) != want {
				t.Fatalf("bi bd=%d [%d] = %d, want %d", bitDepth, i, dst[i], want)
			}
		}
	}
}

func TestWeightedPrediction(t *testing.T) {
	r := rand.New(rand.NewPCG(7, 8))

	for _, bitDepth := range []int{8, 10} {
		for _, denom := range []int{0, 1, 6, 7} {
			a := make([]int16, 64)
			b := make([]int16, 64)

			for i := range a {
				a[i] = int16(r.IntN(1 << 14))
				b[i] = int16(r.IntN(1 << 14))
			}

			w0, w1 := 1<<denom+3, 1<<denom-5
			o0, o1 := 7, -11

			shift1 := 14 - bitDepth
			log2Wd := denom + shift1
			maxV := int32(1)<<bitDepth - 1

			dst := make([]uint16, 64)
			weightUni(dst, 0, 8, a, 8, 8, 8, w0, o0, denom, bitDepth, false)

			for i := range dst {
				o := int32(o0) << (bitDepth - 8)
				v := (int32(a[i])*int32(w0) + 1<<(log2Wd-1)) >> log2Wd
				want := clip3(v+o, 0, maxV)

				if int32(dst[i]) != want {
					t.Fatalf("uni bd=%d denom=%d [%d] = %d, want %d",
						bitDepth, denom, i, dst[i], want)
				}
			}

			weightBi(dst, 0, 8, a, b, 8, 8, 8, w0, w1, o0, o1, denom, bitDepth, false)

			for i := range dst {
				oa := int32(o0) << (bitDepth - 8)
				ob := int32(o1) << (bitDepth - 8)
				v := int32(a[i])*int32(w0) + int32(b[i])*int32(w1) + (oa+ob+1)<<log2Wd
				want := clip3(v>>(log2Wd+1), 0, maxV)

				if int32(dst[i]) != want {
					t.Fatalf("bi bd=%d denom=%d [%d] = %d, want %d",
						bitDepth, denom, i, dst[i], want)
				}
			}
		}
	}
}
