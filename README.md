## h265
[![Status](https://github.com/gen2brain/h265/actions/workflows/test.yml/badge.svg)](https://github.com/gen2brain/h265/actions)
[![Go Reference](https://pkg.go.dev/badge/github.com/gen2brain/h265.svg)](https://pkg.go.dev/github.com/gen2brain/h265)

[HEVC](https://en.wikipedia.org/wiki/High_Efficiency_Video_Coding) video and
[HEIC](https://en.wikipedia.org/wiki/High_Efficiency_Image_File_Format) image decoder in pure Go.

Byte-exact on the JCT-VC HEVC v1 conformance suite. No CGo, no dependencies.

SIMD support for amd64 (AVX2, AVX-512), arm64 (NEON) and riscv64 (RVV, with `GORISCV64=rva23u64`).
Build with `-tags noasm` for pure Go everywhere.

### Decoding

```go
img, err := heic.Decode(r)
```

`heic.Decode` returns `*image.NRGBA`, or `*image.NRGBA64` above 8 bits, and registers itself with
`image.RegisterFormat`. The `hevc` package decodes the bitstream on its own, a NAL unit at a time:

```go
d := hevc.Decoder{}

for _, nal := range nals {
    pics, err := d.DecodeNAL(nal)
    ...
}
```

### Encoding

`hevc.Encoder` currently writes self-contained, lossless PCM IDR access units
from 8-bit 4:2:0 planar frames. Dimensions must be non-zero multiples of 16.
This is the syntax-complete bootstrap for the encoder; lossy intra coding and
HEIC writing are not available yet.

```go
enc, err := hevc.NewEncoder(hevc.EncoderOptions{Width: 1920, Height: 1080})
nals, err := enc.Encode(hevc.Frame{
    Y: y, Cb: cb, Cr: cr,
    StrideY: yStride, StrideC: cStride,
})
stream := hevc.MarshalAnnexB(nals)
```

`heic.Encode` currently accepts an 8-bit 4:2:0 `*image.YCbCr` whose dimensions
are non-zero multiples of 16 and writes a lossless PCM HEIC still.

### Supported

8-16 bit, 4:2:0/4:2:2/4:4:4/monochrome, tiles, wavefronts, dependent slice segments, PCM, lossless,
scaling lists, and the range extensions other than cross-component prediction, RDPCM and CABAC bypass
alignment, which are refused rather than decoded wrongly. In the container alpha, `grid`,
`clap`/`irot`/`imir`, `colr`, image sequences, Exif and XMP.

Decoding is threaded over grid tiles and wavefront rows; `heic.Options.Threads` and
`hevc.Decoder.Threads` bound it.

### License

MIT, in [LICENSE](LICENSE). The decoder is a port of the pure-Rust
[rust_h265](https://github.com/roticv/rust_h265) and
[oxideav-h265](https://github.com/OxideAV/oxideav-h265) decoders and carries their notices.

This project is an implementation of a decoder. It gives you no special rights on the HEVC patents.
HEVC is covered by patents held by several pools and by unpooled holders; if you distribute or use
this software you may need a licence from them.
