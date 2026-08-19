package hevc

type encoderHeaders struct {
	// width and height are the coded picture, which 7.4.3.2 requires to be a
	// multiple of the minimum coding block size. cropRight and cropBottom are
	// the luma samples the conformance window hides beyond the real picture.
	width, height         int
	cropRight, cropBottom int
	// chromaFormat is chroma_format_idc, and subWidthC and subHeightC the
	// units 7.4.3.2 counts the conformance window offsets in.
	chromaFormat          uint32
	subWidthC, subHeightC int
	bitDepth              int
	levelIDC              uint8
	pcm                   bool
	deblockingDisabled    bool
	sao                   bool
	signDataHidingEnabled bool
	ctbLog2               uint8
	maxTrHierIntra        uint32
	wavefront             bool
}

// parameterSets is the video, sequence and picture parameter sets a slice
// coded with these headers needs in front of it.
func (h encoderHeaders) parameterSets() []NALUnit {
	return []NALUnit{
		{Type: NALVPS, RBSP: h.vps()},
		{Type: NALSPS, RBSP: h.sps()},
		{Type: NALPPS, RBSP: h.pps()},
	}
}

// profileTier is 7.3.3: Main tier, progressive frames only. The High tier is
// not defined for the levels this encoder reaches. A.3.2 gives Main eight bit
// 4:2:0 alone and A.3.3 gives Main 10 ten bits of it; anything else is the
// range extension of A.3.5, which spells its limits out in constraint flags.
func (h encoderHeaders) profileTier(w *putBits) {
	chroma, depth := h.chromaFormat, max(h.bitDepth, 8)
	idc, compat := profileIDC(chroma, depth)

	w.bits(0, 2)
	w.bit(0)
	w.bits(uint64(idc), 5)
	w.bits(uint64(compat), 32)
	w.bit(1)
	w.bit(0)
	w.bit(1)
	w.bit(1)

	if idc != 4 {
		w.bits(0, 44)
		w.bits(uint64(h.levelIDC), 8)

		return
	}

	for _, set := range [9]bool{
		depth <= 12, depth <= 10, depth <= 8,
		chroma <= 2, chroma <= 1, chroma == 0,
		true, false, true,
	} {
		w.bit(boolToBit(set))
	}

	w.bits(0, 35)
	w.bits(uint64(h.levelIDC), 8)
}

// profileIDC is general_profile_idc and the compatibility flags that go with
// it, the flag for profile j sitting j bits down from the top.
func profileIDC(chromaFormat uint32, bitDepth int) (uint32, uint32) {
	switch {
	case chromaFormat != 1 || bitDepth > 10:
		return 4, 1 << (31 - 4)
	case bitDepth > 8:
		return 2, 1 << (31 - 2)
	default:
		return 1, 1<<(31-1) | 1<<(31-2)
	}
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
	h.profileTier(&w)
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
	h.profileTier(&w)
	w.ue(0)
	w.ue(h.chromaFormat)
	if h.chromaFormat == 3 {
		w.bit(0)
	}
	w.ue(uint32(h.width))
	w.ue(uint32(h.height))
	if h.cropRight|h.cropBottom != 0 {
		w.bit(1)
		w.ue(0)
		w.ue(uint32(h.cropRight / max(h.subWidthC, 1)))
		w.ue(0)
		w.ue(uint32(h.cropBottom / max(h.subHeightC, 1)))
	} else {
		w.bit(0)
	}
	w.ue(uint32(max(h.bitDepth, 8) - 8))
	w.ue(uint32(max(h.bitDepth, 8) - 8))
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
	w.bit(boolToBit(h.sao))
	w.bit(boolToBit(h.pcm))
	if h.pcm {
		w.bits(uint64(max(h.bitDepth, 8)-1), 4)
		w.bits(uint64(max(h.bitDepth, 8)-1), 4)
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
