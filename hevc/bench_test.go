package hevc

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
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

// benchFrame decodes the first picture of a stream into an encoder's planes.
func benchFrame(b *testing.B, name string) (Frame, int, int) {
	b.Helper()

	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		b.Skip(err)
	}

	var d Decoder

	var pics []*Picture

	for _, nal := range SplitAnnexB(data) {
		if len(pics) > 0 {
			break
		}

		out, _ := d.DecodeNAL(nal)
		pics = append(pics, out...)
	}

	if len(pics) == 0 {
		pics = d.Flush()
	}

	if len(pics) == 0 {
		b.Skipf("%s decoded no pictures", name)
	}

	p := pics[0]
	w, h := p.CropW&^1, p.CropH&^1

	f := Frame{
		Y: make([]byte, w*h), Cb: make([]byte, w*h/4), Cr: make([]byte, w*h/4),
		StrideY: w, StrideC: w / 2,
	}

	for j := range h {
		copy(f.Y[j*w:(j+1)*w], p.Y[(p.CropY+j)*p.StrideY+p.CropX:])
	}

	for j := range h / 2 {
		copy(f.Cb[j*w/2:(j+1)*w/2], p.Cb[(p.CropY/2+j)*p.StrideC+p.CropX/2:])
		copy(f.Cr[j*w/2:(j+1)*w/2], p.Cr[(p.CropY/2+j)*p.StrideC+p.CropX/2:])
	}

	return f, w, h
}

// BenchmarkEncodeReal encodes real pictures, which BenchmarkEncodeLossy's
// synthetic ones stand in for badly. See SPEED.md.
func BenchmarkEncodeReal(b *testing.B) {
	for _, name := range []string{"realworld_320x240.h265", "realworld_720p.h265"} {
		frame, width, height := benchFrame(b, name)

		enc, err := NewEncoder(EncoderOptions{Width: width, Height: height})
		if err != nil {
			b.Fatal(err)
		}

		b.Run(fmt.Sprintf("%dx%d", width, height), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(width * height * 3 / 2))

			for b.Loop() {
				if _, err := enc.Encode(frame); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkEncodeLossy covers both shapes the coding tree takes: 128x128 is
// whole coding tree blocks and 176x144 is mostly edges, which is where the
// 16x16 coding units and the 8x8 transform choice live.
func BenchmarkEncodeLossy(b *testing.B) {
	for _, size := range [][2]int{{128, 128}, {176, 144}} {
		width, height := size[0], size[1]

		y := make([]byte, width*height)
		cb := make([]byte, width*height/4)
		cr := make([]byte, width*height/4)

		for i := range y {
			y[i] = byte(i*17 + i/13)
		}

		for i := range cb {
			cb[i] = byte(i*29 + 7)
			cr[i] = byte(i*43 + 11)
		}

		enc, err := NewEncoder(EncoderOptions{Width: width, Height: height})
		if err != nil {
			b.Fatal(err)
		}

		frame := Frame{Y: y, Cb: cb, Cr: cr, StrideY: width, StrideC: width / 2}

		b.Run(fmt.Sprintf("%dx%d", width, height), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(width * height * 3 / 2))

			for b.Loop() {
				if _, err := enc.Encode(frame); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkQuantize(b *testing.B) {
	for _, n := range []int{4, 8, 16, 32} {
		src := make([]int32, n*n)
		dst := make([]int32, n*n)
		for i := range src {
			src[i] = int32(i*7919%1048576 - 524288)
		}

		b.Run(fmt.Sprintf("%dx%d", n, n), func(b *testing.B) {
			b.SetBytes(int64(n * n * 4))
			for b.Loop() {
				quantize(dst, src, n, 26, 8)
			}
		})
	}
}

func BenchmarkEncodePCM(b *testing.B) {
	const width, height = 176, 144

	y := make([]byte, width*height)
	cb := make([]byte, width*height/4)
	cr := make([]byte, width*height/4)
	for i := range y {
		y[i] = byte(i*17 + i/13)
	}
	for i := range cb {
		cb[i] = byte(i*29 + 7)
		cr[i] = byte(i*43 + 11)
	}

	enc, err := NewEncoder(EncoderOptions{Width: width, Height: height, Lossless: true})
	if err != nil {
		b.Fatal(err)
	}
	frame := Frame{Y: y, Cb: cb, Cr: cr, StrideY: width, StrideC: width / 2}

	b.ReportAllocs()
	b.SetBytes(width * height * 3 / 2)
	b.ResetTimer()

	for b.Loop() {
		if _, err := enc.Encode(frame); err != nil {
			b.Fatal(err)
		}
	}
}

// wideSpeedup times f with the wide kernels on and off, alternating which runs
// first and keeping the fastest observation of each. Ordinary benchmarks cannot
// do this: their sub-benchmarks run in sequence, which on a laptop charges the
// first one for whatever the machine was doing when it started.
func wideSpeedup(t *testing.T, rounds, inner int, f func()) (on, off time.Duration) {
	t.Helper()

	on, off = time.Hour, time.Hour

	for r := range rounds {
		for i := range 2 {
			wide := i == 0
			if r%2 == 1 {
				wide = !wide
			}

			setWideKernels(wide)

			start := time.Now()

			for range inner {
				f()
			}

			d := time.Since(start) / time.Duration(inner)

			if wide && d < on {
				on = d
			} else if !wide && d < off {
				off = d
			}
		}
	}

	setWideKernels(true)

	return on, off
}

// TestWideSpeedup reports what the 512-bit kernels are worth end to end. It is
// a measurement rather than a gate: the ratio is well inside the noise of any
// machine that also has something else to do.
func TestWideSpeedup(t *testing.T) {
	if testing.Short() || !wideKernels() {
		t.Skip("no wide kernels to compare")
	}

	for _, name := range []string{"realworld_320x240.h265", "realworld_720p.h265", "1080p.h265"} {
		data, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Skip(err)
		}

		nals := SplitAnnexB(data)

		on, off := wideSpeedup(t, 20, 1, func() {
			var d Decoder

			for _, nal := range nals {
				out, err := d.DecodeNAL(nal)
				if err != nil {
					t.Fatal(err)
				}

				for _, p := range out {
					p.Release()
				}
			}

			for _, p := range d.Flush() {
				p.Release()
			}
		})

		t.Logf("%-24s wide %v narrow %v  %.3fx", name, on, off, float64(off)/float64(on))
	}
}

func BenchmarkSATD(b *testing.B) {
	const srcStride, predStride = 40, 16

	src := make([]uint8, srcStride*8)
	pred := make([]uint8, predStride*8)

	for i := range src {
		src[i] = uint8(i*7919%251 + 3)
	}

	for i := range pred {
		pred[i] = uint8(i*104729%241 + 7)
	}

	b.Run("go/16x8", func(b *testing.B) {
		for b.Loop() {
			satd8x8Go(src, srcStride, pred, predStride)
			satd8x8Go(src[8:], srcStride, pred[8:], predStride)
		}

		b.ReportMetric(128*float64(b.N)/b.Elapsed().Seconds()/1e6, "Msample/s")
	})

	if k := satd16x8Asm; k != nil {
		b.Run("asm/16x8", func(b *testing.B) {
			for b.Loop() {
				k(src, srcStride, pred, predStride)
			}

			b.ReportMetric(128*float64(b.N)/b.Elapsed().Seconds()/1e6, "Msample/s")
		})
	}
}

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

// angularRowsGo is what predAngularRows replaces, written out here because the
// production one is inlined into predAngular's generic body.
func angularRowsGo(dst []uint8, stride int, ref []int32, angle, n int) {
	for b := range n {
		idx := int(int32(b+1) * int32(angle) >> 5)
		fact := int32(b+1) * int32(angle) & 31
		row := ref[idx+1:]

		for a := range n {
			v := row[a]
			if fact != 0 {
				v = ((32-fact)*row[a] + fact*row[a+1] + 16) >> 5
			}

			dst[b*stride+a] = uint8(v)
		}
	}
}

func BenchmarkPredAngular(b *testing.B) {
	for _, n := range []int{8, 16, 32} {
		var ref [3 * 32 * 2]int32

		for i := range ref {
			ref[i] = int32(i*7919%251 + 3)
		}

		base := 2 * n
		dst := make([]uint8, n*n)

		b.Run(fmt.Sprintf("go/%dx%d", n, n), func(b *testing.B) {
			for b.Loop() {
				angularRowsGo(dst, n, ref[base:], 9, n)
			}

			b.ReportMetric(float64(n*n)*float64(b.N)/b.Elapsed().Seconds()/1e6, "Msample/s")
		})

		if !predAngularRows(dst, n, ref[base:], 0, n) {
			continue
		}

		b.Run(fmt.Sprintf("asm/%dx%d", n, n), func(b *testing.B) {
			for b.Loop() {
				predAngularRows(dst, n, ref[base:], 9, n)
			}

			b.ReportMetric(float64(n*n)*float64(b.N)/b.Elapsed().Seconds()/1e6, "Msample/s")
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

func BenchmarkForwardTransform(b *testing.B) {
	for _, n := range []int{4, 8, 16, 32} {
		src := make([]int32, n*n)
		for i := range src {
			src[i] = int32(i*31%511 - 255)
		}

		dst := make([]int32, n*n)

		b.Run(fmt.Sprintf("go/%d", n), func(b *testing.B) {
			for b.Loop() {
				forwardTransform8Go(dst, src, n)
			}

			b.ReportMetric(float64(n*n)*float64(b.N)/b.Elapsed().Seconds()/1e6, "Msample/s")
		})

		if k := forwardTransform8Asm; k != nil {
			b.Run(fmt.Sprintf("asm/%d", n), func(b *testing.B) {
				for b.Loop() {
					k(dst, src, n)
				}

				b.ReportMetric(float64(n*n)*float64(b.N)/b.Elapsed().Seconds()/1e6, "Msample/s")
			})
		}
	}
}

func TestForwardTransformWideSpeedup(t *testing.T) {
	if testing.Short() || !wideKernels() {
		t.Skip("no wide kernels to compare")
	}

	for _, n := range []int{16, 32} {
		src := make([]int32, n*n)
		for i := range src {
			src[i] = int32(i*31%511 - 255)
		}
		dst := make([]int32, n*n)

		on, off := wideSpeedup(t, 20, 1000, func() {
			forwardTransform8(dst, src, n)
		})
		t.Logf("%dx%d wide %v narrow %v  %.3fx", n, n, on, off, float64(off)/float64(on))
	}
}

func TestEncodeIntraWideSpeedup(t *testing.T) {
	if testing.Short() || !wideKernels() {
		t.Skip("no wide kernels to compare")
	}

	const width, height = 176, 144
	y := make([]byte, width*height)
	cb := make([]byte, width*height/4)
	cr := make([]byte, width*height/4)
	for i := range y {
		y[i] = byte(i*17 + i/13)
	}
	for i := range cb {
		cb[i] = byte(i*29 + 7)
		cr[i] = byte(i*43 + 11)
	}

	on, off := wideSpeedup(t, 20, 1, func() {
		if _, err := encodeIntraLossyQP(y, cb, cr, width, height, 26); err != nil {
			t.Fatal(err)
		}
	})
	t.Logf("wide %v narrow %v  %.3fx", on, off, float64(off)/float64(on))
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
