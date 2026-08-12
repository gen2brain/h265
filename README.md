## h265
[![Status](https://github.com/gen2brain/h265/actions/workflows/test.yml/badge.svg)](https://github.com/gen2brain/h265/actions)
[![Go Reference](https://pkg.go.dev/badge/github.com/gen2brain/h265.svg)](https://pkg.go.dev/github.com/gen2brain/h265)

[HEVC](https://en.wikipedia.org/wiki/High_Efficiency_Video_Coding) video and
[HEIC](https://en.wikipedia.org/wiki/High_Efficiency_Image_File_Format) image decoder in pure Go.

No CGo, no dependencies.

SIMD support for amd64 (AVX2), arm64 (NEON) and riscv64 (RVV, with `GORISCV64=rva23u64`) is planned.
Build with `-tags noasm` for pure Go everywhere.

### Status

Under development. The bitstream decoder is byte-exact on all 97 available JCT-VC HEVC v1
conformance vectors; the HEIC container is not written yet.

See [docs/STATUS.md](docs/STATUS.md) for where things stand and what is next, and
[docs/PLAN.md](docs/PLAN.md) for the design and milestones.

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

### License

MIT, in [LICENSE](LICENSE). The decoder is a port of the pure-Rust
[rust_h265](https://github.com/roticv/rust_h265) and
[oxideav-h265](https://github.com/OxideAV/oxideav-h265) decoders and carries their notices.

This project is an implementation of a decoder. It gives you no special rights on the HEVC patents.
HEVC is covered by patents held by several pools and by unpooled holders; if you distribute or use
this software you may need a licence from them.
