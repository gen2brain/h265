package hevc

func encodePCM(y, cb, cr []uint8, width, height int) ([]NALUnit, error) {
	if !validFrame(width, height, len(y), len(cb), len(cr)) {
		return nil, ErrInvalid
	}

	cw, ch := codedSize(width), codedSize(height)
	py, _ := padPlane(nil, y, width, width, height, cw, ch)
	pcb, _ := padPlane(nil, cb, width/2, width/2, height/2, cw/2, ch/2)
	pcr, _ := padPlane(nil, cr, width/2, width/2, height/2, cw/2, ch/2)

	h := encoderHeaders{
		width: cw, height: ch, cropRight: cw - width, cropBottom: ch - height,
		chromaFormat: 1, subWidthC: 2, subHeightC: 2, bitDepth: 8,
		levelIDC: pcmLevelIDC(cw * ch), pcm: true,
	}
	rbsp := pcmSlice(py, pcb, pcr, cw, ch, 2, 2, 8)

	return append(h.parameterSets(), NALUnit{Type: NALIdrNLP, RBSP: rbsp}), nil
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

func pcmSlice[P pixel](y, cb, cr []P, width, height, subW, subH, bitDepth int) []byte {
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

			writePCMPlane(&bits, y, width, x0, y0, 16, 16, bitDepth)

			if cb != nil {
				writePCMPlane(&bits, cb, width/subW, x0/subW, y0/subH, 16/subW, 16/subH, bitDepth)
				writePCMPlane(&bits, cr, width/subW, x0/subW, y0/subH, 16/subW, 16/subH, bitDepth)
			}
			cabac.reinit()
			cabac.encodeTerminate(boolToBit(x0 == width-16 && y0 == height-16))
		}
	}

	return cabac.bytes()
}

func writePCMPlane[P pixel](bits *putBits, plane []P, stride, x, y, w, h, bitDepth int) {
	for j := range h {
		for i := range w {
			bits.bits(uint64(plane[(y+j)*stride+x+i]), bitDepth)
		}
	}
}
