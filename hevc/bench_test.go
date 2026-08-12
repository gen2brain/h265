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
			}
		}

		for _, p := range d.Flush() {
			pixels += int64(p.Width * p.Height)
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
