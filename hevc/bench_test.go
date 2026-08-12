package hevc

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func benchStream(b *testing.B, name string) {
	b.Helper()

	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		b.Skip(err)
	}

	nals := SplitAnnexB(data)

	var pixels int64

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		var d Decoder

		for _, nal := range nals {
			out, err := d.DecodeNAL(nal)
			if err != nil {
				b.Fatal(err)
			}

			for _, p := range out {
				pixels += int64(p.Width * p.Height)
				p.Release()
			}
		}

		for _, p := range d.Flush() {
			pixels += int64(p.Width * p.Height)
			p.Release()
		}
	}

	b.StopTimer()

	if n := b.Elapsed().Seconds(); n > 0 {
		b.ReportMetric(float64(pixels)/n/1e6, "Mpixel/s")
	}
}

func BenchmarkDecodeIntra(b *testing.B) { benchStream(b, "realworld_320x240.h265") }
func BenchmarkDecodeInter(b *testing.B) { benchStream(b, "motion_320x240.h265") }
func BenchmarkDecode720p(b *testing.B)  { benchStream(b, "realworld_720p.h265") }
func BenchmarkDecode1080p(b *testing.B) { benchStream(b, "1080p.h265") }
func BenchmarkDecodeTiles(b *testing.B) { benchStream(b, "tiles.h265") }
func BenchmarkDecode10Bit(b *testing.B) { benchStream(b, "10bit_128x128.h265") }

func BenchmarkAddResidual(b *testing.B) {
	for _, n := range []int{8, 16, 32} {
		coef := make([]int32, n*n)
		for i := range coef {
			coef[i] = int32(i*7919%65536 - 32768)
		}

		plane := make([]uint8, n*n)

		b.Run(fmt.Sprintf("go/%dx%d", n, n), func(b *testing.B) {
			for b.Loop() {
				addResidualGo(plane, n, 0, 0, n, 12, coef, 8)
			}

			b.ReportMetric(float64(n*n)*float64(b.N)/b.Elapsed().Seconds()/1e6, "Msample/s")
		})

		if k := dsp.addResidual8; k != nil {
			b.Run(fmt.Sprintf("asm/%dx%d", n, n), func(b *testing.B) {
				for b.Loop() {
					k(plane, n, coef, n, 12)
				}

				b.ReportMetric(float64(n*n)*float64(b.N)/b.Elapsed().Seconds()/1e6, "Msample/s")
			})
		}
	}
}

func BenchmarkOdd(b *testing.B) {
	in := make([]int32, 32)
	for i := range in {
		in[i] = int32(i*4099%32768 - 16384)
	}

	out := make([]int32, 16)

	b.Run("go", func(b *testing.B) {
		for b.Loop() {
			oddGo(out, in, 1)
		}
	})

	if oddAsm != nil {
		b.Run("asm", func(b *testing.B) {
			for b.Loop() {
				oddAsm(out, in, 1)
			}
		})
	}
}

func BenchmarkPredPlanar(b *testing.B) {
	for _, n := range []int{8, 16, 32} {
		var r refSamples

		r.n = n
		for i := range 4*n + 1 {
			r.s[i] = int32(i * 7 % 256)
		}

		dst := make([]uint8, n*n)
		shift := log2(n) + 1

		b.Run(fmt.Sprintf("go/%d", n), func(b *testing.B) {
			for b.Loop() {
				predPlanarGo(dst, 0, n, &r, n, shift)
			}

			b.ReportMetric(float64(n*n)*float64(b.N)/b.Elapsed().Seconds()/1e6, "Msample/s")
		})

		if planarAsm != nil {
			b.Run(fmt.Sprintf("asm/%d", n), func(b *testing.B) {
				for b.Loop() {
					planarAsm(dst, n, &r, shift)
				}

				b.ReportMetric(float64(n*n)*float64(b.N)/b.Elapsed().Seconds()/1e6, "Msample/s")
			})
		}
	}
}

func BenchmarkPredUni(b *testing.B) {
	for _, w := range []int{16, 64} {
		h := w

		src := make([]int16, w*h)
		for i := range src {
			src[i] = int16(i*911%16384 - 4096)
		}

		dst := make([]uint8, w*h)

		b.Run(fmt.Sprintf("go/%d", w), func(b *testing.B) {
			for b.Loop() {
				predUniGo(dst, 0, w, src, w, w, h, 8, 6)
			}

			b.ReportMetric(float64(w*h)*float64(b.N)/b.Elapsed().Seconds()/1e6, "Msample/s")
		})

		if predUniAsm != nil {
			b.Run(fmt.Sprintf("asm/%d", w), func(b *testing.B) {
				for b.Loop() {
					predUniAsm(dst, w, src, w, w, h, 6)
				}

				b.ReportMetric(float64(w*h)*float64(b.N)/b.Elapsed().Seconds()/1e6, "Msample/s")
			})
		}
	}
}

func BenchmarkPredBi(b *testing.B) {
	for _, w := range []int{16, 64} {
		h := w

		s0 := make([]int16, w*h)
		s1 := make([]int16, w*h)

		for i := range s0 {
			s0[i] = int16(i*911%16384 - 4096)
			s1[i] = int16(i*613%16384 - 4096)
		}

		dst := make([]uint8, w*h)

		b.Run(fmt.Sprintf("go/%d", w), func(b *testing.B) {
			for b.Loop() {
				predBiGo(dst, 0, w, s0, s1, w, w, h, 8, 7)
			}

			b.ReportMetric(float64(w*h)*float64(b.N)/b.Elapsed().Seconds()/1e6, "Msample/s")
		})

		if predBiAsm != nil {
			b.Run(fmt.Sprintf("asm/%d", w), func(b *testing.B) {
				for b.Loop() {
					predBiAsm(dst, w, s0, s1, w, w, h, 7)
				}

				b.ReportMetric(float64(w*h)*float64(b.N)/b.Elapsed().Seconds()/1e6, "Msample/s")
			})
		}
	}
}

// withoutAsm runs f with the named kernels disabled, so a benchmark can time
// the Go path of a kernel whose scalar version is inline rather than a
// separate function.
func withoutAsm(b *testing.B, f func()) {
	b.Helper()

	tap, tapV, deq := mcTapAsm, mcTapV16Asm, dequant32Asm
	mcTapAsm, mcTapV16Asm, dequant32Asm = nil, nil, nil

	defer func() { mcTapAsm, mcTapV16Asm, dequant32Asm = tap, tapV, deq }()

	f()
}

func BenchmarkMCFilter(b *testing.B) {
	const picW, picH = 96, 96

	src := make([]uint8, picW*picH)
	for i := range src {
		src[i] = uint8(i * 31 % 256)
	}

	const w, h = 16, 16

	dst := make([]int16, w*h)
	scratch := make([]int32, 64*80)
	tmp16 := make([]int16, 64*80)
	pad := make([]uint8, 71*71)

	rate := func(b *testing.B) {
		b.ReportMetric(float64(w*h)*float64(b.N)/b.Elapsed().Seconds()/1e6, "Msample/s")
	}

	// One fractional direction and both, which is the two-pass case.
	for _, frac := range [][2]int{{1, 0}, {0, 1}, {2, 3}} {
		name := fmt.Sprintf("%d%d", frac[0], frac[1])

		luma := func() {
			mcLuma(dst, w, src, picW, picW, picH, 20, 20, frac[0], frac[1],
				w, h, 8, scratch, tmp16, pad)
		}

		chroma := func() {
			mcChroma(dst, w, src, picW, picW, picH, 20, 20, frac[0], frac[1],
				w, h, 8, scratch, tmp16, pad)
		}

		b.Run("luma/go/"+name, func(b *testing.B) {
			withoutAsm(b, func() {
				for b.Loop() {
					luma()
				}
			})

			rate(b)
		})

		if mcTapAsm != nil {
			b.Run("luma/asm/"+name, func(b *testing.B) {
				for b.Loop() {
					luma()
				}

				rate(b)
			})
		}

		b.Run("chroma/go/"+name, func(b *testing.B) {
			withoutAsm(b, func() {
				for b.Loop() {
					chroma()
				}
			})

			rate(b)
		})

		if mcTapAsm != nil {
			b.Run("chroma/asm/"+name, func(b *testing.B) {
				for b.Loop() {
					chroma()
				}

				rate(b)
			})
		}
	}
}

// BenchmarkInverseTransform compares the recursive butterfly against the
// column form, at a density typical of a residual block and at full density.
func BenchmarkInverseTransform(b *testing.B) {
	var s transformScratch

	for _, n := range []int{8, 16, 32} {
		for _, dense := range []bool{false, true} {
			last := n * 2
			name := "sparse"

			if dense {
				last = n * n
				name = "dense"
			}

			src := make([]int32, n*n)
			for i := range last {
				src[i] = int32(i*7919%65536 - 32768)
			}

			work := make([]int32, n*n)

			run := func(b *testing.B) {
				for b.Loop() {
					copy(work, src)
					inverseTransform(work, n, false, 8, false, &s)
				}

				b.ReportMetric(float64(n*n)*float64(b.N)/b.Elapsed().Seconds()/1e6,
					"Mcoef/s")
			}

			b.Run(fmt.Sprintf("go/%s/%d", name, n), func(b *testing.B) {
				save := idctColsAsm
				idctColsAsm = nil

				defer func() { idctColsAsm = save }()

				run(b)
			})

			if idctColsAsm != nil {
				b.Run(fmt.Sprintf("asm/%s/%d", name, n), run)
			}
		}
	}
}

func BenchmarkDequant(b *testing.B) {
	for _, n := range []int{8, 32} {
		coef := make([]int32, n*n)
		for i := range coef {
			coef[i] = int32(i*7919%2048 - 1024)
		}

		work := make([]int32, n*n)

		run := func(b *testing.B) {
			for b.Loop() {
				copy(work, coef)
				dequant(work, nil, n, 30, 8, false)
			}

			b.ReportMetric(float64(n*n)*float64(b.N)/b.Elapsed().Seconds()/1e6, "Mcoef/s")
		}

		b.Run(fmt.Sprintf("go/%d", n), func(b *testing.B) {
			withoutAsm(b, func() { run(b) })
		})

		if dequant32Asm != nil {
			b.Run(fmt.Sprintf("asm/%d", n), run)
		}
	}
}
