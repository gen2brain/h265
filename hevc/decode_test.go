package hevc

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"errors"
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
