package hevc

import (
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
