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

// maxBoxOverhead bounds the header bytes before the sample data, so a file
// needing 64 bit box sizes is refused rather than truncated.
const maxBoxOverhead = 1 << 16

// EncodeOptions are the encoding parameters.
type EncodeOptions struct {
	// Quality in the range [0,100]. Default is DefaultQuality.
	Quality int
	// Lossless codes the samples as PCM and ignores Quality.
	Lossless bool
}

// Encode writes img to w as a HEIC still. Any image will do; one that is not
// already 8-bit 4:2:0 *image.YCbCr is converted, and alpha is dropped.
func Encode(w io.Writer, img image.Image, opts ...EncodeOptions) error {
	quality, lossless := DefaultQuality, false

	if len(opts) > 0 {
		quality, lossless = opts[0].Quality, opts[0].Lossless

		if quality <= 0 {
			quality = DefaultQuality
		} else if quality > 100 {
			quality = 100
		}
	}

	width, height := img.Bounds().Dx(), img.Bounds().Dy()
	ycc := toYCbCr420(img)

	enc, err := hevc.NewEncoder(hevc.EncoderOptions{
		Width: ycc.Rect.Dx(), Height: ycc.Rect.Dy(),
		QP: 51 - quality*50/100, Lossless: lossless,
	})
	if err != nil {
		return ErrUnsupported
	}

	nals, err := enc.Encode(hevc.Frame{
		Y: ycc.Y, Cb: ycc.Cb, Cr: ycc.Cr, StrideY: ycc.YStride, StrideC: ycc.CStride,
	})
	if err != nil {
		return ErrInvalid
	}

	if len(nals) != 4 {
		return ErrInvalid
	}

	stored := image.Point{X: ycc.Rect.Dx(), Y: ycc.Rect.Dy()}

	sample := hevc.MarshalNAL(nals[3])
	if uint64(len(sample))+maxBoxOverhead > math.MaxUint32 {
		return ErrUnsupported
	}

	data := make([]byte, 4)
	binary.BigEndian.PutUint32(data, uint32(len(sample)))
	data = append(data, sample...)

	file, err := makeHEIC(image.Point{X: width, Y: height}, stored, nals[:3], data)
	if err != nil {
		return err
	}

	_, err = w.Write(file)

	return err
}

func makeHEIC(size, stored image.Point, params []hevc.NALUnit, data []byte) ([]byte, error) {
	ftyp := box("ftyp", []byte("heic\x00\x00\x00\x00mif1heic"))

	meta, err := heicMeta(size, stored, params, 0, uint64(len(data)))
	if err != nil {
		return nil, err
	}

	off := uint64(len(ftyp) + len(meta) + 8)

	meta, err = heicMeta(size, stored, params, off, uint64(len(data)))
	if err != nil {
		return nil, err
	}

	return append(append(ftyp, meta...), box("mdat", data)...), nil
}

func heicMeta(size, stored image.Point, params []hevc.NALUnit, offset, length uint64) ([]byte, error) {
	hdlr := fullBox("hdlr", 0, 0, append([]byte("\x00\x00\x00\x00pict"), make([]byte, 13)...))
	pitm := fullBox("pitm", 0, 0, u16(1))
	infe := fullBox("infe", 2, 0, append(append(append(u16(1), 0, 0), []byte("hvc1")...), []byte("image\x00")...))
	iinf := fullBox("iinf", 0, 0, append(u16(1), infe...))
	ilocData := []byte{0x44, 0}
	ilocData = append(ilocData, u16(1)...)
	ilocData = append(ilocData, u16(1)...)
	ilocData = append(ilocData, u16(0)...)
	ilocData = append(ilocData, u16(1)...)
	ilocData = append(ilocData, u32(uint32(offset))...)
	ilocData = append(ilocData, u32(uint32(length))...)
	iloc := fullBox("iloc", 0, 0, ilocData)

	ispe := fullBox("ispe", 0, 0, append(u32(uint32(size.X)), u32(uint32(size.Y))...))
	pixi := fullBox("pixi", 0, 0, []byte{3, 8, 8, 8})
	config, err := marshalHvcC(params)
	if err != nil {
		return nil, err
	}

	hvcc := box("hvcC", config)
	colr := box("colr", append(append(append([]byte("nclx"), u16(encPrimaries)...),
		u16(encTransfer)...), append(u16(encMatrix), 0x80)...))
	props := append(append(append(ispe, pixi...), hvcc...), colr...)
	assoc := []byte{0x81, 0x82, 0x83, 0x04}

	// An odd dimension cannot be coded in 4:2:0, so the stored picture keeps a
	// repeated edge and a clean aperture takes it back off.
	if stored.X != size.X || stored.Y != size.Y {
		props = append(props, box("clap", clapData(size))...)
		assoc = append(assoc, 0x05)
	}

	ipco := box("ipco", props)
	ipma := fullBox("ipma", 0, 0, append(append(append(u32(1), u16(1)...), byte(len(assoc))), assoc...))
	iprp := box("iprp", append(ipco, ipma...))

	meta := append(hdlr, pitm...)
	meta = append(meta, iinf...)
	meta = append(meta, iloc...)
	meta = append(meta, iprp...)

	return fullBox("meta", 0, 0, meta), nil
}

// marshalHvcC writes the HEVCDecoderConfigurationRecord. ISO/IEC 14496-15
// requires its profile, tier and level to be the sequence parameter set's own.
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

func marshalHvcC(params []hevc.NALUnit) ([]byte, error) {
	var ptl []byte

	for _, nal := range params {
		if nal.Type != hevc.NALSPS {
			continue
		}

		b, ok := hevc.ProfileTierLevel(nal.RBSP)
		if !ok {
			return nil, ErrInvalid
		}

		ptl = b
	}

	if ptl == nil {
		return nil, ErrInvalid
	}

	out := append([]byte{1}, ptl...)
	out = append(out, 0xf0, 0, 0xfc, 0xfd, 0xf8, 0xf8, 0, 0, 0x0f, 3)

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
