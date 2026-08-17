package hevc

func encodePCM(y, cb, cr []uint8, width, height int) ([]NALUnit, error) {
	if width <= 0 || height <= 0 || width&15 != 0 || height&15 != 0 ||
		len(y) != width*height || len(cb) != width*height/4 || len(cr) != width*height/4 {
		return nil, ErrInvalid
	}

	h := encoderHeaders{width: width, height: height, levelIDC: pcmLevelIDC(width * height), pcm: true}
	rbsp := pcmSlice(y, cb, cr, width, height)

	return []NALUnit{
		{Type: NALVPS, TemporalID: 0, RBSP: h.vps()},
		{Type: NALSPS, TemporalID: 0, RBSP: h.sps()},
		{Type: NALPPS, TemporalID: 0, RBSP: h.pps()},
		{Type: NALIdrNLP, TemporalID: 0, RBSP: rbsp},
	}, nil
}

func pcmLevelIDC(samples int) uint8 {
	for _, level := range []struct {
		samples int
		idc     uint8
	}{
		{36864, 30},
		{122880, 60},
		{245760, 63},
		{552960, 90},
		{983040, 93},
		{2228224, 120},
		{8912896, 150},
		{35651584, 180},
	} {
		if samples <= level.samples {
			return level.idc
		}
	}

	return 186
}

func pcmSlice(y, cb, cr []uint8, width, height int) []byte {
	var bits putBits
	bits.bit(1)
	bits.bit(0)
	bits.ue(0)
	bits.ue(uint32(sliceI))
	bits.se(0)
	bits.bit(1)
	bits.rbspTrailingBits()

	var cabac cabacWriter
	cabac.init(&bits, 26, sliceI, false)

	for y0 := 0; y0 < height; y0 += 16 {
		for x0 := 0; x0 < width; x0 += 16 {
			cabac.encodeBin(ctxPartMode, 1)
			cabac.encodeTerminate(1)
			cabac.bytes()

			writePCMPlane(&bits, y, width, x0, y0, 16)
			writePCMPlane(&bits, cb, width/2, x0/2, y0/2, 8)
			writePCMPlane(&bits, cr, width/2, x0/2, y0/2, 8)
			cabac.reinit()
			cabac.encodeTerminate(boolToBit(x0 == width-16 && y0 == height-16))
		}
	}

	return cabac.bytes()
}

func writePCMPlane(bits *putBits, plane []uint8, stride, x, y, size int) {
	for j := range size {
		for i := range size {
			bits.bits(uint64(plane[(y+j)*stride+x+i]), 8)
		}
	}
}
