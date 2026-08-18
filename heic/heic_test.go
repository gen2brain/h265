package heic

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"image"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"testing/iotest"

	"github.com/gen2brain/h265/hevc"
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

func TestEncode(t *testing.T) {
	src := image.NewYCbCr(image.Rect(0, 0, 32, 32), image.YCbCrSubsampleRatio420)
	for i := range src.Y {
		src.Y[i] = byte(i*17 + i/13)
	}
	for i := range src.Cb {
		src.Cb[i] = byte(i*29 + 7)
		src.Cr[i] = byte(i*43 + 11)
	}

	data, err := encodeToBytes(src, EncodeOptions{Lossless: true})
	if err != nil {
		t.Fatal(err)
	}

	img, err := Decode(bytes.NewReader(data), Options{ToYCbCr: true})
	if err != nil {
		t.Fatal(err)
	}

	got, ok := img.(*image.YCbCr)
	if !ok {
		t.Fatalf("image = %T", img)
	}
	if !bytes.Equal(got.Y, src.Y) || !bytes.Equal(got.Cb, src.Cb) || !bytes.Equal(got.Cr, src.Cr) {
		t.Fatal("encoded planes differ")
	}

	lossy, err := encodeToBytes(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(lossy) >= len(data) {
		t.Fatalf("lossy = %d bytes, lossless = %d", len(lossy), len(data))
	}

	worse, err := encodeToBytes(src, EncodeOptions{Quality: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(worse) >= len(lossy) {
		t.Fatalf("quality 10 = %d bytes, quality %d = %d", len(worse), DefaultQuality, len(lossy))
	}

	if _, err := Decode(bytes.NewReader(lossy)); err != nil {
		t.Fatal(err)
	}
}

func encodeToBytes(img image.Image, opts ...EncodeOptions) ([]byte, error) {
	var b bytes.Buffer
	err := Encode(&b, img, opts...)

	return b.Bytes(), err
}

// TestEncodeExternal holds the written container to libheif rather than to our
// own reader, which is lenient about things a file should not do: a zero extent
// length reads as the rest of the file here and as nothing there, and a file
// with no colour description at all is anyone's guess.
func TestEncodeExternal(t *testing.T) {
	if _, err := exec.LookPath("heif-dec"); err != nil {
		t.Skip("heif-dec not installed")
	}

	for _, size := range [][2]int{{32, 32}, {176, 144}} {
		width, height := size[0], size[1]

		src := image.NewYCbCr(image.Rect(0, 0, width, height), image.YCbCrSubsampleRatio420)
		for i := range src.Y {
			src.Y[i] = byte(i*17 + i/13)
		}

		for i := range src.Cb {
			src.Cb[i] = byte(i*29 + 7)
			src.Cr[i] = byte(i*43 + 11)
		}

		data, err := encodeToBytes(src, EncodeOptions{Lossless: true})
		if err != nil {
			t.Fatal(err)
		}

		dir := t.TempDir()

		in := filepath.Join(dir, "encoded.heic")
		if err := os.WriteFile(in, data, 0o644); err != nil {
			t.Fatal(err)
		}

		out := filepath.Join(dir, "decoded.y4m")

		cmd := exec.Command("heif-dec", "--quiet", in, out)
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%dx%d: heif-dec: %v: %s", width, height, err, b)
		}

		planes, err := y4mPlanes(out)
		if err != nil {
			t.Fatalf("%dx%d: %v", width, height, err)
		}

		want := append(append(append([]byte{}, src.Y...), src.Cb...), src.Cr...)
		if !bytes.Equal(planes, want) {
			t.Fatalf("%dx%d: libheif decoded %d bytes, want %d equal", width, height, len(planes), len(want))
		}
	}
}

// y4mPlanes returns the sample data of the one frame in a YUV4MPEG2 file.
func y4mPlanes(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	for range 2 {
		i := bytes.IndexByte(b, '\n')
		if i < 0 {
			return nil, errors.New("truncated y4m")
		}

		b = b[i+1:]
	}

	return b, nil
}

func BenchmarkEncode(b *testing.B) {
	const width, height = 176, 144

	src := image.NewYCbCr(image.Rect(0, 0, width, height), image.YCbCrSubsampleRatio420)
	for i := range src.Y {
		src.Y[i] = byte(i*17 + i/13)
	}
	for i := range src.Cb {
		src.Cb[i] = byte(i*29 + 7)
		src.Cr[i] = byte(i*43 + 11)
	}

	b.ReportAllocs()
	b.SetBytes(width * height * 3 / 2)
	b.ResetTimer()

	for b.Loop() {
		if _, err := encodeToBytes(src); err != nil {
			b.Fatal(err)
		}
	}
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

// TestErrorPrefix pins the package name every error names itself with. The Exif
// reader was ported from Projects/gav1d and said "avif" for a long time.
func TestErrorPrefix(t *testing.T) {
	errs := []error{ErrInvalid, ErrUnsupported, ErrNoExif, ErrNoXMP}

	for _, data := range [][]byte{
		{},
		{'X', 'X', 0, 42, 0, 0, 0, 8},
		{'I', 'I', 43, 0, 8, 0, 0, 0},
		{'I', 'I', 42, 0, 1, 0, 0, 0},
	} {
		var exif Exif

		err := parseExifData(data, &exif)
		if err == nil {
			t.Fatalf("%x: no error", data)
		}

		errs = append(errs, err)
	}

	for _, err := range errs {
		if !strings.HasPrefix(err.Error(), "heic: ") {
			t.Errorf("error %q does not name the package", err)
		}
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
		// One goroutine, so a failure reproduces, and a small picture limit so
		// a mutated ispe cannot ask for one that takes seconds to fill.
		img, err := Decode(bytes.NewReader(data), Options{FrameSizeLimit: 1 << 16, Threads: 1})
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
	f, err := parse(memSource(data))
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

func pixOf(t *testing.T, img image.Image) []byte {
	t.Helper()

	switch m := img.(type) {
	case *image.NRGBA:
		return m.Pix
	case *image.NRGBA64:
		return m.Pix
	}

	t.Fatalf("decoded %T, want NRGBA or NRGBA64", img)

	return nil
}

// TestSourcePaths decodes through both forms of source, since srcFor picks
// between reading ranges and buffering on what the reader implements.
func TestSourcePaths(t *testing.T) {
	for _, f := range testdataFiles(t) {
		name := filepath.Base(f)

		t.Run(name, func(t *testing.T) {
			data := mustRead(t, f)

			// A bytes.Reader is a ReaderAt and a Seeker, so this reads ranges.
			ranged, err := Decode(bytes.NewReader(data))
			if err != nil {
				t.Fatalf("ranged: %v", err)
			}

			// A OneByteReader is neither, so this buffers the whole file.
			buffered, err := Decode(iotest.OneByteReader(bytes.NewReader(data)))
			if err != nil {
				t.Fatalf("buffered: %v", err)
			}

			if !bytes.Equal(pixOf(t, ranged), pixOf(t, buffered)) {
				t.Error("ranged and buffered decodes differ")
			}
		})
	}
}

// TestDecodeFile decodes from an os.File, which is the reader the ranged path
// exists for.
func TestDecodeFile(t *testing.T) {
	for _, name := range testdataFiles(t) {
		t.Run(filepath.Base(name), func(t *testing.T) {
			want, err := Decode(bytes.NewReader(mustRead(t, name)))
			if err != nil {
				t.Fatal(err)
			}

			fh, err := os.Open(name)
			if err != nil {
				t.Fatal(err)
			}

			defer fh.Close()

			got, err := Decode(fh)
			if err != nil {
				t.Fatalf("from file: %v", err)
			}

			if !bytes.Equal(pixOf(t, got), pixOf(t, want)) {
				t.Error("decode from a file differs")
			}
		})
	}
}

// TestSourceConcurrent reads overlapping ranges at once, which is what a grid
// does with one goroutine per tile.
func TestSourceConcurrent(t *testing.T) {
	data := make([]byte, 64<<10)
	for i := range data {
		data[i] = byte(i * 7)
	}

	src := &source{r: bytes.NewReader(data), size: uint64(len(data))}

	var wg sync.WaitGroup

	for i := range 16 {
		wg.Add(1)

		go func() {
			defer wg.Done()

			off := uint64(i * 512)

			for range 32 {
				b, err := src.at(off, 4096)
				if err != nil {
					t.Error(err)

					return
				}

				if !bytes.Equal(b, data[off:off+4096]) {
					t.Errorf("range at %d differs", off)

					return
				}
			}
		}()
	}

	wg.Wait()
}

// heicCorpusBaseline is how many files of each set decode today. Raise one
// whenever the tally improves, so a regression fails rather than passing
// quietly.
var heicCorpusBaseline = map[string]int{
	"valid/nokia-conformance": 55,
	"valid/dsoprea-exif":      4,
	"valid/libheif-testdata":  1,
}

// TestConformanceCorpus decodes a corpus of HEIF files that lives outside the
// repository. CONFORMANCE_DIR is a colon separated list of corpora; every one
// of them is searched, and the ones holding no HEIF contribute nothing.
//
// It holds two rules beyond the tally. Nothing may panic, whatever the file,
// and nothing under valid/ may be reported as malformed: a file this package
// cannot render is ErrUnsupported, which is what tells a caller to reach for
// another decoder rather than to distrust the file.
func TestConformanceCorpus(t *testing.T) {
	env := os.Getenv("CONFORMANCE_DIR")
	if env == "" {
		t.Skip("set CONFORMANCE_DIR")
	}

	type entry struct{ root, path string }

	var files []entry

	for _, root := range strings.Split(env, ":") {
		filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
			if err != nil || fi.IsDir() {
				return nil
			}

			switch strings.ToLower(filepath.Ext(p)) {
			case ".heic", ".heif", ".hif", ".avif":
				files = append(files, entry{root, p})
			}

			return nil
		})
	}

	if len(files) == 0 {
		t.Skip("no HEIF corpus in CONFORMANCE_DIR")
	}

	sort.Slice(files, func(i, j int) bool { return files[i].path < files[j].path })

	ok := map[string]int{}
	unsupported := map[string]int{}
	sets := map[string]bool{}

	for _, f := range files {
		p := f.path

		rel, err := filepath.Rel(f.root, p)
		if err != nil {
			t.Fatal(err)
		}

		set := filepath.ToSlash(filepath.Dir(rel))
		sets[set] = true

		t.Run(filepath.ToSlash(rel), func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("panic: %v", r)
				}
			}()

			fh, err := os.Open(p)
			if err != nil {
				t.Fatal(err)
			}

			defer fh.Close()

			_, err = Decode(fh)

			switch {
			case err == nil:
				ok[set]++
			case errors.Is(err, ErrUnsupported):
				unsupported[set]++
			case strings.HasPrefix(set, "valid/"):
				t.Errorf("a valid file failed as %v", err)
			}
		})
	}

	// A baseline holds only for a corpus that is actually present, so pointing
	// CONFORMANCE_DIR at some other collection reports rather than fails.
	for set, want := range heicCorpusBaseline {
		if sets[set] && ok[set] < want {
			t.Errorf("%s: %d decoded, baseline is %d", set, ok[set], want)
		}
	}

	names := make([]string, 0, len(sets))
	for set := range sets {
		names = append(names, set)
	}

	sort.Strings(names)

	for _, set := range names {
		t.Logf("%-28s decoded=%-4d unsupported=%d", set, ok[set], unsupported[set])
	}
}

// TestColorInfoPrecedence holds the rule of ISO/IEC 23008-12: the sequence
// declares a color description in its video usability information, and a colr
// box replaces it whole. An animated HEIC often carries no colr at all, which
// is what made the bitstream's own description worth reading.
func TestColorInfoPrecedence(t *testing.T) {
	pic := &hevc.Picture{ColorPrimaries: 1, ColorTransfer: 13, ColorMatrix: 6, FullRange: true}

	same := func(t *testing.T, got, want ColorInfo) {
		t.Helper()

		if got.Primaries != want.Primaries || got.Transfer != want.Transfer ||
			got.Matrix != want.Matrix || got.FullRange != want.FullRange {
			t.Errorf("got %+v, want %+v", got, want)
		}
	}

	t.Run("no meta", func(t *testing.T) {
		f := &file{}

		same(t, f.colorInfo(nil, pic),
			ColorInfo{Primaries: 1, Transfer: 13, Matrix: 6, FullRange: true})
	})

	t.Run("no picture", func(t *testing.T) {
		f := &file{meta: &metaBox{}}

		same(t, f.colorInfo(nil, nil), ColorInfo{Primaries: 2, Transfer: 2, Matrix: mcUnspec})
	})

	t.Run("colr replaces it", func(t *testing.T) {
		it := &item{id: 1, props: []itemProp{{idx: 0}}}

		f := &file{meta: &metaBox{
			primary: 1,
			items:   map[uint32]*item{1: it},
			order:   []uint32{1},
			props: []property{{
				typ:  "colr",
				colr: &colorInfo{hasNCLX: true, primaries: 9, transfer: 16, matrix: 9},
			}},
		}}

		same(t, f.colorInfo(it, pic), ColorInfo{Primaries: 9, Transfer: 16, Matrix: 9})
	})
}
