package heic

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"image"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"
)

func testdataFiles(t *testing.T) []string {
	t.Helper()

	files, err := filepath.Glob(filepath.Join("testdata", "*.heic"))
	if err != nil {
		t.Fatal(err)
	}

	if len(files) == 0 {
		t.Skip("no test files")
	}

	return files
}

func mustRead(t *testing.T, name string) []byte {
	t.Helper()

	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}

	return b
}

// referencePlanes holds the digest of each file's decoded planes, taken from
// libde265 decoding the same item data.
func referencePlanes(t *testing.T) map[string]string {
	t.Helper()

	b, err := os.ReadFile(filepath.Join("testdata", "planes.md5"))
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

// planarBytes serialises the decoded planes the way a raw YUV dump would, so
// the digest can be compared against a reference decoder's output.
func planarBytes(t *testing.T, name string) []byte {
	t.Helper()

	img, err := Decode(bytes.NewReader(mustRead(t, name)), Options{ToYCbCr: true})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	var out []byte

	appendPlane := func(pix []uint8, stride, w, h int) {
		for y := range h {
			out = append(out, pix[y*stride:y*stride+w]...)
		}
	}

	switch m := img.(type) {
	case *image.Gray:
		appendPlane(m.Pix, m.Stride, m.Rect.Dx(), m.Rect.Dy())

	case *image.YCbCr:
		appendYCbCr(&out, m, appendPlane)

	case *image.NYCbCrA:
		appendYCbCr(&out, &m.YCbCr, appendPlane)

	default:
		t.Fatalf("ToYCbCr returned %T", img)
	}

	return out
}

func appendYCbCr(out *[]byte, m *image.YCbCr, add func([]uint8, int, int, int)) {
	w, h := m.Rect.Dx(), m.Rect.Dy()

	cw, ch := w, h

	switch m.SubsampleRatio {
	case image.YCbCrSubsampleRatio420:
		cw, ch = (w+1)/2, (h+1)/2
	case image.YCbCrSubsampleRatio422:
		cw = (w + 1) / 2
	}

	add(m.Y, m.YStride, w, h)
	add(m.Cb, m.CStride, cw, ch)
	add(m.Cr, m.CStride, cw, ch)
}

// TestDecodePlanes is the gate: the planes this package hands back must match
// what libde265 produces from the same item data.
func TestDecodePlanes(t *testing.T) {
	want := referencePlanes(t)

	for _, f := range testdataFiles(t) {
		name := filepath.Base(f)

		t.Run(name, func(t *testing.T) {
			sum, ok := want[name]
			if !ok {
				t.Fatalf("%s has no digest in testdata/planes.md5", name)
			}

			h := md5.Sum(planarBytes(t, f))
			if got := hex.EncodeToString(h[:]); got != sum {
				t.Errorf("planes digest %s, want %s", got, sum)
			}
		})
	}
}

func TestDecode(t *testing.T) {
	for _, f := range testdataFiles(t) {
		name := filepath.Base(f)

		t.Run(name, func(t *testing.T) {
			data := mustRead(t, f)

			cfg, err := DecodeConfig(bytes.NewReader(data))
			if err != nil {
				t.Fatalf("DecodeConfig: %v", err)
			}

			img, ci, err := DecodeColor(bytes.NewReader(data))
			if err != nil {
				t.Fatalf("DecodeColor: %v", err)
			}

			b := img.Bounds()
			if b.Dx() != cfg.Width || b.Dy() != cfg.Height {
				t.Errorf("decoded %dx%d, DecodeConfig said %dx%d",
					b.Dx(), b.Dy(), cfg.Width, cfg.Height)
			}

			switch img.(type) {
			case *image.NRGBA, *image.NRGBA64:
			default:
				t.Fatalf("Decode returned %T, want NRGBA or NRGBA64", img)
			}

			if img.ColorModel() != cfg.ColorModel {
				t.Errorf("color model %v, DecodeConfig said %v",
					img.ColorModel(), cfg.ColorModel)
			}

			if ci.Matrix == 0 && b.Empty() {
				t.Error("identity matrix with an empty image")
			}
		})
	}
}

// TestImageDecode checks the image.RegisterFormat registration.
func TestImageDecode(t *testing.T) {
	for _, f := range testdataFiles(t) {
		name := filepath.Base(f)

		t.Run(name, func(t *testing.T) {
			img, format, err := image.Decode(bytes.NewReader(mustRead(t, f)))
			if err != nil {
				t.Fatalf("image.Decode: %v", err)
			}

			if format != "heic" {
				t.Errorf("format %q, want heic", format)
			}

			if img.Bounds().Empty() {
				t.Error("empty image")
			}
		})
	}
}

func TestAlpha(t *testing.T) {
	img, err := Decode(bytes.NewReader(mustRead(t, "testdata/alpha.heic")))
	if err != nil {
		t.Fatal(err)
	}

	m, ok := img.(*image.NRGBA)
	if !ok {
		t.Fatalf("got %T", img)
	}

	// The fixture ramps alpha with x and is lossy coded, so the test is that
	// the ramp survives, not that any one sample is exact.
	left, right := int(m.Pix[3]), int(m.Pix[(m.Rect.Dx()-1)*4+3])

	if left > 8 {
		t.Errorf("alpha at the left edge = %d, want near 0", left)
	}

	if right < 240 {
		t.Errorf("alpha at the right edge = %d, want near opaque", right)
	}

	// An alpha channel that decoded as a constant would pass the ends but is
	// not a ramp.
	mid := int(m.Pix[(m.Rect.Dx()/2)*4+3])
	if mid <= left || mid >= right {
		t.Errorf("alpha midpoint %d is not between %d and %d", mid, left, right)
	}
}

func TestToYCbCrTypes(t *testing.T) {
	cases := map[string]string{
		"basic.heic":     "*image.YCbCr",
		"chroma422.heic": "*image.YCbCr",
		"chroma444.heic": "*image.YCbCr",
		"alpha.heic":     "*image.NYCbCrA",
	}

	for name, want := range cases {
		t.Run(name, func(t *testing.T) {
			img, err := Decode(bytes.NewReader(mustRead(t, "testdata/"+name)),
				Options{ToYCbCr: true})
			if err != nil {
				t.Fatal(err)
			}

			if got := typeName(img); got != want {
				t.Errorf("got %s, want %s", got, want)
			}
		})
	}
}

func typeName(v any) string {
	switch v.(type) {
	case *image.YCbCr:
		return "*image.YCbCr"
	case *image.NYCbCrA:
		return "*image.NYCbCrA"
	case *image.Gray:
		return "*image.Gray"
	case *image.NRGBA:
		return "*image.NRGBA"
	case *image.NRGBA64:
		return "*image.NRGBA64"
	}

	return "unknown"
}

func TestSubsampleRatio(t *testing.T) {
	cases := map[string]image.YCbCrSubsampleRatio{
		"basic.heic":     image.YCbCrSubsampleRatio420,
		"chroma422.heic": image.YCbCrSubsampleRatio422,
		"chroma444.heic": image.YCbCrSubsampleRatio444,
	}

	for name, want := range cases {
		t.Run(name, func(t *testing.T) {
			img, err := Decode(bytes.NewReader(mustRead(t, "testdata/"+name)),
				Options{ToYCbCr: true})
			if err != nil {
				t.Fatal(err)
			}

			m, ok := img.(*image.YCbCr)
			if !ok {
				t.Fatalf("got %T", img)
			}

			if m.SubsampleRatio != want {
				t.Errorf("ratio %v, want %v", m.SubsampleRatio, want)
			}
		})
	}
}

func TestFrameSizeLimit(t *testing.T) {
	data := mustRead(t, "testdata/basic.heic")

	if _, err := Decode(bytes.NewReader(data), Options{FrameSizeLimit: 16}); err == nil {
		t.Error("a 320x240 image decoded under a 16 pixel limit")
	}

	if _, err := Decode(bytes.NewReader(data), Options{FrameSizeLimit: -1}); err != nil {
		t.Errorf("negative limit should remove it: %v", err)
	}
}

func TestInvalid(t *testing.T) {
	cases := map[string][]byte{
		"empty":     {},
		"truncated": mustRead(t, "testdata/basic.heic")[:64],
		"not heif":  []byte("this is not a container at all, not even close"),
	}

	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Decode(bytes.NewReader(data)); err == nil {
				t.Error("decoded successfully")
			}
		})
	}
}

func FuzzDecode(f *testing.F) {
	for _, name := range []string{"basic.heic", "alpha.heic", "chroma444.heic"} {
		b, err := os.ReadFile(filepath.Join("testdata", name))
		if err == nil {
			f.Add(b)
		}
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		img, err := Decode(bytes.NewReader(data))
		if err != nil {
			if !errors.Is(err, ErrInvalid) && !errors.Is(err, ErrUnsupported) {
				t.Fatalf("unexpected error type: %v", err)
			}

			return
		}

		if img.Bounds().Empty() {
			t.Fatal("decoded an empty image without error")
		}
	})
}

func TestDecodeAll(t *testing.T) {
	for _, f := range testdataFiles(t) {
		name := filepath.Base(f)

		t.Run(name, func(t *testing.T) {
			out, err := DecodeAll(bytes.NewReader(mustRead(t, f)))
			if err != nil {
				t.Fatal(err)
			}

			if len(out.Image) != 1 {
				t.Fatalf("a still gave %d frames", len(out.Image))
			}

			if len(out.Delay) != len(out.Image) {
				t.Errorf("%d delays for %d frames", len(out.Delay), len(out.Image))
			}
		})
	}
}

// TestRGBMatchesScalar checks the row kernels against the table-driven scalar
// path they replace, over every fixture, so an assembly or upsampling error
// cannot hide behind the planes-only digests.
func TestRGBMatchesScalar(t *testing.T) {
	for _, f := range testdataFiles(t) {
		name := filepath.Base(f)

		t.Run(name, func(t *testing.T) {
			data := mustRead(t, f)

			img, err := Decode(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}

			want, err := decodeScalarRGB(data)
			if err != nil {
				t.Fatal(err)
			}

			var got, ref []uint8

			switch m := img.(type) {
			case *image.NRGBA:
				got, ref = m.Pix, want.(*image.NRGBA).Pix
			case *image.NRGBA64:
				got, ref = m.Pix, want.(*image.NRGBA64).Pix
			default:
				t.Fatalf("got %T", img)
			}

			if len(got) != len(ref) {
				t.Fatalf("%d bytes, want %d", len(got), len(ref))
			}

			worst, count := 0, 0

			for i := range got {
				if d := int(got[i]) - int(ref[i]); d != 0 {
					count++

					if d < 0 {
						d = -d
					}

					if d > worst {
						worst = d
					}
				}
			}

			// The kernels and rgbRow evaluate the same arithmetic in a
			// different order, so a target that contracts a multiply and an
			// add into one rounding step can land a unit away.
			if worst > 1 {
				t.Errorf("%d of %d bytes differ, worst by %d", count, len(got), worst)
			}

			if count != 0 {
				t.Logf("%d of %d bytes differ by one", count, len(got))
			}
		})
	}
}

// decodeScalarRGB converts with rgbRow rather than the row kernels.
func decodeScalarRGB(data []byte) (image.Image, error) {
	f, err := parse(data)
	if err != nil {
		return nil, err
	}

	forceScalarRow = true
	defer func() { forceScalarRow = false }()

	img, _, err := f.decodeStill(Options{})

	return img, err
}

// countReader reports how much of a stream something actually consumed.
type countReader struct {
	r io.Reader
	n int64
}

func (c *countReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)

	return n, err
}

// TestDecodeConfigReadsHeaderOnly holds DecodeConfig to the boxes it needs.
// Everything it reports comes from ftyp, meta and moov, which sit ahead of the
// media data, so reaching the media data at all is the bug this catches.
func TestDecodeConfigReadsHeaderOnly(t *testing.T) {
	for _, f := range testdataFiles(t) {
		name := filepath.Base(f)

		t.Run(name, func(t *testing.T) {
			data := mustRead(t, f)

			c := &countReader{r: bytes.NewReader(data)}

			cfg, err := DecodeConfig(c)
			if err != nil {
				t.Fatalf("DecodeConfig: %v", err)
			}

			want, err := DecodeConfig(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}

			if cfg != want {
				t.Fatalf("config %+v, want %+v", cfg, want)
			}

			if c.n > int64(len(data))/2 {
				t.Errorf("read %d of %d bytes", c.n, len(data))
			}

			t.Logf("read %d of %d bytes (%.1f%%)", c.n, len(data),
				100*float64(c.n)/float64(len(data)))
		})
	}
}

// TestDecodeConfigStream checks a reader that is neither seekable nor readable
// at an offset, which is what image.DecodeConfig hands the decoder.
func TestDecodeConfigStream(t *testing.T) {
	for _, f := range testdataFiles(t) {
		name := filepath.Base(f)

		t.Run(name, func(t *testing.T) {
			data := mustRead(t, f)

			want, err := DecodeConfig(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}

			got, format, err := image.DecodeConfig(iotest.OneByteReader(bytes.NewReader(data)))
			if err != nil {
				t.Fatalf("image.DecodeConfig: %v", err)
			}

			if format != "heic" {
				t.Errorf("format %q, want heic", format)
			}

			if got != want {
				t.Errorf("config %+v, want %+v", got, want)
			}
		})
	}
}
