package heic

import (
	"bytes"
	"encoding/binary"
	"image"
	"io"

	"github.com/gen2brain/h265/hevc"
)

// The written planes are what image.YCbCr holds, which is full range BT.601
// carrying sRGB primaries and transfer.
const (
	encPrimaries = 1
	encTransfer  = 13
	encMatrix    = 6
)

type EncodeOptions struct{}

func Encode(w io.Writer, img image.Image, opts ...EncodeOptions) error {
	ycc, ok := img.(*image.YCbCr)
	if !ok || ycc.SubsampleRatio != image.YCbCrSubsampleRatio420 || ycc.Rect.Min.X != 0 || ycc.Rect.Min.Y != 0 {
		return ErrUnsupported
	}

	width, height := ycc.Rect.Dx(), ycc.Rect.Dy()
	enc, err := hevc.NewEncoder(hevc.EncoderOptions{Width: width, Height: height, Lossless: true})
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

	data := make([]byte, 4)
	binary.BigEndian.PutUint32(data, uint32(len(hevc.MarshalNAL(nals[3]))))
	data = append(data, hevc.MarshalNAL(nals[3])...)

	file := makeHEIC(width, height, nals[:3], data)
	_, err = w.Write(file)

	return err
}

func makeHEIC(width, height int, params []hevc.NALUnit, data []byte) []byte {
	ftyp := box("ftyp", []byte("heic\x00\x00\x00\x00mif1heic"))
	meta := heicMeta(width, height, params, 0, uint64(len(data)))
	off := uint64(len(ftyp) + len(meta) + 8)
	meta = heicMeta(width, height, params, off, uint64(len(data)))

	return append(append(ftyp, meta...), box("mdat", data)...)
}

func heicMeta(width, height int, params []hevc.NALUnit, offset, length uint64) []byte {
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

	ispe := fullBox("ispe", 0, 0, append(u32(uint32(width)), u32(uint32(height))...))
	pixi := fullBox("pixi", 0, 0, []byte{3, 8, 8, 8})
	hvcc := box("hvcC", marshalHvcC(params))
	colr := box("colr", append(append(append([]byte("nclx"), u16(encPrimaries)...),
		u16(encTransfer)...), append(u16(encMatrix), 0x80)...))
	ipco := box("ipco", append(append(append(ispe, pixi...), hvcc...), colr...))
	ipma := fullBox("ipma", 0, 0, append(append(append(u32(1), u16(1)...), 4), 0x81, 0x82, 0x83, 0x04))
	iprp := box("iprp", append(ipco, ipma...))

	meta := append(hdlr, pitm...)
	meta = append(meta, iinf...)
	meta = append(meta, iloc...)
	meta = append(meta, iprp...)

	return fullBox("meta", 0, 0, meta)
}

func marshalHvcC(params []hevc.NALUnit) []byte {
	out := []byte{
		1, 1, 0x60, 0, 0, 0, 0, 0, 0, 0, 0, 0, 30,
		0xf0, 0, 0xfc, 0xfd, 0xf8, 0xf8, 0, 0, 0x0f, 3,
	}

	for _, nal := range params {
		data := hevc.MarshalNAL(nal)
		out = append(out, 0x80|byte(nal.Type), 0, 1)
		out = append(out, u16(uint16(len(data)))...)
		out = append(out, data...)
	}

	return out
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

func encodeToBytes(img image.Image) ([]byte, error) {
	var b bytes.Buffer
	err := Encode(&b, img)

	return b.Bytes(), err
}
