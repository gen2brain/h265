package heic

import (
	"encoding/binary"
	"testing"
)

func rowGen(iter, kind, i int) uint32 {
	v := uint32(i)*2654435761 + uint32(kind)*2246822519 + uint32(iter)*3266489917
	v ^= v >> 15

	return v * 2654435761
}

func TestConvertRowAsm(t *testing.T) {
	matrices := []int{mcBT601, mcBT709, mcBT2020NCL}
	cases := 0

	for iter := range 4 {
		for _, ssHor := range []int{0, 1} {
			asm, ref := rowFn(ssHor), convertRowGo
			if ssHor == 0 {
				ref = convertRow444Go
			}
			for _, matrix := range matrices {
				full := iter&1 != 0
				s := &colorState{matrix: matrix, fullRange: full, depth: 8, ssHor: ssHor}
				s.maxChannel = 255
				s.outMax = 255
				s.kr, s.kg, s.kb = yuvCoefficients(matrix)
				s.crCoef, s.cbCoef = 2*(1-s.kr), 2*(1-s.kb)
				s.gcr, s.gcb = s.kr*(1-s.kr), s.kb*(1-s.kb)
				c := s.rowConsts()

				for n := 1; n <= 40; n++ {
					uw := (n + ssHor) >> ssHor
					yf := make([]float32, n)
					a := make([]uint8, n)
					for i := range yf {
						yf[i] = float32(rowGen(iter, 0, i)%1400)/1000 - 0.2
						a[i] = uint8(rowGen(iter, 1, i))
					}
					rows := make([][]float32, 4)
					for r := range rows {
						rows[r] = make([]float32, uw+8)
						for i := range rows[r] {
							rows[r][i] = float32(rowGen(iter, 2+r, i)%1200)/1000 - 0.6
						}
					}

					got := make([]uint8, 4*n+32)
					want := make([]uint8, 4*n+32)
					for i := range got {
						got[i] = uint8(rowGen(iter, 6, i))
						want[i] = got[i]
					}

					asm(got, yf, rows[0], rows[1], rows[2], rows[3], a, n, &c)
					ref(want, yf, rows[0], rows[1], rows[2], rows[3], a, n, &c)

					for i := range want {
						if got[i] == want[i] {
							continue
						}
						t.Fatalf("ssHor=%d matrix=%d full=%v n=%d: buf[%d] = %d, want %d",
							ssHor, matrix, full, n, i, got[i], want[i])
					}
					cases++
				}
			}
		}
	}

	t.Logf("%d cases", cases)
}

func TestConvertRow16Asm(t *testing.T) {
	matrices := []int{mcBT601, mcBT709, mcBT2020NCL}
	cases := 0

	for iter := range 4 {
		for _, ssHor := range []int{0, 1} {
			asm, ref := rowFn16(ssHor), convertRow16Go
			if ssHor == 0 {
				ref = convertRow16x444Go
			}
			for _, matrix := range matrices {
				full := iter&1 != 0
				for _, depth := range []int{10, 12} {
					s := &colorState{matrix: matrix, fullRange: full, depth: depth, ssHor: ssHor}
					s.maxChannel = 1<<depth - 1
					s.outMax = 65535
					s.kr, s.kg, s.kb = yuvCoefficients(matrix)
					s.crCoef, s.cbCoef = 2*(1-s.kr), 2*(1-s.kb)
					s.gcr, s.gcb = s.kr*(1-s.kr), s.kb*(1-s.kb)
					c := s.rowConsts()

					for n := 1; n <= 40; n++ {
						uw := (n + ssHor) >> ssHor
						yf := make([]float32, n)
						a := make([]uint16, n)
						for i := range yf {
							yf[i] = float32(rowGen(iter, 0, i)%1400)/1000 - 0.2
							a[i] = uint16(rowGen(iter, 1, i))
						}
						rows := make([][]float32, 4)
						for r := range rows {
							rows[r] = make([]float32, uw+8)
							for i := range rows[r] {
								rows[r][i] = float32(rowGen(iter, 2+r, i)%1200)/1000 - 0.6
							}
						}

						got := make([]uint8, 8*n+64)
						want := make([]uint8, 8*n+64)
						for i := range got {
							got[i] = uint8(rowGen(iter, 6, i))
							want[i] = got[i]
						}

						asm(got, yf, rows[0], rows[1], rows[2], rows[3], a, n, &c)
						ref(want, yf, rows[0], rows[1], rows[2], rows[3], a, n, &c)

						for i := range want {
							if got[i] == want[i] {
								continue
							}
							t.Fatalf("ssHor=%d matrix=%d full=%v depth=%d n=%d: buf[%d] = %d, want %d",
								ssHor, matrix, full, depth, n, i, got[i], want[i])
						}
						cases++
					}
				}
			}
		}
	}

	t.Logf("%d cases", cases)
}

func TestNormRowAsm(t *testing.T) {
	cases := 0

	for _, depth := range []int{8, 10, 12} {
		maxCh := uint16(1<<depth - 1)
		shift := depth - 8

		for _, full := range []bool{false, true} {
			biasY, rangeY := float32(0), float32(maxCh)
			if !full {
				biasY, rangeY = float32(int(16)<<shift), float32(int(219)<<shift)
			}
			biasUV, rangeUV := float32(int(1)<<(depth-1)), float32(maxCh)
			if !full {
				rangeUV = float32(int(224) << shift)
			}

			for _, p := range [][2]float32{{biasY, rangeY}, {biasUV, rangeUV}} {
				for n := 1; n <= 40; n++ {
					src := make([]uint8, 2*n)
					for i := range src {
						src[i] = uint8(rowGen(depth, n, i))
					}

					src16 := make([]uint16, n)
					for i := range src16 {
						src16[i] = binary.NativeEndian.Uint16(src[2*i:])
					}

					got, want := make([]float32, n), make([]float32, n)
					if depth == 8 {
						normRow(got, src, p[0], p[1])
						normRowGo(want, src, p[0], p[1])
					} else {
						normRow16(got, src16, maxCh, p[0], p[1])
						normRow16Go(want, src16, maxCh, p[0], p[1])
					}

					for i := range want {
						if got[i] != want[i] {
							t.Fatalf("depth=%d full=%v bias=%g rng=%g n=%d: [%d] = %v, want %v",
								depth, full, p[0], p[1], n, i, got[i], want[i])
						}
					}
					cases++
				}
			}
		}
	}

	t.Logf("%d cases", cases)
}
