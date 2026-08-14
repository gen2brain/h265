/*
Package heic decodes HEIF images that carry HEVC-coded item data, the format
commonly called HEIC.

# Color

[Decode] returns RGB, converted with the matrix and range the file declares in
its nclx color description. [Options.ToYCbCr] skips that and hands back the
planes the bitstream carries: *[image.YCbCr], *[image.NYCbCrA] with alpha, or
*[image.Gray] for monochrome. Above 8 bits there is no such image type, so
*[image.NRGBA64] is returned anyway.

[image.YCbCr] reads its planes as full-range BT.601 whatever the file signals,
which is rarely what a HEIC file means. [DecodeColor] reports what they
actually are, so ToYCbCr is for reaching the samples rather than for display:

	img, ci, err := heic.DecodeColor(r, heic.Options{ToYCbCr: true})

[ColorInfo] carries the CICP code points and the range flag, plus the ICC
profile when the file has one. Matrix and FullRange are what the conversion to
RGB uses. Primaries and Transfer are reported but not applied, so RGB output
stays in the file's own color space.

# Metadata

[DecodeExif] reads the Exif item a file describes its image with, and
[RawExif] and [RawXMP] return the payloads unparsed.
*/
package heic

import (
	"errors"
	"image"
	"image/color"
	"io"
	"runtime"

	"github.com/gen2brain/h265/hevc"
)

// ErrUnsupported is returned for a file this package cannot render but which
// is otherwise well formed: an essential property it does not implement, or a
// sample format it has no conversion for. A caller that has another decoder to
// fall back on should test for this one rather than [ErrInvalid].
var ErrUnsupported = errors.New("heic: unsupported image")

// DefaultFrameSizeLimit bounds the pixel area a header may ask to allocate.
const DefaultFrameSizeLimit = 16384 * 16384

// ColorInfo describes the color space an image was decoded from.
type ColorInfo struct {
	Primaries uint16
	Transfer  uint16
	Matrix    uint16
	FullRange bool
	// ICCP is the embedded ICC profile, for files that carry one in place of
	// an nclx description. It aliases the input, so it is not a copy.
	ICCP []byte
}

// Options controls decoding.
type Options struct {
	// AutoRotate applies the clap/irot/imir transforms, forcing NRGBA output
	// when it transforms.
	AutoRotate bool
	// FrameSizeLimit bounds a frame's area in pixels. Zero means
	// DefaultFrameSizeLimit; a negative value removes the limit.
	FrameSizeLimit int
	// ToYCbCr forces the image's native color space instead of NRGBA:
	// *image.YCbCr, *image.NYCbCrA when there is alpha, or *image.Gray when
	// the image is monochrome. Above 8 bits NRGBA64 is returned anyway.
	// image.YCbCr reads the planes as full-range BT.601 whatever the file
	// signals, so this is for reaching the samples, not for display.
	// DecodeColor reports what the samples actually are.
	ToYCbCr bool
	// Threads bounds the goroutines a decode may use, over the tiles of a grid
	// and the wavefront rows within each. Zero means GOMAXPROCS; one decodes
	// serially.
	Threads int
}

func options(opts []Options) Options {
	if len(opts) == 0 {
		return Options{}
	}

	return opts[0]
}

type file struct {
	src            *source
	meta           *metaBox
	movie          *movie
	frameSizeLimit int
	threads        int
}

// workers is how many goroutines a grid may use, never more than it has tiles.
func (f *file) workers(n int) int {
	w := f.threads
	if w == 0 {
		w = runtime.GOMAXPROCS(0)
	}

	if n <= 0 {
		return max(w, 1)
	}

	return max(min(w, n), 1)
}

// HEIC holds the images of a file, which may be an image sequence.
type HEIC struct {
	// Image holds the decoded frames, *image.NRGBA or *image.NRGBA64.
	Image []image.Image
	// Delay holds each frame's duration in seconds.
	Delay []float64
	// LoopCount controls how many times the animation restarts, following
	// image/gif: zero loops forever, -1 shows each frame once, and any other
	// value plays the animation LoopCount+1 times.
	LoopCount int
	// Color describes the color space the frames were decoded from.
	Color ColorInfo
}

func parse(src *source) (*file, error) {
	f := &file{src: src}

	seen := false

	err := src.eachBox(func(typ string, off, n uint64) error {
		// Only these carry anything parse needs, so the media data is never
		// read here: the items that reference it are read on demand.
		switch typ {
		case "ftyp", "meta", "moov":
		case "mini":
			// The MinimizedImageBox of the low overhead profile carries the
			// whole description in place of meta, so a file built on it is one
			// we can read nothing from rather than a malformed one.
			return ErrUnsupported
		default:
			return nil
		}

		b, err := src.at(off, n)
		if err != nil {
			return err
		}

		switch typ {
		case "ftyp":
			seen = true
		case "meta":
			if f.meta != nil {
				return nil
			}

			m, err := parseMeta(b)
			if err != nil {
				return err
			}

			f.meta = m

		case "moov":
			if f.movie != nil {
				return nil
			}

			mv, err := parseMoov(b)
			if err != nil {
				return err
			}

			f.movie = mv
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	if !seen || (f.meta == nil && f.movie == nil) {
		return nil, ErrInvalid
	}

	return f, nil
}

// srcFor addresses the file by range when the reader allows it, so only the
// items a decode reaches are read. Anything else is buffered whole, which is
// what image.Decode leaves us with: it hands the decoder a bufio.Reader.
func srcFor(r io.Reader) (*source, error) {
	ra, raOK := r.(io.ReaderAt)
	sk, skOK := r.(io.Seeker)

	if raOK && skOK {
		cur, err1 := sk.Seek(0, io.SeekCurrent)
		end, err2 := sk.Seek(0, io.SeekEnd)

		if err1 == nil && err2 == nil && end > cur {
			n := end - cur

			return &source{r: io.NewSectionReader(ra, cur, n), size: uint64(n)}, nil
		}
	}

	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}

	return memSource(data), nil
}

// parseHeader reads only the boxes a configuration needs and stops as soon as
// one can be derived, so a stream that cannot be addressed by range still
// costs no more than its header.
func parseHeader(r io.Reader) (*file, error) {
	f := &file{}

	seen := false

	err := eachBoxReader(r, func(typ string, n int64, body io.Reader) error {
		switch typ {
		case "ftyp":
			seen = true

		case "meta":
			if f.meta != nil {
				return nil
			}

			b, err := boxBytes(body, n)
			if err != nil {
				return err
			}

			m, err := parseMeta(b)
			if err != nil {
				return err
			}

			f.meta = m

		case "moov":
			if f.movie != nil {
				return nil
			}

			b, err := boxBytes(body, n)
			if err != nil {
				return err
			}

			mv, err := parseMoov(b)
			if err != nil {
				return err
			}

			f.movie = mv

			return nil

		default:
			return nil
		}

		// Only a configuration from the primary item ends the walk. A picture
		// track is the fallback for a file that has no usable image item, and
		// a meta box after moov would still outrank it.
		if seen {
			if _, err := f.config(); err == nil {
				return errStop
			}
		}

		return nil
	})
	if err != nil && !errors.Is(err, errStop) {
		return nil, err
	}

	if !seen || (f.meta == nil && f.movie == nil) {
		return nil, ErrInvalid
	}

	return f, nil
}

// config is the image configuration of the primary item, or of the picture
// track when a file carries no image item.
func (f *file) config() (image.Config, error) {
	it, err := f.primary()
	if err != nil {
		if f.movie == nil {
			return image.Config{}, err
		}

		return f.sequenceConfig()
	}

	w, h, err := f.size(it)
	if err != nil {
		return image.Config{}, err
	}

	return image.Config{Width: w, Height: h, ColorModel: colorModelFor(f, it)}, nil
}

func (f *file) limit() int {
	switch {
	case f.frameSizeLimit < 0:
		return 0
	case f.frameSizeLimit == 0:
		return DefaultFrameSizeLimit
	}

	return f.frameSizeLimit
}

// primary is the item a file describes itself with.
func (f *file) primary() (*item, error) {
	if f.meta == nil {
		return nil, ErrInvalid
	}

	it := f.meta.items[f.meta.primary]
	if it == nil {
		return nil, ErrInvalid
	}

	if it.unsupported {
		return nil, ErrUnsupported
	}

	return it, nil
}

// alphaOf finds the auxiliary item that carries this item's alpha channel.
func (f *file) alphaOf(id uint32) *item {
	for _, r := range f.meta.refs {
		if r.typ != "auxl" || len(r.to) == 0 || r.to[0] != id {
			continue
		}

		it := f.meta.items[r.from]
		if it == nil || it.unsupported {
			continue
		}

		if p := f.meta.prop(it, "auxC"); p != nil && isAlphaURN(p.auxC) {
			return it
		}
	}

	return nil
}

func isAlphaURN(s string) bool {
	return s == "urn:mpeg:mpegB:cicp:systems:auxiliary:alpha" ||
		s == "urn:mpeg:hevc:2015:auxid:1"
}

// itemDecoder carries the decoder across the tiles of a grid, which keeps the
// per-picture buffers allocated once, together with the configuration already
// fed to it so the tiles after the first skip the parameter sets they share.
type itemDecoder struct {
	d   hevc.Decoder
	cfg *hevcConfig
}

// use sets how many goroutines this decoder's wavefront may take.
func (dec *itemDecoder) use(threads int) *itemDecoder {
	dec.d.Threads(threads)

	return dec
}

func (f *file) decodeItem(dec *itemDecoder, it *item) (*hevc.Picture, error) {
	if it.typ == "grid" {
		return nil, ErrUnsupported
	}

	if it.typ != "hvc1" && it.typ != "hvc2" {
		return nil, ErrUnsupported
	}

	cfg := f.meta.prop(it, "hvcC")
	if cfg == nil || cfg.hvcC == nil {
		return nil, ErrInvalid
	}

	data, err := f.meta.data(it, f.src)
	if err != nil {
		return nil, err
	}

	if n := f.limit(); n > 0 {
		if p := f.meta.prop(it, "ispe"); p != nil && int(p.w)*int(p.h) > n {
			return nil, ErrUnsupported
		}
	}

	if dec.cfg != cfg.hvcC {
		for _, nal := range cfg.hvcC.paramSets {
			u, ok := hevc.ParseNAL(nal)
			if !ok {
				return nil, ErrInvalid
			}

			if _, err := dec.d.DecodeNAL(u); err != nil {
				return nil, wrap(err)
			}
		}

		dec.cfg = cfg.hvcC
	}

	var out []*hevc.Picture

	for _, u := range hevc.SplitHVCC(data, cfg.hvcC.lengthSize) {
		pics, err := dec.d.DecodeNAL(u)
		if err != nil {
			return nil, wrap(err)
		}

		out = append(out, pics...)
	}

	out = append(out, dec.d.Flush()...)

	if len(out) == 0 {
		return nil, ErrInvalid
	}

	return out[0], nil
}

func wrap(err error) error {
	if errors.Is(err, hevc.ErrUnsupported) {
		return ErrUnsupported
	}

	return ErrInvalid
}

// decodeStill decodes the primary item, its alpha, and any grid it derives
// from, and converts the result.
func (f *file) decodeStill(o Options) (image.Image, ColorInfo, error) {
	it, err := f.primary()
	if err != nil {
		if f.movie == nil {
			return nil, ColorInfo{}, err
		}

		seq, serr := f.decodeSequence(o)
		if serr != nil {
			return nil, ColorInfo{}, serr
		}

		if seq == nil {
			return nil, ColorInfo{}, err
		}

		return seq.Image[0], seq.Color, nil
	}

	pic, err := f.decodeImage(it)
	if err != nil {
		return nil, ColorInfo{}, err
	}

	// ISO/IEC 23008-12 7.2.1: ispe is the displayed size.
	f.clampToISPE(it, pic)

	var alpha *hevc.Picture

	// A grid carries no alpha of its own; its tiles do.
	if it.typ == "grid" {
		alpha, err = f.gridAlpha(it)
	} else if a := f.alphaOf(it.id); a != nil {
		alpha, err = f.decodeImage(a)
	}

	if err != nil {
		return nil, ColorInfo{}, err
	}

	if alpha != nil {
		f.clampToISPE(it, alpha)
	}

	ci := f.colorInfo(it, pic)

	img, err := toImage(pic, alpha, ci, o.ToYCbCr)
	if err != nil {
		return nil, ci, err
	}

	if o.AutoRotate {
		img, err = f.transform(it, img)
		if err != nil {
			return nil, ci, err
		}
	}

	return img, ci, nil
}

// Decode reads a HEIC image as *image.NRGBA, or *image.NRGBA64 above 8 bits.
func Decode(r io.Reader, opts ...Options) (image.Image, error) {
	img, _, err := decode(r, opts...)

	return img, err
}

// DecodeColor is Decode, and also reports the color space the image was
// decoded from.
func DecodeColor(r io.Reader, opts ...Options) (image.Image, ColorInfo, error) {
	return decode(r, opts...)
}

func decode(r io.Reader, opts ...Options) (image.Image, ColorInfo, error) {
	src, err := srcFor(r)
	if err != nil {
		return nil, ColorInfo{}, err
	}

	f, err := parse(src)
	if err != nil {
		return nil, ColorInfo{}, err
	}

	o := options(opts)
	f.frameSizeLimit = o.FrameSizeLimit
	f.threads = o.Threads

	return f.decodeStill(o)
}

// DecodeAll returns every frame of an image sequence with its duration, and
// how many times the animation repeats. A still image gives one frame.
func DecodeAll(r io.Reader, opts ...Options) (*HEIC, error) {
	src, err := srcFor(r)
	if err != nil {
		return nil, err
	}

	f, err := parse(src)
	if err != nil {
		return nil, err
	}

	o := options(opts)
	f.frameSizeLimit = o.FrameSizeLimit
	f.threads = o.Threads

	if f.movie != nil {
		seq, err := f.decodeSequence(o)
		if err != nil {
			return nil, err
		}

		if seq != nil {
			return seq, nil
		}
	}

	img, ci, err := f.decodeStill(o)
	if err != nil {
		return nil, err
	}

	return &HEIC{Image: []image.Image{img}, Delay: []float64{0}, Color: ci}, nil
}

// DecodeConfig returns the dimensions and color model without decoding the
// image data.
func DecodeConfig(r io.Reader) (image.Config, error) {
	f, err := parseHeader(r)
	if err != nil {
		return image.Config{}, err
	}

	return f.config()
}

// sequenceConfig reads the dimensions from the sample entry of the picture
// track, for a file that carries no image item.
func (f *file) sequenceConfig() (image.Config, error) {
	t := f.movie.pictTrack()
	if t == nil || t.hvcC == nil || t.width == 0 || t.height == 0 {
		return image.Config{}, ErrInvalid
	}

	model := color.NRGBAModel
	if t.hvcC.bitDepthLuma > 8 {
		model = color.NRGBA64Model
	}

	return image.Config{Width: t.width, Height: t.height, ColorModel: model}, nil
}

// clampToISPE trims a decoded picture to the size the item declares.
func (f *file) clampToISPE(it *item, pic *hevc.Picture) {
	p := f.meta.prop(it, "ispe")
	if p == nil {
		return
	}

	pic.CropW = min(pic.CropW, int(p.w))
	pic.CropH = min(pic.CropH, int(p.h))
}

// size is the stored size of an item, which is what Decode returns unless
// AutoRotate transforms it.
func (f *file) size(it *item) (int, int, error) {
	p := f.meta.prop(it, "ispe")
	if p == nil {
		return 0, 0, ErrInvalid
	}

	if p.w == 0 || p.h == 0 || p.w > 1<<20 || p.h > 1<<20 {
		return 0, 0, ErrInvalid
	}

	return int(p.w), int(p.h), nil
}

func decodeWrapper(r io.Reader) (image.Image, error) {
	return Decode(r)
}

func init() {
	for _, brand := range []string{
		"heic", "heix", "heim", "heis", "hevc", "hevx", "hevm", "hevs",
		"mif1", "msf1",
	} {
		image.RegisterFormat("heic", "????ftyp"+brand, decodeWrapper, DecodeConfig)
	}
}
