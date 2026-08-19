## h265
[![Status](https://github.com/gen2brain/h265/actions/workflows/test.yml/badge.svg)](https://github.com/gen2brain/h265/actions)
[![Go Reference](https://pkg.go.dev/badge/github.com/gen2brain/h265.svg)](https://pkg.go.dev/github.com/gen2brain/h265)

[HEVC](https://en.wikipedia.org/wiki/High_Efficiency_Video_Coding) video and
[HEIC](https://en.wikipedia.org/wiki/High_Efficiency_Image_File_Format) image codec in pure Go,
decoding and encoding. No CGo, no dependencies.

The decoder is byte-exact on the JCT-VC HEVC v1 conformance suite. What the encoder writes is
held to libde265, FFmpeg and libheif sample for sample.

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

`heic.Encode` writes any image as a HEIC still, keeping alpha as the auxiliary item of
ISO/IEC 23008-12 Annex F.

```go
err := heic.Encode(w, img, heic.EncodeOptions{Quality: 60})
```

`hevc.Encoder` writes the bitstream on its own, as self-contained intra IDR access units:

```go
enc, err := hevc.NewEncoder(hevc.EncoderOptions{Width: 1920, Height: 1080, QP: 26})
nals, err := enc.Encode(hevc.Frame{
    Y: y, Cb: cb, Cr: cr,
    StrideY: yStride, StrideC: cStride,
})
stream := hevc.MarshalAnnexB(nals)
```

### Supported

Decoding: 8-16 bit, 4:2:0/4:2:2/4:4:4/monochrome, tiles, wavefronts, dependent slice segments,
PCM, lossless, scaling lists, and the range extensions other than cross-component prediction,
RDPCM and CABAC bypass alignment, which are refused rather than decoded wrongly. In the container
alpha, `grid`, `clap`/`irot`/`imir`, `colr`, image sequences, Exif and XMP.

Encoding: 8-12 bit, 4:2:0/4:2:2/4:4:4/monochrome, intra only, deblocking, sample adaptive
offset, wavefronts, PCM lossless, and alpha, `colr`, Exif and XMP in the container.

Decoding is threaded over grid tiles and wavefront rows; `heic.Options.Threads` and
`hevc.Decoder.Threads` bound it.

### License

MIT, in [LICENSE](LICENSE). The decoder is a port of the pure-Rust
[rust_h265](https://github.com/roticv/rust_h265) and
[oxideav-h265](https://github.com/OxideAV/oxideav-h265) decoders and carries their notices.

This software implements a decoder and an encoder. It gives you no special rights on the HEVC
patents. HEVC is covered by patents held by several pools and by unpooled holders; if you
distribute or use this software you may need a licence from them.
