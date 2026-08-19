package hevc

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func decodeFile(t *testing.T, path string) []*Picture {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	var (
		d    Decoder
		pics []*Picture
	)

	for i, nal := range SplitAnnexB(data) {
		out, err := d.DecodeNAL(nal)
		if err != nil {
			t.Fatalf("%s: NAL %d (type %d): %v", filepath.Base(path), i, nal.Type, err)
		}

		pics = append(pics, out...)
	}

	pics = append(pics, d.Flush()...)

	return pics
}

// encodeIntraLossyQP and encodePCM code one 8-bit 4:2:0 picture the way a
// caller does, so the tests and the shipped path are the same code.
func encodeIntraLossyQP(y, cb, cr []uint8, width, height, qp int) ([]NALUnit, error) {
	return encodeFrame(EncoderOptions{Width: width, Height: height, QP: qp}, y, cb, cr)
}

func encodePCM(y, cb, cr []uint8, width, height int) ([]NALUnit, error) {
	return encodeFrame(EncoderOptions{Width: width, Height: height, Lossless: true}, y, cb, cr)
}

func encodeFrame(opts EncoderOptions, y, cb, cr []uint8) ([]NALUnit, error) {
	enc, err := NewEncoder(opts)
	if err != nil {
		return nil, err
	}

	return enc.Encode(Frame{Y: y, Cb: cb, Cr: cr,
		StrideY: opts.Width, StrideC: opts.Width / 2})
}

func planarYUV(p *Picture) []byte {
	out := make([]byte, 0, p.CropW*p.CropH*3)

	cx, cy, cw, ch := 0, 0, 0, 0
	if p.StrideC != 0 {
		cx, cy = p.CropX*p.WidthC/p.Width, p.CropY*p.HeightC/p.Height
		cw, ch = p.CropW*p.WidthC/p.Width, p.CropH*p.HeightC/p.Height
	}

	if p.deep() {
		crop16 := func(plane []uint16, stride, x, y, w, h int) {
			for j := range h {
				for i := range w {
					v := plane[(y+j)*stride+x+i]
					out = append(out, byte(v), byte(v>>8))
				}
			}
		}

		crop16(p.Y16, p.StrideY, p.CropX, p.CropY, p.CropW, p.CropH)

		if p.StrideC != 0 {
			crop16(p.Cb16, p.StrideC, cx, cy, cw, ch)
			crop16(p.Cr16, p.StrideC, cx, cy, cw, ch)
		}

		return out
	}

	crop := func(plane []byte, stride, x, y, w, h int) {
		for j := range h {
			out = append(out, plane[(y+j)*stride+x:(y+j)*stride+x+w]...)
		}
	}

	crop(p.Y, p.StrideY, p.CropX, p.CropY, p.CropW, p.CropH)

	if p.StrideC != 0 {
		crop(p.Cb, p.StrideC, cx, cy, cw, ch)
		crop(p.Cr, p.StrideC, cx, cy, cw, ch)
	}

	return out
}

func TestEncodePCM(t *testing.T) {
	const width, height = 32, 32

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

	nals, err := encodePCM(y, cb, cr, width, height)
	if err != nil {
		t.Fatal(err)
	}

	var d Decoder
	var pics []*Picture
	for _, nal := range nals {
		data := marshalNAL(nal.Type, nal.LayerID, nal.TemporalID, nal.RBSP)
		parsed, ok := ParseNAL(data)
		if !ok {
			t.Fatal("ParseNAL failed")
		}

		out, err := d.DecodeNAL(parsed)
		if err != nil {
			t.Fatalf("DecodeNAL %d: %v", nal.Type, err)
		}
		pics = append(pics, out...)
	}
	pics = append(pics, d.Flush()...)

	if len(pics) != 1 {
		t.Fatalf("pictures = %d", len(pics))
	}

	want := append(append(append([]byte{}, y...), cb...), cr...)
	if got := planarYUV(pics[0]); !bytes.Equal(got, want) {
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("PCM sample %d = %d, want %d", i, got[i], want[i])
			}
		}
	}
}

func TestEncodeLossyIntraIDR(t *testing.T) {
	const width, height = 32, 32

	src := mustRead(t, filepath.Join("testdata", "encoder_lossy_32x32_src.yuv"))
	if len(src) != width*height*3/2 {
		t.Fatalf("fixture size = %d", len(src))
	}
	y := src[:width*height]
	cb := src[width*height : width*height*5/4]
	cr := src[width*height*5/4:]

	nals, err := encodeIntraLossyQP(y, cb, cr, width, height, 26)
	if err != nil {
		t.Fatal(err)
	}

	var d Decoder
	var pics []*Picture
	for _, nal := range nals {
		out, err := d.DecodeNAL(nal)
		if err != nil {
			t.Fatalf("DecodeNAL %d: %v", nal.Type, err)
		}
		pics = append(pics, out...)
	}
	pics = append(pics, d.Flush()...)

	if len(pics) != 1 {
		t.Fatalf("pictures = %d", len(pics))
	}

	got := planarYUV(pics[0])
	want := append(append(append([]byte{}, y...), cb...), cr...)
	if bytes.Equal(got, want) {
		t.Fatal("QP 26 output equals source")
	}
	for _, v := range got[1:] {
		if v != got[0] {
			return
		}
	}
	t.Fatal("decoded frame is flat")
}

func TestEncodeLossyIntraModes(t *testing.T) {
	const width, height = 32, 16

	gradients := []struct {
		name string
		pix  func(int, int) byte
	}{
		{"vertical", func(_, y int) byte { return byte(20 + 5*y) }},
		{"diagonal", func(x, y int) byte { return byte(20 + 3*x + 5*y) }},
	}

	modes := make(map[int]bool)
	for _, gradient := range gradients {
		t.Run(gradient.name, func(t *testing.T) {
			y := make([]byte, width*height)
			cb := make([]byte, width*height/4)
			cr := make([]byte, width*height/4)
			for j := range height {
				for i := range width {
					y[j*width+i] = gradient.pix(i, j)
				}
			}

			var e intraEncoder[uint8]
			e.reset(y, cb, cr, width, height, 26)
			first := e.lumaMode(0, 0, lossyMPM(e.modes, width/16, 0, 0, 4))
			e.codeBlock(lossyBlock[uint8]{x: 0, y: 0, n: 16, mode: first}, true)
			e.modes[0], e.depth[0] = first, 2
			mode := e.lumaMode(16, 0, lossyMPM(e.modes, width/16, 1, 0, 4))
			if mode == intraDC {
				t.Fatal("gradient selected DC")
			}
			modes[mode] = true

			nals, err := encodeIntraLossyQP(y, cb, cr, width, height, 26)
			if err != nil {
				t.Fatal(err)
			}

			var d Decoder
			var pics []*Picture
			for _, nal := range nals {
				out, err := d.DecodeNAL(nal)
				if err != nil {
					t.Fatalf("DecodeNAL %d: %v", nal.Type, err)
				}
				pics = append(pics, out...)
			}
			pics = append(pics, d.Flush()...)
			if len(pics) != 1 {
				t.Fatalf("pictures = %d", len(pics))
			}
		})
	}
	if len(modes) != len(gradients) {
		t.Fatalf("gradient modes = %v", modes)
	}
}

func TestEncodeLossyIntraTransformChoice(t *testing.T) {
	const width, height = 16, 16

	y := make([]byte, width*height)
	cb := make([]byte, width*height/4)
	cr := make([]byte, width*height/4)
	for i := range y {
		y[i] = 96
	}
	for i := range cb {
		cb[i] = 112
		cr[i] = 144
	}
	nals, err := encodeIntraLossyQP(y, cb, cr, width, height, 26)
	if err != nil {
		t.Fatal(err)
	}

	var d Decoder
	for _, nal := range nals {
		if _, err := d.DecodeNAL(nal); err != nil {
			t.Fatalf("DecodeNAL %d: %v", nal.Type, err)
		}
	}
	if pics := d.Flush(); len(pics) != 1 {
		t.Fatalf("pictures = %d", len(pics))
	}
	if d.ctuPrev.blk[d.ctuPrev.blkIndex(4, 0)].tuV {
		t.Fatal("unexpected 4x4 transform boundary")
	}
}

func TestEncodeLossyIntraClosedLoop(t *testing.T) {
	const width, height = 128, 128

	y, cb, cr := lossyTestFrame(width, height)

	rbsp, recon := encodeRecon(t, y, cb, cr, width, height, 26)

	h := encoderHeaders{
		width: width, height: height, levelIDC: pcmLevelIDC(width * height),
		chromaFormat: 1, subWidthC: 2, subHeightC: 2,
		ctbLog2: 6, maxTrHierIntra: 2,
	}
	s, err := parseSPS(h.sps())
	if err != nil {
		t.Fatal(err)
	}
	if s.ctbSizeY != 64 {
		t.Fatalf("CTB size = %d", s.ctbSizeY)
	}
	p, err := parsePPS(h.pps())
	if err != nil {
		t.Fatal(err)
	}
	sh, err := parseSliceHeader(rbsp, NALIdrNLP, s, p)
	if err != nil {
		t.Fatal(err)
	}
	// slice_loop_filter_across_slices_enabled_flag is only in the header while
	// deblocking is on, so the entry point count behind it moves with it.
	if sh.deblockingDisabled || !sh.loopFilterAcross ||
		sh.betaOffsetDiv2 != 0 || sh.tcOffsetDiv2 != 0 || len(sh.entryPointOffsets) != 0 {
		t.Fatalf("slice header = %+v", sh)
	}
	nals := []NALUnit{
		{Type: NALVPS, RBSP: h.vps()},
		{Type: NALSPS, RBSP: h.sps()},
		{Type: NALPPS, RBSP: h.pps()},
		{Type: NALIdrNLP, RBSP: rbsp},
	}

	var d Decoder
	var pics []*Picture
	for _, nal := range nals {
		out, err := d.DecodeNAL(nal)
		if err != nil {
			t.Fatalf("DecodeNAL %d: %v", nal.Type, err)
		}
		pics = append(pics, out...)
	}
	pics = append(pics, d.Flush()...)
	if len(pics) != 1 {
		t.Fatalf("pictures = %d", len(pics))
	}
	want := append(append(append([]byte{}, recon[0]...), recon[1]...), recon[2]...)
	if got := planarYUV(pics[0]); !bytes.Equal(got, want) {
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("sample %d = %d, want %d", i, got[i], want[i])
			}
		}
	}
}

// TestEncodeTransformEdges holds the edge map the encoder deblocks by to the
// transform tree the decoder reads back out of the same bitstream. The two are
// derived apart, and 8.7.2 filters exactly where they agree.
func TestEncodeTransformEdges(t *testing.T) {
	const width, height = 128, 96

	r := rand.New(rand.NewPCG(9, 4))

	for kind := range 7 {
		y, cb, cr := lossyPattern(r, width, height, kind)

		for _, qp := range []int{12, 26, 44} {
			t.Run(fmt.Sprintf("kind%d/qp%d", kind, qp), func(t *testing.T) {
				var e intraEncoder[uint8]

				rbsp, err := e.slice(y, cb, cr, width, height, qp)
				if err != nil {
					t.Fatal(err)
				}

				var d Decoder
				for _, nal := range e.nals(width, height, rbsp) {
					if _, err := d.DecodeNAL(nal); err != nil {
						t.Fatalf("DecodeNAL %d: %v", nal.Type, err)
					}
				}

				if pics := d.Flush(); len(pics) != 1 {
					t.Fatalf("pictures = %d", len(pics))
				}

				marked := 0

				for by := 0; by < height; by += 4 {
					for bx := 0; bx < width; bx += 4 {
						got := e.edges[by/4*(width/4)+bx/4]
						blk := d.ctuPrev.blk[d.ctuPrev.blkIndex(bx, by)]

						want := uint8(0)
						if blk.tuV {
							want |= edgeV
						}

						if blk.tuH {
							want |= edgeH
						}

						if got != want {
							t.Fatalf("edges at %d,%d = %d, want %d", bx, by, got, want)
						}

						marked += int(got)
					}
				}

				if marked == 0 {
					t.Fatal("no transform edges")
				}
			})
		}
	}
}

// TestEncodeChromaFormats codes every sampling but 4:2:0 and holds the decoded
// picture to the reconstruction the encoder built. Monochrome takes chroma out
// of the tree, 4:2:2 stacks two blocks per luma one, and 4:4:4 splits chroma
// down with the luma to 4x4.
func TestEncodeChromaFormats(t *testing.T) {
	sizes := [][2]int{{16, 16}, {64, 64}, {80, 48}, {2, 2}, {50, 34}, {130, 98}}

	for _, chroma := range []ChromaFormat{ChromaMono, Chroma422, Chroma444} {
		sw, sh := chroma.sub()

		for _, size := range sizes {
			width, height := size[0], size[1]

			for kind := range 7 {
				r := rand.New(rand.NewPCG(uint64(kind), uint64(width*height)))
				y, cb, cr := lossyPlanes(r, width, height, kind, sw, sh)

				for _, qp := range []int{1, 26, 51} {
					name := fmt.Sprintf("%d/%dx%d/kind%d/qp%d", chroma, width, height, kind, qp)

					t.Run(name, func(t *testing.T) {
						enc, err := NewEncoder(EncoderOptions{Width: width, Height: height,
							QP: qp, Chroma: chroma})
						if err != nil {
							t.Fatal(err)
						}

						frame := Frame{Y: y, StrideY: width}
						if chroma != ChromaMono {
							frame.Cb, frame.Cr, frame.StrideC = cb, cr, width/sw
						}

						nals, err := enc.Encode(frame)
						if err != nil {
							t.Fatal(err)
						}

						var (
							d    Decoder
							pics []*Picture
						)

						for _, nal := range nals {
							out, err := d.DecodeNAL(nal)
							if err != nil {
								t.Fatalf("DecodeNAL %d: %v", nal.Type, err)
							}

							pics = append(pics, out...)
						}

						pics = append(pics, d.Flush()...)

						if len(pics) != 1 {
							t.Fatalf("pictures = %d", len(pics))
						}

						p := pics[0]
						if uint32(p.ChromaFormat) != chroma.idc() {
							t.Fatalf("chroma format = %d, want %d", p.ChromaFormat, chroma.idc())
						}

						cw := codedSize(width)
						want := cropPlane(enc.intra.recon[0], cw, width, height)

						for c := 1; c < 3 && chroma != ChromaMono; c++ {
							want = append(want, cropPlane(enc.intra.recon[c], cw/sw,
								width/sw, height/sh)...)
						}

						if got := planarYUV(p); !bytes.Equal(got, want) {
							for i := range min(len(got), len(want)) {
								if got[i] != want[i] {
									t.Fatalf("sample %d = %d, want %d", i, got[i], want[i])
								}
							}

							t.Fatalf("decoded %d bytes, want %d", len(got), len(want))
						}
					})
				}
			}
		}
	}
}

// deepPlanes is a picture of sixteen bit samples at a chroma sampling.
func deepPlanes(r *rand.Rand, width, height, depth, subW, subH int) ([]uint16, []uint16, []uint16) {
	top := uint(1)<<depth - 1

	y := make([]uint16, width*height)
	cb := make([]uint16, width/subW*height/subH)
	cr := make([]uint16, len(cb))

	fill := func(p []uint16, stride int) {
		for i := range p {
			x, j := i%stride, i/stride

			switch {
			case i%3 == 0:
				p[i] = uint16(r.UintN(top + 1))
			case i%3 == 1:
				p[i] = uint16(uint(x*4+j*3) & top)
			default:
				p[i] = uint16(top / 2)
			}
		}
	}

	fill(y, width)
	fill(cb, width/subW)
	fill(cr, width/subW)

	return y, cb, cr
}

// TestEncodeBitDepth codes above eight bits, where the samples come in sixteen
// bit planes, Qp' carries the offset of 8.6.1 and none of the eight bit kernels
// apply. The decoded picture has to be the reconstruction the encoder built,
// and libde265 and ffmpeg have to read the same stream.
func TestEncodeBitDepth(t *testing.T) {
	for _, depth := range []int{10, 12} {
		for _, chroma := range []ChromaFormat{Chroma420, Chroma422, Chroma444, ChromaMono} {
			sw, sh := chroma.sub()

			for _, size := range [][2]int{{64, 64}, {176, 144}, {130, 98}} {
				width, height := size[0], size[1]

				for _, qp := range []int{12, 30, 51} {
					for _, lossless := range []bool{false, true} {
						name := fmt.Sprintf("d%d/c%d/%dx%d/qp%d", depth, chroma, width, height, qp)
						if lossless {
							name = fmt.Sprintf("d%d/c%d/%dx%d/pcm", depth, chroma, width, height)
						}

						t.Run(name, func(t *testing.T) {
							r := rand.New(rand.NewPCG(uint64(depth), uint64(width*height)))
							y, cb, cr := deepPlanes(r, width, height, depth, sw, sh)

							enc, err := NewEncoder(EncoderOptions{Width: width, Height: height,
								QP: qp, Chroma: chroma, BitDepth: depth, Lossless: lossless})
							if err != nil {
								t.Fatal(err)
							}

							frame := Frame{Y16: y, StrideY: width}
							if chroma != ChromaMono {
								frame.Cb16, frame.Cr16, frame.StrideC = cb, cr, width/sw
							}

							nals, err := enc.Encode(frame)
							if err != nil {
								t.Fatal(err)
							}

							var (
								d    Decoder
								pics []*Picture
							)

							for _, nal := range nals {
								out, err := d.DecodeNAL(nal)
								if err != nil {
									t.Fatalf("DecodeNAL %d: %v", nal.Type, err)
								}

								pics = append(pics, out...)
							}

							pics = append(pics, d.Flush()...)

							if len(pics) != 1 {
								t.Fatalf("pictures = %d", len(pics))
							}

							p := pics[0]
							if p.BitDepth != depth || uint32(p.ChromaFormat) != chroma.idc() {
								t.Fatalf("depth %d format %d", p.BitDepth, p.ChromaFormat)
							}

							// Lossless has to give the source itself back.
							want := [3][]uint16{y, cb, cr}
							if !lossless {
								cw := codedSize(width)
								want[0] = cropDeep(enc.deep.recon[0], cw, width, height)

								for c := 1; c < 3 && chroma != ChromaMono; c++ {
									want[c] = cropDeep(enc.deep.recon[c], cw/sw, width/sw, height/sh)
								}
							}

							planes := [3][]uint16{p.Y16, p.Cb16, p.Cr16}
							strides := [3]int{p.StrideY, p.StrideC, p.StrideC}
							dims := [3][2]int{{width, height},
								{width / sw, height / sh}, {width / sw, height / sh}}
							origin := [3][2]int{{p.CropX, p.CropY},
								{p.CropX / sw, p.CropY / sh}, {p.CropX / sw, p.CropY / sh}}

							last := 3
							if chroma == ChromaMono {
								last = 1
							}

							for c := range last {
								for j := range dims[c][1] {
									for i := range dims[c][0] {
										got := planes[c][(origin[c][1]+j)*strides[c]+origin[c][0]+i]
										if w := want[c][j*dims[c][0]+i]; got != w {
											t.Fatalf("plane %d at %d,%d = %d, want %d", c, i, j, got, w)
										}
									}
								}
							}

							checkDeepExternal(t, nals, want[0], width, height,
								depth, qp, chroma == ChromaMono)
						})
					}
				}
			}
		}
	}
}

func cropDeep(src []uint16, stride, width, height int) []uint16 {
	out := make([]uint16, 0, width*height)

	for y := range height {
		out = append(out, src[y*stride:y*stride+width]...)
	}

	return out
}

// checkDeepExternal holds a deep stream to libde265 and ffmpeg, which write
// sixteen bit samples little endian whatever the coded depth is.
//
// Two operating points are left out, each to the tool that reads it. libde265
// aborts on a monochrome picture above eight bits whose conformance window
// takes columns off. ffmpeg hands back a flat picture at twelve bits once Qp'
// passes 73, where libde265 and this decoder agree sample for sample.
func checkDeepExternal(t *testing.T, nals []NALUnit, luma []uint16,
	width, height, depth, qp int, mono bool,
) {
	t.Helper()

	dir := t.TempDir()

	stream := filepath.Join(dir, "deep.h265")
	if err := os.WriteFile(stream, MarshalAnnexB(nals), 0o644); err != nil {
		t.Fatal(err)
	}

	tools := map[string]func(out string) *exec.Cmd{
		"dec265": func(out string) *exec.Cmd {
			return exec.Command("dec265", "-q", "-o", out, stream)
		},
		"ffmpeg": func(out string) *exec.Cmd {
			return exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error",
				"-i", stream, "-f", "rawvideo", "-y", out)
		},
	}

	for _, name := range []string{"dec265", "ffmpeg"} {
		if _, err := exec.LookPath(name); err != nil {
			continue
		}

		if name == "dec265" && mono && codedSize(width) != width {
			continue
		}

		if name == "ffmpeg" && qp+6*(depth-8) > 73 {
			continue
		}

		out := filepath.Join(dir, name+".yuv")
		if b, err := tools[name](out).CombinedOutput(); err != nil {
			t.Fatalf("%s: %v: %s", name, err, b)
		}

		got, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}

		if len(got) < 2*width*height {
			t.Fatalf("%s gave %d bytes", name, len(got))
		}

		for j := range height {
			for i := range width {
				o := 2 * (j*width + i)

				if v := uint16(got[o]) | uint16(got[o+1])<<8; v != luma[j*width+i] {
					t.Fatalf("%s luma at %d,%d = %d, want %d", name, i, j, v, luma[j*width+i])
				}
			}
		}
	}
}

// TestEncodeBitDepthQuality codes one picture at every sample size, the deeper
// ones carrying the same content shifted up. Qp' of 8.6.1 is meant to make the
// three cost the same bits for the same quality, so a decision path that has
// kept an eight bit constant in it shows up as one of them falling behind. The
// round trip alone would not: encoder and decoder agree on a bad
// reconstruction as readily as on a good one.
func TestEncodeBitDepthQuality(t *testing.T) {
	const width, height = 176, 144

	for _, qp := range []int{12, 20, 30, 40, 51} {
		t.Run(fmt.Sprintf("qp%d", qp), func(t *testing.T) {
			var (
				base  float64
				bytes int
			)

			for _, depth := range []int{8, 10, 12} {
				r := rand.New(rand.NewPCG(5, 6))
				y, cb, cr := deepPlanes(r, width, height, 8, 2, 2)

				for i := range y {
					y[i] <<= depth - 8
				}

				for i := range cb {
					cb[i] <<= depth - 8
					cr[i] <<= depth - 8
				}

				enc, err := NewEncoder(EncoderOptions{Width: width, Height: height,
					QP: qp, BitDepth: depth})
				if err != nil {
					t.Fatal(err)
				}

				var nals []NALUnit

				if depth == 8 {
					nals, err = enc.Encode(Frame{Y: narrow(y), Cb: narrow(cb), Cr: narrow(cr),
						StrideY: width, StrideC: width / 2})
				} else {
					nals, err = enc.Encode(Frame{Y16: y, Cb16: cb, Cr16: cr,
						StrideY: width, StrideC: width / 2})
				}

				if err != nil {
					t.Fatal(err)
				}

				recon := enc.deep
				if depth == 8 {
					recon = nil
				}

				cw := codedSize(width)

				var sse float64

				for j := range height {
					for i := range width {
						var got int64
						if recon != nil {
							got = int64(recon.recon[0][j*cw+i])
						} else {
							got = int64(enc.intra.recon[0][j*cw+i])
						}

						d := float64(got - int64(y[j*width+i]))
						sse += d * d
					}
				}

				top := float64(int(1)<<depth - 1)
				psnr := 10 * math.Log10(top*top/(sse/float64(width*height)))
				n := len(MarshalAnnexB(nals))

				if depth == 8 {
					base, bytes = psnr, n

					continue
				}

				if psnr < base-1 || psnr > base+1 {
					t.Fatalf("depth %d psnr %.2f, eight bit %.2f", depth, psnr, base)
				}

				if n < bytes*9/10 || n > bytes*11/10 {
					t.Fatalf("depth %d took %d bytes, eight bit %d", depth, n, bytes)
				}
			}
		})
	}
}

func narrow(v []uint16) []uint8 {
	out := make([]uint8, len(v))
	for i := range v {
		out[i] = uint8(v[i])
	}

	return out
}

// TestEncodeCUSize covers the choice between one 32x32 coding unit and four
// 16x16 ones. Flat content has nothing to gain from splitting; quadrants that
// each run a different way cannot share one prediction mode, so they must.
func TestEncodeCUSize(t *testing.T) {
	const size = 64

	flatY := make([]byte, size*size)
	turnY := make([]byte, size*size)
	cb := make([]byte, size*size/4)
	cr := make([]byte, size*size/4)

	for i := range cb {
		cb[i], cr[i] = 128, 128
	}

	dirs := [4][2]int{{8, 0}, {0, 8}, {6, 6}, {6, -6}}

	for j := range size {
		for i := range size {
			flatY[j*size+i] = 96
			d := dirs[(j/16&1)*2+i/16&1]
			turnY[j*size+i] = byte(68 + d[0]*(i%16) + d[1]*(j%16))
		}
	}

	for _, c := range []struct {
		name  string
		y     []byte
		depth uint8
	}{
		{"flat", flatY, 1},
		{"turning", turnY, 2},
	} {
		t.Run(c.name, func(t *testing.T) {
			nals, err := encodeIntraLossyQP(c.y, cb, cr, size, size, 26)
			if err != nil {
				t.Fatal(err)
			}

			var d Decoder

			for _, nal := range nals {
				if _, err := d.DecodeNAL(nal); err != nil {
					t.Fatalf("DecodeNAL %d: %v", nal.Type, err)
				}
			}

			if pics := d.Flush(); len(pics) != 1 {
				t.Fatalf("pictures = %d", len(pics))
			}

			if got := d.ctuPrev.cuDepth[d.ctuPrev.tbIndex(0, 0)]; got != c.depth {
				t.Fatalf("CU depth = %d, want %d", got, c.depth)
			}

			// A whole 32x32 unit leaves no transform boundary inside itself.
			if inside := d.ctuPrev.blk[d.ctuPrev.blkIndex(16, 0)].tuV; inside == (c.depth == 1) {
				t.Fatalf("transform boundary at 16 = %v", inside)
			}
		})
	}
}

// TestCUTransformFlat pins both halves of the test: no residual, and one
// transform rather than four.
func TestCUTransformFlat(t *testing.T) {
	var tu cuTransform

	if !tu.flat() {
		t.Fatal("an empty 32x32 transform is not flat")
	}

	tu.split = true

	if tu.flat() {
		t.Fatal("four transforms are flat")
	}

	tu.split = false
	tu.whole = true

	if tu.flat() {
		t.Fatal("a coded luma block is flat")
	}

	tu.whole = false

	// 4:2:2 stacks two chroma blocks per component, and any one of the four
	// carrying levels is enough.
	for c := range 2 {
		for k := range 2 {
			tu.wholeC[c][k] = true

			if tu.flat() {
				t.Fatalf("a coded chroma block %d,%d is flat", c, k)
			}

			tu.wholeC[c][k] = false
		}
	}

	if !tu.flat() {
		t.Fatal("cleared again and not flat")
	}
}

// TestCUTransformFlags holds the stored coded block flags to the levels they
// stand for, which nothing else checks now the tree takes them on trust. Every
// chroma sampling is swept, since each lays its blocks out differently.
func TestCUTransformFlags(t *testing.T) {
	const size = 64

	y := make([]byte, size*size)

	dirs := [4][2]int{{8, 0}, {0, 8}, {6, 6}, {6, -6}}

	for j := range size {
		for i := range size {
			d := dirs[(j/16&1)*2+i/16&1]
			y[j*size+i] = byte(68 + d[0]*(i%16) + d[1]*(j%16))
		}
	}

	for _, chroma := range []ChromaFormat{Chroma420, Chroma422, Chroma444, ChromaMono} {
		sw, sh := chroma.sub()

		var cb, cr []byte

		if chroma != ChromaMono {
			cb = make([]byte, size/sw*size/sh)
			cr = make([]byte, len(cb))

			for i := range cb {
				cb[i] = byte(120 + i%9)
				cr[i] = byte(130 + i%7)
			}
		}

		for _, qp := range []int{12, 26, 40} {
			t.Run(fmt.Sprintf("%d/qp%d", chroma, qp), func(t *testing.T) {
				var e intraEncoder[uint8]

				e.reset(y, cb, cr, size, size, qp)

				if got := e.s.chromaFormatIDC; got != chroma.idc() {
					t.Fatalf("chroma_format_idc = %d, want %d", got, chroma.idc())
				}

				if err := e.cu32(0, 0, 1, intraDC); err != nil {
					t.Fatal(err)
				}

				tu := &e.tu

				if want := hasCoefficients(tu.y32[:]); tu.whole != want {
					t.Fatalf("whole = %v, want %v", tu.whole, want)
				}

				checkChroma(t, e.chromaTBs(), 5, &e, tu.wholeC, tu.c32[0][:], tu.c32[1][:])

				for i := range 4 {
					if want := hasCoefficients(tu.y[i][:]); tu.quad[i] != want {
						t.Fatalf("quad[%d] = %v, want %v", i, tu.quad[i], want)
					}

					checkChroma(t, e.chromaTBs(), 4, &e, tu.quadC[i], tu.c[0][i][:], tu.c[1][i][:])
				}

				plan := e.tu8(0, 0, intraDC)

				if want := hasCoefficients(plan.y8[:]); !plan.split && plan.cbfY8 != want {
					t.Fatalf("cbfY8 = %v, want %v", plan.cbfY8, want)
				}

				for i := range 4 {
					if want := hasCoefficients(plan.y[i][:]); plan.split && plan.cbfY[i] != want {
						t.Fatalf("cbfY[%d] = %v, want %v", i, plan.cbfY[i], want)
					}
				}

				if plan.split && e.chromaAt(4) {
					for j := range 4 {
						checkChroma(t, e.chromaTBs(), 2, &e, plan.cbfC[j],
							plan.c[0][j][:], plan.c[1][j][:])
					}

					return
				}

				checkChroma(t, e.chromaTBs(), 3, &e, plan.cbfC8, plan.c8[0][:], plan.c8[1][:])
			})
		}
	}
}

// checkChroma holds one luma block's chroma flags to the levels behind them,
// each stacked block on its own stretch of the buffer.
func checkChroma(t *testing.T, tbs, log2 int, e *intraEncoder[uint8], cbf [2][2]bool, cb, cr []int32) {
	t.Helper()

	n := 1 << (2 * e.chromaLog2(log2))
	src := [2][]int32{cb, cr}

	for c := range 2 {
		for k := range 2 {
			want := k < tbs && hasCoefficients(src[c][k*n:(k+1)*n])
			if cbf[c][k] != want {
				t.Fatalf("cbf[%d][%d] at log2 %d = %v, want %v", c, k, log2, cbf[c][k], want)
			}
		}
	}
}

// TestLumaModeSearch holds the mode search to a sweep written the slow way,
// every mode filtering its own reference and predicting again to be trialled.
// Nothing else notices when the shortlist serves a cached prediction that
// belongs to another mode, because the picture still decodes.
func TestLumaModeSearch(t *testing.T) {
	pics := decodeFile(t, filepath.Join("testdata", "realworld_320x240.h265"))
	if len(pics) == 0 {
		t.Skip("no pictures")
	}

	p := pics[0]
	w, h := p.CropW&^15, p.CropH&^15

	y := make([]byte, w*h)
	cb := make([]byte, w*h/4)
	cr := make([]byte, w*h/4)

	for j := range h {
		copy(y[j*w:(j+1)*w], p.Y[(p.CropY+j)*p.StrideY+p.CropX:])
	}

	for j := range h / 2 {
		copy(cb[j*w/2:(j+1)*w/2], p.Cb[(p.CropY/2+j)*p.StrideC+p.CropX/2:])
		copy(cr[j*w/2:(j+1)*w/2], p.Cr[(p.CropY/2+j)*p.StrideC+p.CropX/2:])
	}

	for _, qp := range []int{14, 26, 38} {
		var e, slow intraEncoder[uint8]

		e.reset(y, cb, cr, w, h, qp)
		slow.reset(y, cb, cr, w, h, qp)

		// Without a reconstruction the reference samples are all substituted
		// and every mode scores the same, which pins nothing.
		for _, enc := range [2]*intraEncoder[uint8]{&e, &slow} {
			copy(enc.recon[0], y)
			copy(enc.recon[1], cb)
			copy(enc.recon[2], cr)

			for i := range enc.coded[0] {
				enc.coded[0][i] = 1
			}

			for i := range enc.coded[1] {
				enc.coded[1][i] = 1
			}
		}

		for y0 := 0; y0 < h; y0 += 16 {
			for x0 := 0; x0 < w; x0 += 16 {
				cand := lossyMPM(e.modes, w/16, x0/16, y0/16, 4)

				got := e.lumaMode(x0, y0, cand)
				if want := slowLumaMode(&slow, x0, y0, cand); got != want {
					t.Fatalf("qp=%d at %d,%d: mode %d, want %d", qp, x0, y0, got, want)
				}
			}
		}
	}
}

// slowLumaMode is lumaMode with nothing shared between modes.
func slowLumaMode(e *intraEncoder[uint8], x, y int, cand [3]int) int {
	b := lossyBlock[uint8]{x: x, y: y, n: 16}
	e.prepareRef(b)

	var (
		short [modeShortlist]int
		score [modeShortlist]int64
	)

	for i := range short {
		short[i], score[i] = intraPlanar, 1<<62
	}

	pred := make([]uint8, 16*16)

	for mode := intraPlanar; mode <= 34; mode++ {
		e.scratch.ref.copyFrom(&e.scratch.base)
		filterRef(&e.scratch.ref, mode, 0, 8, &e.s)
		intraPredict(pred, 0, 16, &e.scratch.ref, mode, 0, 8)

		s := e.satd(x, y, pred, 16, 1<<62)

		for i := range short {
			if s >= score[i] {
				continue
			}

			copy(short[i+1:], short[i:len(short)-1])
			copy(score[i+1:], score[i:len(score)-1])
			short[i], score[i] = mode, s

			break
		}
	}

	bestMode, bestCost := short[0], int64(-1)

	for _, mode := range short {
		b.mode, b.pred = mode, nil

		coef, trial, cbf := e.blockData(b, false)

		cost := e.rdCost(e.distortion(0, x, y, 16, trial, 16), e.modeRate(cand, mode, coef, cbf))
		if bestCost < 0 || cost < bestCost {
			bestMode, bestCost = mode, cost
		}
	}

	return bestMode
}

func TestLossyTU8Rate(t *testing.T) {
	e := new(intraEncoder[uint8])
	e.reset(make([]byte, 16*16), make([]byte, 8*8), make([]byte, 8*8), 16, 16, 26)
	before := e.cabac
	plan := lossyTU8Plan{split: true}
	plan.y[0][0] = 1

	if rate := e.tu8Rate(&plan, intraPlanar); rate <= 0 {
		t.Fatalf("rate = %d", rate)
	}
	if e.cabac != before {
		t.Fatal("CABAC state changed")
	}
	if allocs := testing.AllocsPerRun(100, func() {
		e.tu8Rate(&plan, intraPlanar)
	}); allocs != 0 {
		t.Fatalf("allocations = %f", allocs)
	}
}

// encodeRecon codes a picture and hands back the reconstruction the encoder
// built, cropped to the picture the conformance window leaves, which is what a
// decoder must reproduce sample for sample.
func encodeRecon(t *testing.T, y, cb, cr []uint8, width, height, qp int) ([]byte, [3][]uint8) {
	t.Helper()

	cw, ch := codedSize(width), codedSize(height)
	py, _ := padPlane(nil, y, width, width, height, cw, ch)
	pcb, _ := padPlane(nil, cb, width/2, width/2, height/2, cw/2, ch/2)
	pcr, _ := padPlane(nil, cr, width/2, width/2, height/2, cw/2, ch/2)

	var e intraEncoder[uint8]

	rbsp, err := e.slice(py, pcb, pcr, cw, ch, qp)
	if err != nil {
		t.Fatal(err)
	}

	return rbsp, [3][]uint8{
		cropPlane(e.recon[0], cw, width, height),
		cropPlane(e.recon[1], cw/2, width/2, height/2),
		cropPlane(e.recon[2], cw/2, width/2, height/2),
	}
}

func cropPlane(src []uint8, stride, width, height int) []uint8 {
	out := make([]uint8, 0, width*height)
	for y := range height {
		out = append(out, src[y*stride:y*stride+width]...)
	}

	return out
}

// TestEncodeExternal holds the written bitstream to decoders that were not
// written here. Both arms are exact: the lossy one against the reconstruction
// the encoder built, the PCM one against the source itself.
func TestEncodeExternal(t *testing.T) {
	tools := map[string]func(in, out string) *exec.Cmd{
		"dec265": func(in, out string) *exec.Cmd {
			return exec.Command("dec265", "-q", "-o", out, in)
		},
		"ffmpeg": func(in, out string) *exec.Cmd {
			return exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error",
				"-i", in, "-f", "rawvideo", "-y", out)
		},
	}

	cases := []struct {
		width, height int
		qp            int
		pcm           bool
		threads       int
		chroma        ChromaFormat
	}{
		{48, 48, 26, false, 0, Chroma420},
		{64, 64, 1, false, 0, Chroma420},
		{80, 48, 51, false, 0, Chroma420},
		{176, 144, 34, false, 0, Chroma420},
		{50, 34, 26, false, 0, Chroma420},
		{130, 98, 30, false, 0, Chroma420},
		{32, 32, 0, true, 0, Chroma420},
		{80, 48, 0, true, 0, Chroma420},
		{66, 18, 0, true, 0, Chroma420},

		// The wavefront needs two rows and two columns of coding tree blocks
		// before it changes anything, and its entry points are what another
		// decoder is most likely to disagree with.
		{192, 192, 26, false, 3, Chroma420},
		{320, 144, 34, false, 2, Chroma420},
		{144, 320, 18, false, 4, Chroma420},

		// Monochrome leaves the chroma out of the tree and the planes out of
		// the picture. 4:2:2 stacks two chroma blocks per luma one and 4:4:4
		// splits chroma down with the luma, which another decoder has to agree
		// about all the way to the 4x4 blocks.
		{64, 64, 26, false, 0, ChromaMono},
		{176, 144, 40, false, 0, ChromaMono},
		{130, 98, 20, false, 0, ChromaMono},
		{192, 192, 30, false, 3, ChromaMono},
		{64, 64, 26, false, 0, Chroma422},
		{176, 144, 12, false, 0, Chroma422},
		{130, 98, 40, false, 0, Chroma422},
		{192, 192, 30, false, 3, Chroma422},
		{64, 64, 26, false, 0, Chroma444},
		{176, 144, 12, false, 0, Chroma444},
		{130, 98, 40, false, 0, Chroma444},
		{192, 192, 30, false, 3, Chroma444},
	}

	for _, c := range cases {
		r := rand.New(rand.NewPCG(uint64(c.width), uint64(c.qp)))
		csw, csh := c.chroma.sub()
		y, cb, cr := lossyPlanes(r, c.width, c.height, 3, csw, csh)

		var (
			nals []NALUnit
			want []byte
			err  error
		)

		switch {
		case c.chroma != Chroma420:
			enc, e := NewEncoder(EncoderOptions{Width: c.width, Height: c.height,
				QP: c.qp, Chroma: c.chroma})
			if e != nil {
				t.Fatal(e)
			}

			enc.Threads(c.threads)

			sw, sh := c.chroma.sub()
			frame := Frame{Y: y, StrideY: c.width}

			if c.chroma != ChromaMono {
				frame.Cb, frame.Cr, frame.StrideC = cb, cr, c.width/sw
			}

			nals, err = enc.Encode(frame)

			cw := codedSize(c.width)
			want = cropPlane(enc.intra.recon[0], cw, c.width, c.height)

			for p := 1; p < 3 && c.chroma != ChromaMono; p++ {
				want = append(want, cropPlane(enc.intra.recon[p], cw/sw,
					c.width/sw, c.height/sh)...)
			}

		case c.pcm:
			nals, err = encodePCM(y, cb, cr, c.width, c.height)
			want = append(append(append([]byte{}, y...), cb...), cr...)

		case c.threads > 0:
			enc, e := NewEncoder(EncoderOptions{Width: c.width, Height: c.height, QP: c.qp})
			if e != nil {
				t.Fatal(e)
			}

			enc.Threads(c.threads)

			nals, err = enc.Encode(Frame{Y: y, Cb: cb, Cr: cr,
				StrideY: c.width, StrideC: c.width / 2})

			if !enc.intra.wavefront {
				t.Fatalf("%dx%d did not take the wavefront", c.width, c.height)
			}

			cw := codedSize(c.width)
			for j := range c.height {
				want = append(want, enc.intra.recon[0][j*cw:j*cw+c.width]...)
			}

			for ch := 1; ch < 3; ch++ {
				for j := range c.height / 2 {
					want = append(want, enc.intra.recon[ch][j*cw/2:j*cw/2+c.width/2]...)
				}
			}

		default:
			var recon [3][]uint8

			_, recon = encodeRecon(t, y, cb, cr, c.width, c.height, c.qp)
			nals, err = encodeIntraLossyQP(y, cb, cr, c.width, c.height, c.qp)
			want = append(append(append([]byte{}, recon[0]...), recon[1]...), recon[2]...)
		}

		if err != nil {
			t.Fatal(err)
		}

		dir := t.TempDir()

		stream := filepath.Join(dir, "encoded.h265")
		if err := os.WriteFile(stream, MarshalAnnexB(nals), 0o644); err != nil {
			t.Fatal(err)
		}

		for _, tool := range []string{"dec265", "ffmpeg"} {
			if _, err := exec.LookPath(tool); err != nil {
				t.Logf("%s not installed", tool)

				continue
			}

			name := fmt.Sprintf("%s/%dx%d/qp%d", tool, c.width, c.height, c.qp)

			switch {
			case c.pcm:
				name = fmt.Sprintf("%s/%dx%d/pcm", tool, c.width, c.height)
			case c.threads > 0:
				name = fmt.Sprintf("%s/%dx%d/wpp%d", tool, c.width, c.height, c.threads)
			}

			if c.chroma != Chroma420 {
				name = fmt.Sprintf("%s/%s", name, [4]string{"420", "422", "444", "mono"}[c.chroma])
			}

			t.Run(name, func(t *testing.T) {
				out := filepath.Join(t.TempDir(), "decoded.yuv")
				if b, err := tools[tool](stream, out).CombinedOutput(); err != nil {
					t.Fatalf("%s: %v: %s", tool, err, b)
				}

				got, err := os.ReadFile(out)
				if err != nil {
					t.Fatal(err)
				}

				if !bytes.Equal(got, want) {
					t.Fatalf("decoded %d bytes, want %d", len(got), len(want))
				}
			})
		}
	}
}

// TestEncodeWavefront pins the two things the row threading must not change:
// the stream a given picture codes to, whatever it is spread over, and that
// the stream is the reconstruction the encoder predicted from.
func TestEncodeWavefront(t *testing.T) {
	const width, height = 320, 192

	r := rand.New(rand.NewPCG(51, 52))
	y, cb, cr := lossyPattern(r, width, height, 3)
	frame := Frame{Y: y, Cb: cb, Cr: cr, StrideY: width, StrideC: width / 2}

	var first []byte

	for _, threads := range []int{2, 3, 5, 16} {
		enc, err := NewEncoder(EncoderOptions{Width: width, Height: height, QP: 30})
		if err != nil {
			t.Fatal(err)
		}

		enc.Threads(threads)

		nals, err := enc.Encode(frame)
		if err != nil {
			t.Fatal(err)
		}

		if !enc.intra.wavefront {
			t.Fatal("no wavefront")
		}

		stream := MarshalAnnexB(nals)

		if first == nil {
			first = stream
		} else if !bytes.Equal(stream, first) {
			t.Fatalf("threads=%d: stream differs", threads)
		}

		var d Decoder

		var pics []*Picture

		for _, nal := range SplitAnnexB(stream) {
			out, err := d.DecodeNAL(nal)
			if err != nil {
				t.Fatalf("threads=%d: %v", threads, err)
			}

			pics = append(pics, out...)
		}

		pics = append(pics, d.Flush()...)

		if len(pics) != 1 {
			t.Fatalf("threads=%d: %d pictures", threads, len(pics))
		}

		p := pics[0]
		cw := codedSize(width)

		for j := range height {
			for i := range width {
				if got, want := p.Y[j*p.StrideY+i], enc.intra.recon[0][j*cw+i]; got != want {
					t.Fatalf("threads=%d: luma %d,%d = %d, want %d", threads, i, j, got, want)
				}
			}
		}
	}
}

func TestEncodeLossyIntraQP(t *testing.T) {
	const width, height = 48, 48

	y, cb, cr := lossyTestFrame(width, height)
	for _, qp := range []int{18, 26, 34, 42} {
		t.Run(fmt.Sprintf("qp=%d", qp), func(t *testing.T) {
			nals, err := encodeIntraLossyQP(y, cb, cr, width, height, qp)
			if err != nil {
				t.Fatal(err)
			}
			_, recon := encodeRecon(t, y, cb, cr, width, height, qp)

			var d Decoder
			for _, nal := range nals {
				if _, err := d.DecodeNAL(nal); err != nil {
					t.Fatalf("DecodeNAL %d: %v", nal.Type, err)
				}
			}
			pics := d.Flush()
			if len(pics) != 1 {
				t.Fatalf("pictures = %d", len(pics))
			}
			want := append(append(append([]byte{}, recon[0]...), recon[1]...), recon[2]...)
			if got := planarYUV(pics[0]); !bytes.Equal(got, want) {
				t.Fatal("decoded reconstruction differs")
			}
		})
	}

	for _, qp := range []int{-1, 52} {
		if _, err := encodeIntraLossyQP(y, cb, cr, width, height, qp); !errors.Is(err, ErrInvalidEncodeInput) {
			t.Fatalf("QP %d: %v", qp, err)
		}
	}
}

func TestEncodeLossyIntraQualityBaseline(t *testing.T) {
	const width, height = 176, 144

	y, cb, cr := lossyTestFrame(width, height)
	lastBytes := math.MaxInt
	lastMSE := -1.0
	for _, qp := range []int{18, 26, 34, 42} {
		nals, err := encodeIntraLossyQP(y, cb, cr, width, height, qp)
		if err != nil {
			t.Fatal(err)
		}
		_, recon := encodeRecon(t, y, cb, cr, width, height, qp)
		reconY := recon[0]

		var sse uint64
		for i := range y {
			d := int64(y[i]) - int64(reconY[i])
			sse += uint64(d * d)
		}
		mse := float64(sse) / float64(len(y))
		bytes := len(MarshalAnnexB(nals))
		if bytes >= lastBytes {
			t.Fatalf("QP %d bytes = %d, previous = %d", qp, bytes, lastBytes)
		}
		if mse <= lastMSE {
			t.Fatalf("QP %d MSE = %f, previous = %f", qp, mse, lastMSE)
		}
		t.Logf("QP %d: %d bytes, %.2f dB", qp, bytes, 10*math.Log10(255*255/mse))
		lastBytes, lastMSE = bytes, mse
	}
}

// lossyPattern builds a frame of one kind: noise stresses the residual coder,
// the flat and bilevel ones the mode decision, and the gradients the angular
// predictions.
func lossyPattern(r *rand.Rand, width, height, kind int) ([]byte, []byte, []byte) {
	return lossyPlanes(r, width, height, kind, 2, 2)
}

// lossyPlanes is lossyPattern at a chroma sampling of its own.
func lossyPlanes(r *rand.Rand, width, height, kind, subW, subH int) ([]byte, []byte, []byte) {
	y := make([]byte, width*height)
	cb := make([]byte, width/subW*height/subH)
	cr := make([]byte, len(cb))

	fill := func(p []byte, stride int) {
		for i := range p {
			x, j := i%stride, i/stride

			switch kind {
			case 0:
				p[i] = byte(r.UintN(256))
			case 1:
				p[i] = byte(3 * x)
			case 2:
				p[i] = byte(128 + 100*((x/4+j/4)&1))
			case 3:
				p[i] = byte(int(r.UintN(24)) + x + j)
			case 4:
				p[i] = 0
			case 5:
				p[i] = 255
			default:
				p[i] = byte(r.UintN(2) * 255)
			}
		}
	}

	fill(y, width)
	fill(cb, width/subW)
	fill(cr, width/subW)

	return y, cb, cr
}

// TestEncodeLossyRoundTrip decodes what the encoder writes and holds it to the
// reconstruction the encoder built, over every picture shape the coding tree
// takes: whole coding tree blocks, ones cut short on either edge, and the whole
// range of QP.
func TestEncodeLossyRoundTrip(t *testing.T) {
	sizes := [][2]int{{16, 16}, {48, 32}, {64, 64}, {80, 48}, {128, 64},
		{2, 2}, {18, 18}, {50, 34}, {66, 18}, {130, 98}}

	for _, size := range sizes {
		width, height := size[0], size[1]

		for kind := range 7 {
			r := rand.New(rand.NewPCG(uint64(kind), uint64(width*height)))
			y, cb, cr := lossyPattern(r, width, height, kind)

			for _, qp := range []int{1, 20, 34, 51} {
				name := fmt.Sprintf("%dx%d/kind%d/qp%d", width, height, kind, qp)

				t.Run(name, func(t *testing.T) {
					_, recon := encodeRecon(t, y, cb, cr, width, height, qp)

					nals, err := encodeIntraLossyQP(y, cb, cr, width, height, qp)
					if err != nil {
						t.Fatal(err)
					}

					var (
						d    Decoder
						pics []*Picture
					)

					for _, nal := range nals {
						out, err := d.DecodeNAL(nal)
						if err != nil {
							t.Fatalf("DecodeNAL %d: %v", nal.Type, err)
						}

						pics = append(pics, out...)
					}

					pics = append(pics, d.Flush()...)

					if len(pics) != 1 {
						t.Fatalf("pictures = %d", len(pics))
					}

					want := append(append(append([]byte{}, recon[0]...), recon[1]...), recon[2]...)

					got := planarYUV(pics[0])
					if bytes.Equal(got, want) {
						return
					}

					for i := range want {
						if got[i] != want[i] {
							t.Fatalf("sample %d = %d, want %d", i, got[i], want[i])
						}
					}
				})
			}
		}
	}
}

// FuzzEncode holds the encoder to the same contract on arbitrary input: it must
// not panic, and a decoder must reproduce the reconstruction it built, sample
// for sample. The PCM arm must reproduce the source itself.
func FuzzEncode(f *testing.F) {
	f.Add(uint8(0), uint8(0), uint8(26), true, []byte{0})
	f.Add(uint8(3), uint8(1), uint8(51), false, []byte{7, 200, 13, 255, 0, 91})
	f.Add(uint8(7), uint8(4), uint8(1), false, []byte{128, 129, 130})

	f.Fuzz(func(t *testing.T, w, h, q uint8, lossless bool, data []byte) {
		if len(data) == 0 {
			return
		}

		width, height := (int(w)%64+1)*2, (int(h)%32+1)*2
		qp := int(q%51) + 1

		y := make([]byte, width*height)
		cb := make([]byte, width*height/4)
		cr := make([]byte, width*height/4)

		for i := range y {
			y[i] = data[i%len(data)]
		}

		for i := range cb {
			cb[i] = data[(i*3+1)%len(data)]
			cr[i] = data[(i*5+2)%len(data)]
		}

		var (
			nals []NALUnit
			want []byte
			err  error
		)

		if lossless {
			nals, err = encodePCM(y, cb, cr, width, height)
			want = append(append(append([]byte{}, y...), cb...), cr...)
		} else {
			_, recon := encodeRecon(t, y, cb, cr, width, height, qp)
			nals, err = encodeIntraLossyQP(y, cb, cr, width, height, qp)
			want = append(append(append([]byte{}, recon[0]...), recon[1]...), recon[2]...)
		}

		if err != nil {
			t.Fatalf("encode: %v", err)
		}

		var (
			d    Decoder
			pics []*Picture
		)

		for _, nal := range nals {
			out, err := d.DecodeNAL(nal)
			if err != nil {
				t.Fatalf("DecodeNAL %d: %v", nal.Type, err)
			}

			pics = append(pics, out...)
		}

		pics = append(pics, d.Flush()...)

		if len(pics) != 1 {
			t.Fatalf("pictures = %d", len(pics))
		}

		if got := planarYUV(pics[0]); !bytes.Equal(got, want) {
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("%dx%d qp %d lossless %v: sample %d = %d, want %d",
						width, height, qp, lossless, i, got[i], want[i])
				}
			}
		}
	})
}

func lossyTestFrame(width, height int) ([]byte, []byte, []byte) {
	y := make([]byte, width*height)
	cb := make([]byte, width*height/4)
	cr := make([]byte, width*height/4)
	for j := range height {
		for i := range width {
			y[j*width+i] = byte((17*i + 29*j + i*j) % 256)
		}
	}
	for j := range height / 2 {
		for i := range width / 2 {
			cb[j*width/2+i] = byte((31*i + 7*j + i*j) % 256)
			cr[j*width/2+i] = byte((11*i + 37*j + 3*i*j) % 256)
		}
	}

	return y, cb, cr
}

func TestLossyMPMIgnoresAboveCTB(t *testing.T) {
	modes := []int{
		intraPlanar, intraDC, intraHor,
		intraVer, 18, 34,
	}
	if got, want := lossyMPM(modes, 3, 1, 1, 1), [3]int{intraVer, intraDC, intraPlanar}; got != want {
		t.Fatalf("MPM = %v, want %v", got, want)
	}
}

func TestEncoder(t *testing.T) {
	enc, err := NewEncoder(EncoderOptions{Width: 16, Height: 16, QP: 34})
	if err != nil {
		t.Fatal(err)
	}
	defaultEnc, err := NewEncoder(EncoderOptions{Width: 16, Height: 16})
	if err != nil {
		t.Fatal(err)
	}
	if defaultEnc.qp != 26 {
		t.Fatalf("default QP = %d", defaultEnc.qp)
	}

	frame := Frame{
		Y:       make([]byte, 16*20),
		Cb:      make([]byte, 8*10),
		Cr:      make([]byte, 8*10),
		StrideY: 20,
		StrideC: 10,
	}
	for y := range 16 {
		for x := range 16 {
			frame.Y[y*frame.StrideY+x] = byte(x + 17*y)
		}
	}

	nals, err := enc.Encode(frame)
	if err != nil {
		t.Fatal(err)
	}
	if len(nals) != 4 || nals[3].Type != NALIdrNLP {
		t.Fatalf("NALs = %+v", nals)
	}
	y, _ := padPlane(nil, frame.Y, frame.StrideY, 16, 16, 16, 16)
	cb, _ := padPlane(nil, frame.Cb, frame.StrideC, 8, 8, 8, 8)
	cr, _ := padPlane(nil, frame.Cr, frame.StrideC, 8, 8, 8, 8)
	want, err := encodeIntraLossyQP(y, cb, cr, 16, 16, 34)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(MarshalAnnexB(nals), MarshalAnnexB(want)) {
		t.Fatal("Encoder did not use QP")
	}
	if got := SplitAnnexB(MarshalAnnexB(nals)); len(got) != len(nals) {
		t.Fatalf("Annex B NAL count = %d", len(got))
	}

	if out, err := enc.Flush(); err != nil || out != nil {
		t.Fatalf("Flush = %v, %v", out, err)
	}

	if _, err := NewEncoder(EncoderOptions{Width: 15, Height: 16}); !errors.Is(err, ErrInvalidEncodeInput) {
		t.Fatalf("odd width: %v", err)
	}
	if _, err := NewEncoder(EncoderOptions{Width: 16, Height: 15}); !errors.Is(err, ErrInvalidEncodeInput) {
		t.Fatalf("odd height: %v", err)
	}
	if _, err := NewEncoder(EncoderOptions{Width: 18, Height: 6}); err != nil {
		t.Fatalf("size off the coding grid: %v", err)
	}
	if _, err := NewEncoder(EncoderOptions{Width: 16, Height: 16, QP: 52}); !errors.Is(err, ErrInvalidEncodeInput) {
		t.Fatalf("invalid QP: %v", err)
	}
	if _, err := enc.Encode(Frame{}); !errors.Is(err, ErrInvalidEncodeInput) {
		t.Fatalf("invalid frame: %v", err)
	}
}

var cannotDecode = map[string]string{
	"fuzz_dequant_qp_overflow.h265": "slice QP outside the range of 7.4.7.1",
	"fuzz_mvd_overflow.h265":        "corrupted motion vector difference desyncs the arithmetic decoder",
}

var ffmpegWrong = map[string]string{
	"culossless_176x144.h265": "FFmpeg filters chroma across a lossless boundary",
}

// TestDecodeAgainstReference decodes the streams that ship a reference YUV and
// compares every sample.
func TestDecodeAgainstReference(t *testing.T) {
	refs, err := filepath.Glob(filepath.Join("testdata", "*_ref.yuv"))
	if err != nil {
		t.Fatal(err)
	}

	if len(refs) == 0 {
		t.Skip("no reference YUVs")
	}

	for _, ref := range refs {
		name := filepath.Base(ref)
		stream := ref[:len(ref)-len("_ref.yuv")] + ".h265"

		t.Run(name, func(t *testing.T) {
			if why, ok := cannotDecode[filepath.Base(stream)]; ok {
				if decodesCleanly(stream) {
					t.Fatalf("now decodes without error (%s): remove it from cannotDecode", why)
				}

				t.Skipf("not supported yet: %s", why)
			}

			want, err := os.ReadFile(ref)
			if err != nil {
				t.Fatal(err)
			}

			pics := decodeFile(t, stream)
			if len(pics) == 0 {
				t.Fatal("no pictures decoded")
			}

			var got []byte
			for _, p := range pics {
				got = append(got, planarYUV(p)...)
			}

			if len(got) != len(want) {
				t.Fatalf("decoded %d bytes, reference has %d", len(got), len(want))
			}

			diff := 0

			for i := range got {
				if got[i] != want[i] {
					if diff == 0 {
						t.Errorf("first mismatch at byte %d: got %d, want %d",
							i, got[i], want[i])
					}

					diff++
				}
			}

			if diff != 0 {
				t.Errorf("%d of %d samples differ", diff, len(got))
			}
		})
	}
}

func decodesCleanly(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}

	var d Decoder

	for _, nal := range SplitAnnexB(data) {
		if _, err := d.DecodeNAL(nal); err != nil {
			return false
		}
	}

	return true
}

func ffmpegYUV(t *testing.T, path string) []byte {
	t.Helper()

	out := filepath.Join(t.TempDir(), "ref.yuv")

	cmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error",
		"-i", path, "-f", "rawvideo", "-y", out)

	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v: %s", err, b)
	}

	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}

	return b
}

// TestDecodeAgainstFFmpeg widens the comparison to every stream in the corpus,
// using FFmpeg to produce the reference.
func TestDecodeAgainstFFmpeg(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}

	for _, stream := range testdataFiles(t) {
		name := filepath.Base(stream)

		t.Run(name, func(t *testing.T) {
			if why, ok := cannotDecode[name]; ok {
				t.Skipf("not supported yet: %s", why)
			}

			if why, ok := ffmpegWrong[name]; ok {
				if matchesFFmpeg(t, stream) {
					t.Fatalf("now matches FFmpeg (%s): remove it from ffmpegWrong", why)
				}

				t.Skipf("FFmpeg is not a reference here: %s", why)
			}

			want := ffmpegYUV(t, stream)

			var (
				d    Decoder
				got  []byte
				data = mustRead(t, stream)
			)

			for i, nal := range SplitAnnexB(data) {
				out, err := d.DecodeNAL(nal)

				if errors.Is(err, ErrUnsupported) {
					t.Skipf("NAL %d (type %d): %v", i, nal.Type, err)
				}

				if err != nil {
					t.Fatalf("NAL %d (type %d): %v", i, nal.Type, err)
				}

				for _, p := range out {
					got = append(got, planarYUV(p)...)
				}
			}

			for _, p := range d.Flush() {
				got = append(got, planarYUV(p)...)
			}

			if len(got) != len(want) {
				t.Fatalf("decoded %d bytes, FFmpeg produced %d", len(got), len(want))
			}

			diff := 0

			for i := range got {
				if got[i] != want[i] {
					if diff == 0 {
						t.Errorf("first mismatch at byte %d: got %d, want %d", i, got[i], want[i])
					}

					diff++
				}
			}

			if diff != 0 {
				t.Errorf("%d of %d samples differ", diff, len(got))
			}
		})
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	return b
}

func matchesFFmpeg(t *testing.T, stream string) bool {
	t.Helper()

	defer func() { recover() }()

	var (
		d   Decoder
		got []byte
	)

	for _, nal := range SplitAnnexB(mustRead(t, stream)) {
		out, err := d.DecodeNAL(nal)
		if err != nil {
			return false
		}

		for _, p := range out {
			got = append(got, planarYUV(p)...)
		}
	}

	for _, p := range d.Flush() {
		got = append(got, planarYUV(p)...)
	}

	want := ffmpegYUV(t, stream)

	if len(got) != len(want) {
		return false
	}

	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}

	return true
}

// referenceMD5 reads the recorded digests, so the comparison still runs where
// FFmpeg is not installed.
func referenceMD5(t *testing.T) map[string]string {
	t.Helper()

	b, err := os.ReadFile(filepath.Join("testdata", "streams.md5"))
	if err != nil {
		t.Fatal(err)
	}

	want := make(map[string]string)

	for line := range strings.Lines(string(b)) {
		f := strings.Fields(line)
		if len(f) == 2 {
			want[f[1]] = f[0]
		}
	}

	return want
}

// TestDecodeAgainstManifest checks every stream in the corpus against the
// recorded digest. It is the gate that does not need FFmpeg present.
func TestDecodeAgainstManifest(t *testing.T) {
	want := referenceMD5(t)

	seen := 0

	for _, stream := range testdataFiles(t) {
		name := filepath.Base(stream)

		sum, ok := want[name]
		if !ok {
			if _, bad := cannotDecode[name]; !bad {
				t.Errorf("%s has no digest in testdata/streams.md5", name)
			}

			continue
		}

		seen++

		t.Run(name, func(t *testing.T) {
			h := md5.New()

			var d Decoder

			for _, nal := range SplitAnnexB(mustRead(t, stream)) {
				out, err := d.DecodeNAL(nal)
				if err != nil {
					t.Fatalf("decode: %v", err)
				}

				for _, p := range out {
					h.Write(planarYUV(p))
				}
			}

			for _, p := range d.Flush() {
				h.Write(planarYUV(p))
			}

			if got := hex.EncodeToString(h.Sum(nil)); got != sum {
				t.Errorf("digest %s, want %s", got, sum)
			}
		})
	}

	if seen != len(want) {
		t.Errorf("checked %d streams, manifest has %d", seen, len(want))
	}
}

// decodeThreads is decodeFile with the wavefront bounded and errors returned
// rather than fatal, so a stream can be decoded both ways and compared even
// when it is one of the fuzz cases that must be rejected.
func decodeThreads(t *testing.T, path string, threads int) ([]*Picture, error) {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	var (
		d    Decoder
		pics []*Picture
	)

	d.Threads(threads)

	for _, nal := range SplitAnnexB(data) {
		out, err := d.DecodeNAL(nal)
		if err != nil {
			return pics, err
		}

		pics = append(pics, out...)
	}

	return append(pics, d.Flush()...), nil
}

// TestWavefrontMatchesSerial holds the threaded wavefront to the serial loop it
// replaces. Only streams with entropy_coding_sync take the threaded path, so
// the rest of the corpus is checking that the two agree trivially.
func TestWavefrontMatchesSerial(t *testing.T) {
	streams, err := filepath.Glob(filepath.Join("testdata", "*.h265"))
	if err != nil || len(streams) == 0 {
		t.Skip("no streams")
	}

	for _, stream := range streams {
		t.Run(filepath.Base(stream), func(t *testing.T) {
			one, errOne := decodeThreads(t, stream, 1)
			many, errMany := decodeThreads(t, stream, 8)

			if (errOne == nil) != (errMany == nil) {
				t.Fatalf("serial err %v, threaded err %v", errOne, errMany)
			}

			if len(one) != len(many) {
				t.Fatalf("%d pictures serial, %d threaded", len(one), len(many))
			}

			for i := range one {
				a, b := planarYUV(one[i]), planarYUV(many[i])
				if !bytes.Equal(a, b) {
					t.Fatalf("picture %d (POC %d) differs", i, one[i].POC)
				}
			}
		})
	}
}

// streamsBySize is the bundled corpus ordered by size.
func streamsBySize(t testing.TB) []string {
	t.Helper()

	files, err := filepath.Glob(filepath.Join("testdata", "*.h265"))
	if err != nil || len(files) == 0 {
		return nil
	}

	sort.Slice(files, func(i, j int) bool {
		a, _ := os.Stat(files[i])
		b, _ := os.Stat(files[j])

		return a.Size() < b.Size()
	})

	return files
}

// smallestStreams seeds the fuzzer. A 1080p stream costs seconds per execution
// and the fuzzer barely explores; the small ones still carry a parameter set, a
// slice header and real residual.
func smallestStreams(t testing.TB, n int) []string {
	files := streamsBySize(t)

	return files[:min(n, len(files))]
}

// spreadStreams picks n across the size range, so a truncation lands mid-slice
// on a picture worth decoding as well as on a sixteen by sixteen one.
func spreadStreams(t testing.TB, n int) []string {
	files := streamsBySize(t)
	if len(files) <= n {
		return files
	}

	out := make([]string, 0, n)
	for i := range n {
		out = append(out, files[i*len(files)/n])
	}

	return out
}

// decodeQuietly decodes without caring what comes out, and reports an error the
// caller is not prepared for. A panic is a decoder bug whatever the input.
func decodeQuietly(t *testing.T, data []byte) {
	t.Helper()

	var d Decoder

	// One goroutine, so a failure reproduces, and a small picture limit so a
	// mutated sequence header cannot ask for one that takes seconds to fill.
	d.Threads(1)
	d.FrameSizeLimit(1 << 16)

	release := func(pics []*Picture) {
		for _, p := range pics {
			if p.Width <= 0 || p.Height <= 0 {
				t.Fatalf("decoded %dx%d", p.Width, p.Height)
			}

			p.Release()
		}
	}

	for _, nal := range SplitAnnexB(data) {
		pics, err := d.DecodeNAL(nal)
		if err != nil {
			if !errors.Is(err, ErrInvalid) && !errors.Is(err, ErrUnsupported) {
				t.Fatalf("unexpected error: %v", err)
			}

			return
		}

		release(pics)
	}

	release(d.Flush())
}

func FuzzDecodeNAL(f *testing.F) {
	for _, p := range smallestStreams(f, 12) {
		if b, err := os.ReadFile(p); err == nil {
			f.Add(b)
		}
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		decodeQuietly(t, data)
	})
}

// TestMalformedStreams truncates and corrupts the bundled streams and requires
// the decoder to return rather than crash. It is the in-repo stand-in for an
// invalid-stream corpus: deterministic, and it runs everywhere the tests do.
func TestMalformedStreams(t *testing.T) {
	streams := spreadStreams(t, 8)
	if len(streams) == 0 {
		t.Skip("no bundled streams")
	}

	r := rand.New(rand.NewPCG(9, 10))

	for _, p := range streams {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}

		name := filepath.Base(p)

		t.Run(name+"/truncated", func(t *testing.T) {
			for _, frac := range []int{1, 2, 3, 5, 7, 8} {
				decodeQuietly(t, data[:len(data)*frac/8])
			}
		})

		t.Run(name+"/corrupt", func(t *testing.T) {
			for range 16 {
				bad := bytes.Clone(data)
				for range 8 {
					bad[r.IntN(len(bad))] = byte(r.IntN(256))
				}

				decodeQuietly(t, bad)
			}
		})
	}
}
