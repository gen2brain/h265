package hevc

import (
	"bytes"
	"crypto/md5"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
)

func TestDecodeNALImmediateOutputAndInputLifetime(t *testing.T) {
	const width, height = 32, 32
	y := make([]byte, width*height)
	cb, cr := make([]byte, len(y)/4), make([]byte, len(y)/4)
	for i := range y {
		y[i] = byte(i*17 + i/13)
	}
	for i := range cb {
		cb[i], cr[i] = byte(i*29+7), byte(i*43+11)
	}
	nals, err := encodePCM(y, cb, cr, width, height)
	if err != nil {
		t.Fatal(err)
	}
	want := append(append(append([]byte{}, y...), cb...), cr...)
	var d Decoder
	defer d.Reset()

	var held *Picture
	for _, tag := range []uint64{0, 9} {
		var pics []*Picture
		for _, nal := range nals {
			nal.Tag = tag
			nal.RBSP = append([]byte(nil), nal.RBSP...)
			nal.EPB = append([]uint32(nil), nal.EPB...)
			out, err := d.DecodeNAL(nal)
			// The caller can reuse compressed input immediately, even while
			// decoded output remains owned by the decoder or its caller.
			clear(nal.RBSP)
			clear(nal.EPB)
			if err != nil {
				t.Fatal(err)
			}
			pics = append(pics, out...)
		}
		if len(pics) != 1 {
			t.Fatalf("complete zero-reorder picture returned %d pictures", len(pics))
		}
		p := pics[0]
		defer p.Release()
		if p.Tag != tag || !bytes.Equal(planarYUV(p), want) {
			t.Fatal("immediate output lost its tag or differs from the PCM input")
		}
		if held != nil && !bytes.Equal(planarYUV(held), want) {
			t.Fatal("decoding another picture changed an outstanding output")
		}
		held = p
	}
	if out := d.Flush(); len(out) != 0 {
		t.Fatalf("zero-reorder pictures were left waiting for Flush: %d", len(out))
	}
}

// The recorded FFmpeg digests validate pixels independently of the completion
// path. Slice-header POC values identify which input tag belongs to each output
// in these short fixtures, whose POC values do not wrap.
func TestDecodeNALSliceCompletionAndTags(t *testing.T) {
	manifest := referenceMD5(t)
	for _, name := range []string{
		"inter_p.h265", "multi_slice.h265", "dep_slices.h265",
		"multi_slice_sao_deblock_256x256.h265", "ctu64_wpp.h265",
		"bframes3_128x128.h265",
	} {
		for _, threads := range []int{1, 4} {
			t.Run(fmt.Sprintf("%s/threads%d", name, threads), func(t *testing.T) {
				units := accessUnitFixture(t, name)
				var d Decoder
				defer d.Reset()
				d.Threads(threads)
				h := md5.New()
				wantTags := make(map[int]uint64)
				seen := make(map[uint64]bool)
				lastTag := uint64(0)
				delayed, reordered := false, false
				consume := func(out []*Picture) {
					for _, p := range out {
						wantTag, ok := wantTags[p.POC]
						if !ok || p.Tag != wantTag || seen[p.Tag] {
							t.Fatalf("POC %d: tag=%d, want=%d; known=%v, duplicate=%v",
								p.POC, p.Tag, wantTag, ok, seen[p.Tag])
						}
						reordered = reordered || len(seen) > 0 && p.Tag < lastTag
						seen[p.Tag], lastTag = true, p.Tag
						h.Write(planarYUV(p))
						p.Release()
					}
				}
				for i, unit := range units {
					tag := uint64(101 + i)
					lastSlice := -1
					for j, nal := range unit {
						if nal.Type.IsVCL() {
							lastSlice = j
						}
					}
					outputs := 0
					for j, nal := range unit {
						// Only the first slice supplies the picture's identity.
						// Later slices and parameter/filler NALs must not retag it.
						nal.Tag = 9999
						if nal.Type.IsVCL() && nal.RBSP[0]&0x80 != 0 {
							nal.Tag = tag
							p, _, err := d.ppsForSlice(nal)
							if err != nil {
								t.Fatal(err)
							}
							sh, err := parseSliceHeader(nal.RBSP, nal.Type, d.sps[p.spsID], p)
							if err != nil {
								t.Fatal(err)
							}
							wantTags[int(sh.picOrderCntLsb)] = tag
						}
						nal.RBSP = append([]byte(nil), nal.RBSP...)
						nal.EPB = append([]uint32(nil), nal.EPB...)
						out, err := d.DecodeNAL(nal)
						clear(nal.RBSP)
						clear(nal.EPB)
						if err != nil {
							t.Fatalf("picture %d, NAL %d: %v", i, j, err)
						}
						if d.maxReorder == 0 && nal.Type.IsVCL() {
							want := 0
							if j == lastSlice {
								want = 1
							}
							if len(out) != want {
								t.Fatalf("picture %d, slice NAL %d: outputs=%d, want %d", i, j, len(out), want)
							}
						}
						outputs += len(out)
						consume(out)
						if nal.Type.IsVCL() && j < lastSlice {
							out, err := d.DecodeNAL(NALUnit{Type: NALFD, Tag: 9999, RBSP: []byte{0xff, 0x80}})
							if err != nil || len(out) != 0 {
								t.Fatalf("filler finished an incomplete picture: outputs=%d, err=%v", len(out), err)
							}
						}
					}
					delayed = delayed || outputs == 0
					out, err := d.DecodeNAL(NALUnit{Type: NALFD, Tag: 9999, RBSP: []byte{0xff, 0x80}})
					if err != nil || len(out) != 0 {
						t.Fatalf("filler produced output: pictures=%d, err=%v", len(out), err)
					}
				}
				consume(d.Flush())
				if len(seen) != len(units) {
					t.Fatalf("pictures=%d, want %d", len(seen), len(units))
				}
				if got, want := fmt.Sprintf("%x", h.Sum(nil)), manifest[name]; got != want {
					t.Fatalf("decoded digest=%s, FFmpeg=%s", got, want)
				}
				if name == "bframes3_128x128.h265" && (!delayed || !reordered) {
					t.Fatal("fixture did not exercise delayed and reordered output")
				}
			})
		}
	}
}

func TestDecodeNALIncompletePictures(t *testing.T) {
	unit := accessUnitFixture(t, "multi_slice.h265")[0]
	lastSlice := -1
	for i, nal := range unit {
		if nal.Type.IsVCL() {
			lastSlice = i
		}
	}
	for _, action := range []string{"flush", "next_picture", "reset"} {
		t.Run(action, func(t *testing.T) {
			var d Decoder
			defer d.Reset()
			out, err := decodeTaggedNALs(&d, unit[:lastSlice], 17)
			if err != nil || len(out) != 0 {
				t.Fatalf("incomplete picture: outputs=%d, err=%v", len(out), err)
			}
			switch action {
			case "flush":
				if out := d.Flush(); len(out) != 0 {
					t.Fatal("Flush emitted an incomplete picture")
				}
			case "next_picture":
				out, err = decodeTaggedNALs(&d, unit, 18)
				if !errors.Is(err, ErrInvalid) || len(out) != 0 {
					t.Fatalf("new picture after a lost slice: outputs=%d, err=%v", len(out), err)
				}
			case "reset":
				d.Reset()
			}
			out, err = decodeTaggedNALs(&d, unit, 23)
			if err != nil || len(out) != 1 || out[0].Tag != 23 {
				t.Fatalf("fresh random access: outputs=%d, err=%v", len(out), err)
			}
			out[0].Release()
		})
	}

	t.Run("missing_end_of_slice", func(t *testing.T) {
		var d Decoder
		defer d.Reset()
		nals := SplitAnnexB(mustRead(t, filepath.Join("testdata", "fuzz_bit_depth_change.h265")))
		out, err := decodeTaggedNALs(&d, nals, 17)
		if !errors.Is(err, ErrInvalid) || len(out) != 0 {
			t.Fatalf("outputs=%d, err=%v; want rejection without partial output", len(out), err)
		}
		if out := d.Flush(); len(out) != 0 {
			t.Fatal("a picture lacking its final termination flag was emitted later")
		}
	})
}

func TestDecodeNALOutputsSurviveError(t *testing.T) {
	first := accessUnitFixture(t, "bframes3_128x128.h265")[0]
	var d Decoder
	defer d.Reset()
	out, err := decodeTaggedNALs(&d, first, 11)
	if err != nil || len(out) != 0 || len(d.dpb) != 1 {
		t.Fatalf("initial reordered picture: outputs=%d, err=%v", len(out), err)
	}
	want := planarYUV(d.dpb[0].pic)

	// A new IDR releases the old complete picture before decoding its own
	// samples. Keep its header but remove CABAC data: the same DecodeNAL call
	// must return both the earlier picture and an error for the new picture.
	var broken NALUnit
	for _, nal := range first {
		if nal.Type.IsVCL() {
			p, _, err := d.ppsForSlice(nal)
			if err != nil {
				t.Fatal(err)
			}
			sh, err := parseSliceHeader(nal.RBSP, nal.Type, d.sps[p.spsID], p)
			if err != nil {
				t.Fatal(err)
			}
			broken = nal
			broken.RBSP = append([]byte(nil), nal.RBSP[:sh.dataOffset]...)
			broken.Tag = 12
			break
		}
	}
	out, err = d.DecodeNAL(broken)
	if !errors.Is(err, ErrInvalid) || len(out) != 1 {
		t.Fatalf("outputs=%d, err=%v; want prior output with error", len(out), err)
	}
	defer out[0].Release()
	if out[0].Tag != 11 || !bytes.Equal(planarYUV(out[0]), want) {
		t.Fatal("error reset invalidated or relabeled the earlier output")
	}
	if out := d.Flush(); len(out) != 0 {
		t.Fatal("failed picture or prior output was emitted again")
	}
}

func TestDecodeNALFrameSizeLimitSurvivesReset(t *testing.T) {
	unit := accessUnitFixture(t, "inter_p.h265")[0]
	var d Decoder
	defer d.Reset()
	d.FrameSizeLimit(1)
	d.Reset()
	// Both explicit Reset and automatic reset after a rejection must retain
	// the application's decoded-sample limit.
	for range 2 {
		out, err := decodeTaggedNALs(&d, unit, 1)
		if !errors.Is(err, ErrUnsupported) || len(out) != 0 {
			t.Fatalf("oversized picture: outputs=%d, err=%v", len(out), err)
		}
		if out := d.Flush(); len(out) != 0 {
			t.Fatal("oversized picture was emitted later")
		}
	}
	d.FrameSizeLimit(0)
	out, err := decodeTaggedNALs(&d, unit, 2)
	if err != nil || len(out) != 1 {
		t.Fatalf("lifting the limit: outputs=%d, err=%v", len(out), err)
	}
	out[0].Release()
}

func decodeTaggedNALs(d *Decoder, nals []NALUnit, tag uint64) ([]*Picture, error) {
	var out []*Picture
	for _, nal := range nals {
		nal.Tag = tag
		pics, err := d.DecodeNAL(nal)
		out = append(out, pics...)
		if err != nil {
			return out, err
		}
	}
	return out, nil
}

// Fixtures are already framed; their first-slice flags separate complete
// pictures while dependent and independent following slices stay together.
func accessUnitFixture(t *testing.T, name string) [][]NALUnit {
	t.Helper()
	return groupTestAccessUnits(SplitAnnexB(mustRead(t, filepath.Join("testdata", name))))
}

func groupTestAccessUnits(nals []NALUnit) [][]NALUnit {
	var out [][]NALUnit
	var cur []NALUnit
	seen := false
	for _, nal := range nals {
		if nal.Type.IsVCL() && nal.RBSP[0]&0x80 != 0 && seen {
			out = append(out, cur)
			cur, seen = nil, false
		}
		cur = append(cur, nal)
		seen = seen || nal.Type.IsVCL()
	}
	if seen {
		out = append(out, cur)
	}
	return out
}
