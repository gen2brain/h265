package heic

import (
	"math"

	"github.com/gen2brain/h265/hevc"
)

type track struct {
	id        uint32
	handler   string
	timescale uint32
	samples   []extent
	deltas    []uint32
	auxl      []uint32
	auxType   string
	hvcC      *hevcConfig
	width     int
	height    int

	duration   uint64
	hasEdits   bool
	repeating  bool
	segmentDur uint64
}

const indefiniteDuration = ^uint64(0)

// loopCount is zero for forever, -1 to show each frame once, otherwise the
// number of extra plays. ISO/IEC 23008-12 section 9.6.1.
func (t *track) loopCount() int {
	if !t.hasEdits {
		return 0
	}
	if !t.repeating {
		return -1
	}
	if t.duration == indefiniteDuration || t.duration == 0 || t.segmentDur == 0 {
		return 0
	}

	n := t.duration / t.segmentDur
	if t.duration%t.segmentDur != 0 {
		n++
	}
	if n <= 1 {
		return -1
	}
	if n-1 > math.MaxInt32 {
		return 0
	}

	return int(n - 1)
}

type movie struct {
	tracks []track
}

func parseMoov(b []byte) (*movie, error) {
	m := &movie{}

	err := eachBox(b, func(typ string, b []byte) error {
		if typ != "trak" {
			return nil
		}
		t, err := parseTrak(b)
		if err != nil {
			return err
		}
		m.tracks = append(m.tracks, t)

		return nil
	})
	if err != nil {
		return nil, err
	}

	return m, nil
}

func parseTrak(b []byte) (track, error) {
	var t track

	err := eachBox(b, func(typ string, b []byte) error {
		switch typ {
		case "tkhd":
			r := &reader{b: b}
			v, _ := r.fullBox()
			if v == 1 {
				r.skip(16)
			} else {
				r.skip(8)
			}
			t.id = r.u32()
			r.skip(4)
			if v == 1 {
				t.duration = r.u64()
			} else {
				t.duration = uint64(r.u32())
				if t.duration == 1<<32-1 {
					t.duration = indefiniteDuration
				}
			}
			if r.err {
				return ErrInvalid
			}

		case "edts":
			t.hasEdits = true

			return eachBox(b, func(typ string, b []byte) error {
				if typ != "elst" {
					return nil
				}
				r := &reader{b: b}
				v, flags := r.fullBox()
				if flags&1 == 0 {
					return nil
				}
				t.repeating = true
				if r.u32() != 1 {
					return nil
				}
				if v == 1 {
					t.segmentDur = r.u64()
				} else {
					t.segmentDur = uint64(r.u32())
				}
				if r.err {
					return ErrInvalid
				}

				return nil
			})

		case "tref":
			return eachBox(b, func(typ string, b []byte) error {
				if typ != "auxl" {
					return nil
				}
				r := &reader{b: b}
				for r.remaining() >= 4 {
					t.auxl = append(t.auxl, r.u32())
				}

				return nil
			})

		case "mdia":
			return eachBox(b, func(typ string, b []byte) error {
				switch typ {
				case "mdhd":
					r := &reader{b: b}
					v, _ := r.fullBox()
					if v == 1 {
						r.skip(16)
					} else {
						r.skip(8)
					}
					t.timescale = r.u32()
					if r.err {
						return ErrInvalid
					}

				case "hdlr":
					r := &reader{b: b}
					r.fullBox()
					r.u32()
					t.handler = r.str4()

				case "minf":
					return eachBox(b, func(typ string, b []byte) error {
						if typ != "stbl" {
							return nil
						}

						return t.parseStbl(b)
					})
				}

				return nil
			})
		}

		return nil
	})

	return t, err
}

type chunkRun struct {
	firstChunk, perChunk uint32
}

func (t *track) parseStbl(b []byte) error {
	var sizes []uint32
	var offsets []uint64
	var runs []chunkRun

	err := eachBox(b, func(typ string, b []byte) error {
		r := &reader{b: b}

		switch typ {
		case "stts":
			r.fullBox()
			n := int(r.u32())
			for range n {
				count := r.u32()
				delta := r.u32()
				if r.err || count > 1<<24 {
					return ErrInvalid
				}
				for range int(count) {
					t.deltas = append(t.deltas, delta)
				}
			}

		case "stsz":
			r.fullBox()
			uniform := r.u32()
			n := int(r.u32())
			if r.err || n < 0 || n > 1<<24 {
				return ErrInvalid
			}
			sizes = make([]uint32, n)
			for i := range n {
				if uniform != 0 {
					sizes[i] = uniform
				} else {
					sizes[i] = r.u32()
				}
			}

		case "stsd":
			r.fullBox()
			r.u32()

			return eachBox(b[r.i:], func(typ string, b []byte) error {
				if len(b) < 78 {
					return nil
				}

				rr := &reader{b: b}
				rr.skip(24)
				t.width = int(rr.u16())
				t.height = int(rr.u16())

				return eachBox(b[78:], func(typ string, b []byte) error {
					switch typ {
					case "auxi":
						rr := &reader{b: b}
						rr.fullBox()
						t.auxType = rr.cstr()

					case "hvcC":
						c, err := parseHvcC(&reader{b: b})
						if err != nil {
							return err
						}
						t.hvcC = c
					}

					return nil
				})
			})

		case "stsc":
			r.fullBox()
			n := int(r.u32())
			for range n {
				first := r.u32()
				per := r.u32()
				r.u32()
				if r.err {
					return ErrInvalid
				}
				runs = append(runs, chunkRun{first, per})
			}

		case "stco", "co64":
			r.fullBox()
			n := int(r.u32())
			if r.err || n < 0 || n > 1<<24 {
				return ErrInvalid
			}
			offsets = make([]uint64, n)
			for i := range n {
				if typ == "stco" {
					offsets[i] = uint64(r.u32())
				} else {
					offsets[i] = r.u64()
				}
			}
		}

		if r.err {
			return ErrInvalid
		}

		return nil
	})
	if err != nil {
		return err
	}

	t.samples = layoutSamples(sizes, offsets, runs)

	return nil
}

// layoutSamples walks the chunk table to give every sample a file offset.
func layoutSamples(sizes []uint32, offsets []uint64, runs []chunkRun) []extent {
	if len(sizes) == 0 || len(offsets) == 0 || len(runs) == 0 {
		return nil
	}

	out := make([]extent, 0, len(sizes))
	s := 0

	for c := range offsets {
		var per uint32
		for _, run := range runs {
			if uint32(c+1) < run.firstChunk {
				break
			}
			per = run.perChunk
		}

		off := offsets[c]
		for range int(per) {
			if s >= len(sizes) {
				return out
			}
			out = append(out, extent{off: off, len: uint64(sizes[s])})
			off += uint64(sizes[s])
			s++
		}
	}

	return out
}

func (m *movie) pictTrack() *track {
	for i := range m.tracks {
		if m.tracks[i].handler == "pict" && len(m.tracks[i].samples) > 0 {
			return &m.tracks[i]
		}
	}

	return nil
}

func (m *movie) alphaTrack(id uint32) *track {
	for i := range m.tracks {
		t := &m.tracks[i]
		if t.handler != "auxv" || len(t.samples) == 0 || !isAlphaURN(t.auxType) {
			continue
		}
		for _, to := range t.auxl {
			if to == id {
				return t
			}
		}
	}

	return nil
}

// decodeSequence decodes an image sequence track.
func (f *file) decodeSequence(o Options) (*HEIC, error) {
	t := f.movie.pictTrack()
	if t == nil || t.timescale == 0 {
		return nil, nil
	}

	colors, err := f.decodeTrack(t)
	if err != nil {
		return nil, err
	}

	var alphas []*hevc.Picture

	if at := f.movie.alphaTrack(t.id); at != nil {
		if alphas, err = f.decodeTrack(at); err != nil {
			return nil, err
		}
	}

	var first *hevc.Picture
	if len(colors) > 0 {
		first = colors[0]
	}

	out := &HEIC{LoopCount: t.loopCount(), Color: f.sequenceColor(first)}

	for i, pic := range colors {
		var alpha *hevc.Picture
		if i < len(alphas) {
			alpha = alphas[i]
		}

		img, err := toImage(pic, alpha, out.Color, o.ToYCbCr)
		if err != nil {
			return nil, err
		}

		if o.AutoRotate && f.meta != nil {
			if it, err := f.primary(); err == nil {
				if img, err = f.transform(it, img); err != nil {
					return nil, err
				}
			}
		}

		out.Image = append(out.Image, img)

		d := 0.0
		if i < len(t.deltas) {
			d = float64(t.deltas[i]) / float64(t.timescale)
		}

		out.Delay = append(out.Delay, d)
	}

	if len(out.Image) == 0 {
		return nil, nil
	}

	return out, nil
}

// sequenceColor takes the color description from the primary item when the
// file carries one, and otherwise from what the sequence itself declares.
func (f *file) sequenceColor(pic *hevc.Picture) ColorInfo {
	var it *item

	if f.meta != nil {
		it, _ = f.primary()
	}

	return f.colorInfo(it, pic)
}

func (f *file) decodeTrack(t *track) ([]*hevc.Picture, error) {
	if t.hvcC == nil {
		return nil, ErrInvalid
	}

	// A sample needs at least one byte, so a table claiming more samples than
	// the file has bytes is describing data that cannot exist.
	if uint64(len(t.samples)) > f.src.size {
		return nil, ErrInvalid
	}

	var d hevc.Decoder

	for _, nal := range t.hvcC.paramSets {
		u, ok := hevc.ParseNAL(nal)
		if !ok {
			return nil, ErrInvalid
		}

		if _, err := d.DecodeNAL(u); err != nil {
			return nil, wrap(err)
		}
	}

	var out []*hevc.Picture

	for _, s := range t.samples {
		if s.len == 0 {
			return nil, ErrInvalid
		}

		b, err := f.src.at(s.off, s.len)
		if err != nil {
			return nil, err
		}

		for _, u := range hevc.SplitHVCC(b, t.hvcC.lengthSize) {
			pics, err := d.DecodeNAL(u)
			if err != nil {
				return nil, wrap(err)
			}

			out = append(out, pics...)
		}
	}

	return append(out, d.Flush()...), nil
}
