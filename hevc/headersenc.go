package hevc

type encoderHeaders struct {
	// width and height are the coded picture, which 7.4.3.2 requires to be a
	// multiple of the minimum coding block size. cropRight and cropBottom are
	// the luma samples the conformance window hides beyond the real picture.
	width, height         int
	cropRight, cropBottom int
	levelIDC              uint8
	pcm                   bool
	deblockingDisabled    bool
	signDataHidingEnabled bool
	ctbLog2               uint8
	maxTrHierIntra        uint32
	wavefront             bool
}

// writeProfileTierLevel is 7.3.3: Main profile, Main tier, progressive frames
// only. The High tier is not defined for the levels this encoder reaches.
// parameterSets is the video, sequence and picture parameter sets a slice
// coded with these headers needs in front of it.
func (h encoderHeaders) parameterSets() []NALUnit {
	return []NALUnit{
		{Type: NALVPS, RBSP: h.vps()},
		{Type: NALSPS, RBSP: h.sps()},
		{Type: NALPPS, RBSP: h.pps()},
	}
}

func writeProfileTierLevel(w *putBits, levelIDC uint8) {
	w.bits(0, 2)
	w.bit(0)
	w.bits(1, 5)
	w.bits(3<<29, 32)
	w.bit(1)
	w.bit(0)
	w.bit(1)
	w.bit(1)
	w.bits(0, 44)
	w.bits(uint64(levelIDC), 8)
}

func (h encoderHeaders) vps() []byte {
	var w putBits
	w.bits(0, 4)
	w.bit(1)
	w.bit(1)
	w.bits(0, 6)
	w.bits(0, 3)
	w.bit(1)
	w.bits(0xffff, 16)
	writeProfileTierLevel(&w, h.levelIDC)
	w.bit(1)
	w.ue(1)
	w.ue(0)
	w.ue(0)
	w.bits(0, 6)
	w.ue(0)
	w.bit(0)
	w.bit(0)
	w.rbspTrailingBits()

	return w.bytes()
}

func (h encoderHeaders) sps() []byte {
	var w putBits
	w.bits(0, 4)
	w.bits(0, 3)
	w.bit(1)
	writeProfileTierLevel(&w, h.levelIDC)
	w.ue(0)
	w.ue(1)
	w.ue(uint32(h.width))
	w.ue(uint32(h.height))
	if h.cropRight|h.cropBottom != 0 {
		w.bit(1)
		w.ue(0)
		w.ue(uint32(h.cropRight / 2))
		w.ue(0)
		w.ue(uint32(h.cropBottom / 2))
	} else {
		w.bit(0)
	}
	w.ue(0)
	w.ue(0)
	w.ue(4)
	w.bit(1)
	w.ue(1)
	w.ue(0)
	w.ue(0)
	w.ue(1)
	ctbLog2 := h.ctbLog2
	if ctbLog2 == 0 {
		ctbLog2 = 4
	}
	w.ue(uint32(ctbLog2 - 4))
	w.ue(0)
	// 7.4.3.2 caps the transform at the coding tree block, and at 32.
	w.ue(uint32(min(ctbLog2, 5) - 2))
	w.ue(0)
	w.ue(h.maxTrHierIntra)
	w.bit(0)
	w.bit(0)
	w.bit(0)
	w.bit(boolToBit(h.pcm))
	if h.pcm {
		w.bits(7, 4)
		w.bits(7, 4)
		w.ue(1)
		w.ue(0)
		w.bit(1)
	}
	w.ue(0)
	w.bit(0)
	w.bit(0)
	w.bit(0)
	w.bit(0)
	w.bit(0)
	w.rbspTrailingBits()

	return w.bytes()
}

func (h encoderHeaders) pps() []byte {
	var w putBits
	w.ue(0)
	w.ue(0)
	w.bit(0)
	w.bit(0)
	w.bits(0, 3)
	w.bit(boolToBit(h.signDataHidingEnabled))
	w.bit(0)
	w.ue(0)
	w.ue(0)
	w.se(0)
	w.bit(0)
	w.bit(0)
	w.bit(0)
	w.se(0)
	w.se(0)
	w.bit(0)
	w.bit(0)
	w.bit(0)
	w.bit(0)
	w.bit(0)
	w.bit(boolToBit(h.wavefront))
	w.bit(1)
	w.bit(1)
	w.bit(0)
	w.bit(boolToBit(h.deblockingDisabled))
	if !h.deblockingDisabled {
		w.se(0)
		w.se(0)
	}
	w.bit(0)
	w.bit(0)
	w.ue(0)
	w.bit(0)
	w.bit(0)
	w.rbspTrailingBits()

	return w.bytes()
}

func boolToBit(v bool) uint32 {
	if v {
		return 1
	}

	return 0
}
