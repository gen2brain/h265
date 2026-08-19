package heic

import (
	"encoding/binary"
	"image"
	"io"
	"math"

	"github.com/gen2brain/h265/hevc"
)

// DefaultQuality is the quality Encode uses when none is given.
const DefaultQuality = 60

// The written planes are what image.YCbCr holds, which is full range BT.601
// carrying sRGB primaries and transfer.
const (
	encPrimaries = 1
	encTransfer  = 13
	encMatrix    = 6
)

// alphaURN is the auxiliary type of ISO/IEC 23008-12 Annex F, which names an
// item as the alpha channel of the picture it is tied to.
const alphaURN = "urn:mpeg:hevc:2015:auxid:1"

// maxBoxOverhead bounds the header bytes before the sample data, so a file
// needing 64 bit box sizes is refused rather than truncated.
const maxBoxOverhead = 1 << 16

// ChromaFormat is the chroma sampling [Encode] writes. The zero value is 4:2:0.
type ChromaFormat = hevc.ChromaFormat

const (
	Chroma420  = hevc.Chroma420
	Chroma422  = hevc.Chroma422
	Chroma444  = hevc.Chroma444
	ChromaGray = hevc.ChromaMono
)

// EncodeOptions are the encoding parameters.
type EncodeOptions struct {
	// Quality in the range [0,100]. Default is DefaultQuality.
	Quality int
	// Lossless codes the samples as PCM and ignores Quality.
	Lossless bool
	// Chroma is the sampling to code in. The zero value is 4:2:0.
	Chroma ChromaFormat
	// BitDepth is the sample size, 8 through 12. The zero value is 8.
	BitDepth int
	// Exif is a TIFF payload, written as an Exif item describing the picture.
	Exif []byte
	// XMP is an XMP packet, written as a metadata item describing the picture.
	XMP []byte
}

// ratioOf is the image.YCbCr subsampling that matches the coded one. Without
// chroma nothing constrains the stored size, which is what 4:4:4 also says.
func ratioOf(c ChromaFormat) image.YCbCrSubsampleRatio {
	switch c {
	case Chroma422:
		return image.YCbCrSubsampleRatio422
	case Chroma444, ChromaGray:
		return image.YCbCrSubsampleRatio444
	default:
		return image.YCbCrSubsampleRatio420
	}
}

// Encode writes img to w as a HEIC still. Any image will do; one that is not
// already in [EncodeOptions.Chroma] as an *image.YCbCr is converted. An image
// that carries alpha keeps it, in a monochrome auxiliary item coded at the
// same quality.
func Encode(w io.Writer, img image.Image, opts ...EncodeOptions) error {
	var o EncodeOptions

	if len(opts) > 0 {
		o = opts[0]
	}

	switch {
	case o.Quality <= 0:
		o.Quality = DefaultQuality
	case o.Quality > 100:
		o.Quality = 100
	}

	if o.Chroma < Chroma420 || o.Chroma > ChromaGray {
		return ErrUnsupported
	}

	if o.BitDepth == 0 {
		o.BitDepth = 8
	}

	if o.BitDepth < 8 || o.BitDepth > 12 {
		return ErrUnsupported
	}

	b := img.Bounds()
	width, height := b.Dx(), b.Dy()
	sw, sh := ratioSub(ratioOf(o.Chroma))
	even := image.Rect(0, 0, roundUp(width, sw), roundUp(height, sh))

	qp := 51 - o.Quality*50/100
	deep := o.BitDepth > 8

	var (
		frame  hevc.Frame
		alpha  []uint8
		alpha6 []uint16
	)

	switch {
	case deep:
		alpha6 = alphaDeep(img, even, o.BitDepth)
		_, y, cb, cr := toDeep(img, ratioOf(o.Chroma), o.BitDepth, alpha6 != nil)
		frame = hevc.Frame{Y16: y, StrideY: even.Dx()}

		if o.Chroma != ChromaGray {
			frame.Cb16, frame.Cr16, frame.StrideC = cb, cr, even.Dx()/sw
		}
	case o.Chroma == ChromaGray:
		alpha = alphaPlane(img, even)
		frame = hevc.Frame{Y: toGray(img, alpha != nil), StrideY: even.Dx()}
	default:
		alpha = alphaPlane(img, even)
		ycc := toYCbCr(img, ratioOf(o.Chroma), alpha != nil)
		frame = hevc.Frame{Y: ycc.Y, Cb: ycc.Cb, Cr: ycc.Cr,
			StrideY: ycc.YStride, StrideC: ycc.CStride}
	}

	stored := image.Point{X: even.Dx(), Y: even.Dy()}

	sample, params, err := codeItem(hevc.EncoderOptions{
		Width: stored.X, Height: stored.Y, Chroma: o.Chroma, BitDepth: o.BitDepth,
		QP: qp, Lossless: o.Lossless,
	}, frame)
	if err != nil {
		return err
	}

	f := heicFile{size: image.Point{X: width, Y: height}, stored: stored,
		gray: o.Chroma == ChromaGray, depth: o.BitDepth}
	f.add(encItem{id: 1, typ: "hvc1", name: "image", data: sample}, params)

	if alpha != nil || alpha6 != nil {
		aFrame := hevc.Frame{Y: alpha, Y16: alpha6, StrideY: stored.X}

		aSample, aParams, err := codeItem(hevc.EncoderOptions{
			Width: stored.X, Height: stored.Y, Chroma: hevc.ChromaMono,
			BitDepth: o.BitDepth, QP: qp, Lossless: o.Lossless,
		}, aFrame)
		if err != nil {
			return err
		}

		f.add(encItem{id: 2, typ: "hvc1", name: "alpha", hidden: true,
			data: aSample, ref: "auxl", to: 1}, aParams)
	}

	if len(o.Exif) > 0 {
		f.meta(encItem{typ: "Exif", name: "exif", data: append(u32(0), o.Exif...)})
	}

	if len(o.XMP) > 0 {
		f.meta(encItem{typ: "mime", name: "xmp", mime: xmpContentType, data: o.XMP})
	}

	file, err := f.marshal()
	if err != nil {
		return err
	}

	_, err = w.Write(file)

	return err
}

// codeItem codes one picture as a complete access unit, and returns the sample
// data its item carries with the parameter sets its hvcC repeats.
func codeItem(opts hevc.EncoderOptions, frame hevc.Frame) ([]byte, []hevc.NALUnit, error) {
	enc, err := hevc.NewEncoder(opts)
	if err != nil {
		return nil, nil, ErrUnsupported
	}

	nals, err := enc.Encode(frame)
	if err != nil || len(nals) != 4 {
		return nil, nil, ErrInvalid
	}

	sample := hevc.MarshalNAL(nals[3])
	if uint64(len(sample))+maxBoxOverhead > math.MaxUint32 {
		return nil, nil, ErrUnsupported
	}

	data := make([]byte, 4, 4+len(sample))
	binary.BigEndian.PutUint32(data, uint32(len(sample)))

	return append(data, sample...), nals[:3], nil
}

// encItem is one item on its way into the file: what iinf declares, what iloc
// points at, which ipco properties ipma ties to it, and what iref ties it to.
type encItem struct {
	id     uint16
	typ    string
	name   string
	mime   string
	hidden bool
	data   []byte
	assoc  []byte
	ref    string
	to     uint16
}

// heicFile gathers the items and their properties, so that the boxes naming
// them are written once from one list.
type heicFile struct {
	size, stored image.Point
	gray         bool
	depth        int
	items        []encItem
	props        []byte
	nProps       byte
	ispe, clap   byte
}

// prop appends a property to ipco and returns the one based index ipma names
// it by, with the essential bit of 23008-12 set.
func (f *heicFile) prop(b []byte, essential bool) byte {
	f.props = append(f.props, b...)
	f.nProps++

	idx := f.nProps
	if essential {
		idx |= 0x80
	}

	return idx
}

// shared is the ispe every item carries and, for a picture stored with a
// repeated edge, the clean aperture that takes it back off.
func (f *heicFile) shared() {
	if f.ispe != 0 {
		return
	}

	size := append(u32(uint32(f.size.X)), u32(uint32(f.size.Y))...)
	f.ispe = f.prop(fullBox("ispe", 0, 0, size), true)

	if f.stored != f.size {
		f.clap = f.prop(box("clap", clapData(f.size)), false)
	}
}

// add appends a coded picture item, with the properties an image needs.
func (f *heicFile) add(it encItem, params []hevc.NALUnit) {
	f.shared()

	mono := it.ref == "auxl"

	depth := byte(max(f.depth, 8))

	pixi := []byte{3, depth, depth, depth}
	if mono || f.gray {
		pixi = []byte{1, depth}
	}

	it.assoc = []byte{f.ispe, f.prop(fullBox("pixi", 0, 0, pixi), true)}

	if config, err := marshalHvcC(params); err == nil {
		it.assoc = append(it.assoc, f.prop(box("hvcC", config), true))
	}

	if mono {
		it.assoc = append(it.assoc,
			f.prop(fullBox("auxC", 0, 0, append([]byte(alphaURN), 0)), true))
	} else {
		it.assoc = append(it.assoc, f.prop(box("colr",
			append(append(append([]byte("nclx"), u16(encPrimaries)...),
				u16(encTransfer)...), append(u16(encMatrix), 0x80)...)), false))
	}

	if f.clap != 0 {
		it.assoc = append(it.assoc, f.clap)
	}

	f.items = append(f.items, it)
}

// meta appends a metadata item, which describes the picture and carries no
// properties of its own.
func (f *heicFile) meta(it encItem) {
	it.id = uint16(len(f.items)) + 1
	it.ref, it.to = "cdsc", 1

	f.items = append(f.items, it)
}

func (f *heicFile) marshal() ([]byte, error) {
	ftyp := box("ftyp", []byte("heic\x00\x00\x00\x00mif1heic"))

	var data []byte
	for _, it := range f.items {
		data = append(data, it.data...)
	}

	meta, err := f.metaBox(0)
	if err != nil {
		return nil, err
	}

	meta, err = f.metaBox(uint64(len(ftyp) + len(meta) + 8))
	if err != nil {
		return nil, err
	}

	return append(append(ftyp, meta...), box("mdat", data)...), nil
}

func (f *heicFile) metaBox(offset uint64) ([]byte, error) {
	hdlr := fullBox("hdlr", 0, 0, append([]byte("\x00\x00\x00\x00pict"), make([]byte, 13)...))
	pitm := fullBox("pitm", 0, 0, u16(1))

	var (
		infes []byte
		ilocs []byte
		irefs []byte
		ipmas []byte
		assoc int
	)

	for _, it := range f.items {
		flags := uint32(0)
		if it.hidden {
			flags = 1
		}

		entry := append(u16(it.id), 0, 0)
		entry = append(entry, it.typ...)
		entry = append(entry, it.name...)
		entry = append(entry, 0)

		if it.typ == "mime" {
			entry = append(entry, it.mime...)
			entry = append(entry, 0, 0)
		}

		infes = append(infes, fullBox("infe", 2, flags, entry)...)

		if uint64(len(it.data))+offset > math.MaxUint32 {
			return nil, ErrUnsupported
		}

		ilocs = append(ilocs, u16(it.id)...)
		ilocs = append(ilocs, u16(0)...)
		ilocs = append(ilocs, u16(1)...)
		ilocs = append(ilocs, u32(uint32(offset))...)
		ilocs = append(ilocs, u32(uint32(len(it.data)))...)
		offset += uint64(len(it.data))

		if it.ref != "" {
			irefs = append(irefs, box(it.ref,
				append(append(u16(it.id), u16(1)...), u16(it.to)...))...)
		}

		if len(it.assoc) == 0 {
			continue
		}

		ipmas = append(ipmas, u16(it.id)...)
		ipmas = append(ipmas, byte(len(it.assoc)))
		ipmas = append(ipmas, it.assoc...)
		assoc++
	}

	iinf := fullBox("iinf", 0, 0, append(u16(uint16(len(f.items))), infes...))
	locs := append(u16(uint16(len(f.items))), ilocs...)
	iloc := fullBox("iloc", 0, 0, append([]byte{0x44, 0}, locs...))
	iprp := box("iprp", append(box("ipco", f.props),
		fullBox("ipma", 0, 0, append(u32(uint32(assoc)), ipmas...))...))

	meta := append(hdlr, pitm...)
	meta = append(meta, iinf...)
	meta = append(meta, iloc...)

	if irefs != nil {
		meta = append(meta, fullBox("iref", 0, 0, irefs)...)
	}

	meta = append(meta, iprp...)

	return fullBox("meta", 0, 0, meta), nil
}

// clapData is the CleanApertureBox naming the whole of the image ispe
// describes. It is what makes a reader take the stored picture down to that
// size rather than hand back the repeated edge with it.
func clapData(size image.Point) []byte {
	var out []byte

	for _, v := range [8]int32{int32(size.X), 1, int32(size.Y), 1, 0, 1, 0, 1} {
		out = append(out, u32(uint32(v))...)
	}

	return out
}

// marshalHvcC writes the HEVCDecoderConfigurationRecord. ISO/IEC 14496-15
// requires its profile, tier and level to be the sequence parameter set's own.
func marshalHvcC(params []hevc.NALUnit) ([]byte, error) {
	var (
		ptl []byte
		sps []byte
	)

	for _, nal := range params {
		if nal.Type != hevc.NALSPS {
			continue
		}

		b, ok := hevc.ProfileTierLevel(nal.RBSP)
		if !ok {
			return nil, ErrInvalid
		}

		ptl, sps = b, nal.RBSP
	}

	if ptl == nil {
		return nil, ErrInvalid
	}

	chroma, depthY, depthC, ok := hevc.SPSFormat(sps)
	if !ok {
		return nil, ErrInvalid
	}

	out := append([]byte{1}, ptl...)
	out = append(out, 0xf0, 0, 0xfc,
		0xfc|byte(chroma),
		0xf8|byte(depthY-8),
		0xf8|byte(depthC-8),
		0, 0, 0x0f, 3)

	for _, nal := range params {
		data := hevc.MarshalNAL(nal)
		if len(data) > 0xffff {
			return nil, ErrInvalid
		}

		out = append(out, 0x80|byte(nal.Type), 0, 1)
		out = append(out, u16(uint16(len(data)))...)
		out = append(out, data...)
	}

	return out, nil
}

func box(typ string, data []byte) []byte {
	out := make([]byte, 8, 8+len(data))
	binary.BigEndian.PutUint32(out, uint32(8+len(data)))
	copy(out[4:], typ)

	return append(out, data...)
}

func fullBox(typ string, version uint8, flags uint32, data []byte) []byte {
	return box(typ, append([]byte{version, byte(flags >> 16), byte(flags >> 8), byte(flags)}, data...))
}

func u16(v uint16) []byte {
	out := make([]byte, 2)
	binary.BigEndian.PutUint16(out, v)

	return out
}

func u32(v uint32) []byte {
	out := make([]byte, 4)
	binary.BigEndian.PutUint32(out, v)

	return out
}
