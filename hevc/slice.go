package hevc

import "math/bits"

type sliceType uint8

const (
	sliceB sliceType = iota
	sliceP
	sliceI
)

const maxRefs = 16

type longTermRPS struct {
	numSps           int
	pocLsbLt         []uint32
	usedByCurrPicLt  []bool
	deltaPocMsbPresn []bool
	deltaPocMsbCycle []uint32
}

type refPicListModification struct {
	flagL0      bool
	listEntryL0 []uint32
	flagL1      bool
	listEntryL1 []uint32
}

type predWeightTable struct {
	lumaLog2Denom   uint8
	chromaLog2Denom uint8
	lumaWeight      [2][maxRefs]int16
	lumaOffset      [2][maxRefs]int16
	chromaWeight    [2][maxRefs][2]int16
	chromaOffset    [2][maxRefs][2]int16
}

type sliceHeader struct {
	nalType NALType

	firstSliceSegmentInPic bool
	noOutputOfPriorPics    bool
	ppsID                  uint32
	dependentSliceSegment  bool
	sliceSegmentAddress    uint32

	sliceType     sliceType
	picOutputFlag bool
	colourPlaneID uint8

	picOrderCntLsb  uint32
	stRPSFromSPS    bool
	stRPSIdx        uint32
	stRPS           shortTermRPS
	ltRPS           longTermRPS
	temporalMvp     bool
	numPocTotalCurr uint32

	saoLuma   bool
	saoChroma bool

	numRefIdxL0Active uint32
	numRefIdxL1Active uint32
	listModification  refPicListModification
	mvdL1Zero         bool
	cabacInit         bool
	collocatedFromL0  bool
	collocatedRefIdx  uint32
	maxNumMergeCand   uint32
	weights           predWeightTable

	qpDelta            int32
	qpY                int32
	cbQPOffset         int32
	crQPOffset         int32
	cuChromaQPOffset   bool
	deblockingDisabled bool
	betaOffsetDiv2     int32
	tcOffsetDiv2       int32
	loopFilterAcross   bool

	entryPointOffsets []uint32

	dataOffset int
}

func ceilLog2(n uint32) int {
	if n <= 1 {
		return 0
	}

	return bits.Len32(n - 1)
}

func (s *sps) chromaArrayType() uint32 {
	if s.separateColourPlane {
		return 0
	}

	return s.chromaFormatIDC
}

func (h *sliceHeader) inherit(prev *sliceHeader) {
	addr := h.sliceSegmentAddress
	entry := h.entryPointOffsets
	off := h.dataOffset
	first := h.firstSliceSegmentInPic
	dep := h.dependentSliceSegment
	nal := h.nalType
	ppsID := h.ppsID
	noOutput := h.noOutputOfPriorPics

	*h = *prev

	h.sliceSegmentAddress = addr
	h.entryPointOffsets = entry
	h.dataOffset = off
	h.firstSliceSegmentInPic = first
	h.dependentSliceSegment = dep
	h.nalType = nal
	h.ppsID = ppsID
	h.noOutputOfPriorPics = noOutput
}

func parseSliceHeader(rbsp []byte, nalType NALType, s *sps, p *pps) (*sliceHeader, error) {
	var c getBits
	c.init(rbsp)

	h := &sliceHeader{nalType: nalType}
	h.firstSliceSegmentInPic = c.bit() != 0

	if nalType >= NALBlaWLP && nalType <= 23 {
		h.noOutputOfPriorPics = c.bit() != 0
	}

	h.ppsID = c.ue()
	if h.ppsID != p.id {
		return nil, ErrInvalid
	}

	numCtbs := s.picWidthInCtbs * s.picHeightInCtbs

	if !h.firstSliceSegmentInPic {
		if p.dependentSliceSegmentsEnabled {
			h.dependentSliceSegment = c.bit() != 0
		}

		h.sliceSegmentAddress = c.bits(ceilLog2(numCtbs))
		if h.sliceSegmentAddress >= numCtbs {
			return nil, ErrInvalid
		}
	}

	h.picOutputFlag = true
	h.collocatedFromL0 = true
	h.maxNumMergeCand = 5
	h.numRefIdxL0Active = p.numRefIdxL0DefaultActive
	h.numRefIdxL1Active = p.numRefIdxL1DefaultActive
	h.deblockingDisabled = p.deblockingDisabled
	h.betaOffsetDiv2 = p.betaOffsetDiv2
	h.tcOffsetDiv2 = p.tcOffsetDiv2
	h.loopFilterAcross = p.loopFilterAcrossSlices

	if !h.dependentSliceSegment {
		c.skip(int(p.numExtraSliceHeaderBits))

		t := c.ue()
		if t > 2 {
			return nil, ErrInvalid
		}

		h.sliceType = sliceType(t)

		if p.outputFlagPresent {
			h.picOutputFlag = c.bit() != 0
		}

		if s.separateColourPlane {
			h.colourPlaneID = uint8(c.bits(2))
		}

		if !nalType.IsIDR() {
			h.picOrderCntLsb = c.bits(int(s.log2MaxPocLsb))

			h.stRPSFromSPS = c.bit() != 0
			if !h.stRPSFromSPS {
				rps, err := parseShortTermRPS(&c, len(s.stRPS), len(s.stRPS), s.stRPS)
				if err != nil {
					return nil, err
				}

				h.stRPS = rps
			} else {
				if len(s.stRPS) == 0 {
					return nil, ErrInvalid
				}

				if len(s.stRPS) > 1 {
					h.stRPSIdx = c.bits(ceilLog2(uint32(len(s.stRPS))))
				}

				if int(h.stRPSIdx) >= len(s.stRPS) {
					return nil, ErrInvalid
				}

				h.stRPS = s.stRPS[h.stRPSIdx]
			}

			if s.longTermRefPicsPresent {
				lt, err := parseLongTermRPS(&c, s)
				if err != nil {
					return nil, err
				}

				h.ltRPS = lt
			}

			if s.temporalMvpEnabled {
				h.temporalMvp = c.bit() != 0
			}
		}

		h.numPocTotalCurr = numPocTotalCurr(&h.stRPS, &h.ltRPS)

		if s.saoEnabled {
			h.saoLuma = c.bit() != 0

			if s.chromaArrayType() != 0 {
				h.saoChroma = c.bit() != 0
			}
		}

		if h.sliceType != sliceI {
			if c.bit() != 0 {
				h.numRefIdxL0Active = c.ue() + 1

				if h.sliceType == sliceB {
					h.numRefIdxL1Active = c.ue() + 1
				}
			}

			if h.numRefIdxL0Active > maxRefs || h.numRefIdxL1Active > maxRefs {
				return nil, ErrInvalid
			}

			if p.listsModificationPresent && h.numPocTotalCurr > 1 {
				if err := parseRefPicListModification(&c, h); err != nil {
					return nil, err
				}
			}

			if h.sliceType == sliceB {
				h.mvdL1Zero = c.bit() != 0
			}

			if p.cabacInitPresent {
				h.cabacInit = c.bit() != 0
			}

			if h.temporalMvp {
				if h.sliceType == sliceB {
					h.collocatedFromL0 = c.bit() != 0
				}

				active := h.numRefIdxL1Active
				if h.collocatedFromL0 {
					active = h.numRefIdxL0Active
				}

				if active > 1 {
					h.collocatedRefIdx = c.ue()
					if h.collocatedRefIdx >= active {
						return nil, ErrInvalid
					}
				}
			}

			if (p.weightedPred && h.sliceType == sliceP) ||
				(p.weightedBipred && h.sliceType == sliceB) {
				if err := parsePredWeightTable(&c, h, s); err != nil {
					return nil, err
				}
			}

			n := c.ue()
			if n > 4 {
				return nil, ErrInvalid
			}

			h.maxNumMergeCand = 5 - n
		}

		h.qpDelta = c.se()
		h.qpY = p.initQP + h.qpDelta

		if h.qpY < -(6*int32(s.bitDepthLuma)-48) || h.qpY > 51 {
			return nil, ErrInvalid
		}

		if p.sliceChromaQPOffsets {
			h.cbQPOffset = c.se()
			h.crQPOffset = c.se()

			if h.cbQPOffset < -12 || h.cbQPOffset > 12 ||
				h.crQPOffset < -12 || h.crQPOffset > 12 {
				return nil, ErrInvalid
			}
		}

		if p.chromaQPOffsetList {
			h.cuChromaQPOffset = c.bit() != 0
		}

		if p.deblockingOverride && c.bit() != 0 {
			h.deblockingDisabled = c.bit() != 0

			if !h.deblockingDisabled {
				h.betaOffsetDiv2 = c.se()
				h.tcOffsetDiv2 = c.se()

				if h.betaOffsetDiv2 < -6 || h.betaOffsetDiv2 > 6 ||
					h.tcOffsetDiv2 < -6 || h.tcOffsetDiv2 > 6 {
					return nil, ErrInvalid
				}
			}
		}

		if p.loopFilterAcrossSlices && (h.saoLuma || h.saoChroma || !h.deblockingDisabled) {
			h.loopFilterAcross = c.bit() != 0
		}
	}

	if p.tilesEnabled || p.entropyCodingSync {
		n := c.ue()
		if n > numCtbs {
			return nil, ErrInvalid
		}

		if n > 0 {
			lenMinus1 := c.ue()
			if lenMinus1 > 31 {
				return nil, ErrInvalid
			}

			h.entryPointOffsets = make([]uint32, n)

			var cum uint32

			for i := range h.entryPointOffsets {
				v := c.bits(int(lenMinus1) + 1)

				if cum+v+1 < cum {
					return nil, ErrInvalid
				}

				cum += v + 1
				h.entryPointOffsets[i] = cum
			}
		}
	}

	if p.sliceHeaderExtensionPresen {
		n := c.ue()
		if n > uint32(len(rbsp)) {
			return nil, ErrInvalid
		}

		c.skip(int(n) * 8)
	}

	if c.bit() != 1 {
		return nil, ErrInvalid
	}

	c.byteAlign()

	if c.err {
		return nil, ErrInvalid
	}

	h.dataOffset = c.pos() / 8

	return h, nil
}

func numPocTotalCurr(st *shortTermRPS, lt *longTermRPS) uint32 {
	var n uint32

	for _, u := range st.usedS0 {
		if u {
			n++
		}
	}

	for _, u := range st.usedS1 {
		if u {
			n++
		}
	}

	for _, u := range lt.usedByCurrPicLt {
		if u {
			n++
		}
	}

	return n
}

func parseRefPicListModification(c *getBits, h *sliceHeader) error {
	n := ceilLog2(h.numPocTotalCurr)

	h.listModification.flagL0 = c.bit() != 0
	if h.listModification.flagL0 {
		h.listModification.listEntryL0 = make([]uint32, h.numRefIdxL0Active)

		for i := range h.listModification.listEntryL0 {
			v := c.bits(n)
			if v >= h.numPocTotalCurr {
				return ErrInvalid
			}

			h.listModification.listEntryL0[i] = v
		}
	}

	if h.sliceType != sliceB {
		return nil
	}

	h.listModification.flagL1 = c.bit() != 0
	if h.listModification.flagL1 {
		h.listModification.listEntryL1 = make([]uint32, h.numRefIdxL1Active)

		for i := range h.listModification.listEntryL1 {
			v := c.bits(n)
			if v >= h.numPocTotalCurr {
				return ErrInvalid
			}

			h.listModification.listEntryL1[i] = v
		}
	}

	return nil
}

func parseLongTermRPS(c *getBits, s *sps) (longTermRPS, error) {
	var lt longTermRPS

	var numSps uint32
	if len(s.ltRefPicPocLsb) > 0 {
		numSps = c.ue()
	}

	numPics := c.ue()

	if numSps > uint32(len(s.ltRefPicPocLsb)) || numSps+numPics > maxLongTermRefPics {
		return lt, ErrInvalid
	}

	total := int(numSps + numPics)
	lt.numSps = int(numSps)
	lt.pocLsbLt = make([]uint32, total)
	lt.usedByCurrPicLt = make([]bool, total)
	lt.deltaPocMsbPresn = make([]bool, total)
	lt.deltaPocMsbCycle = make([]uint32, total)

	for i := range total {
		if uint32(i) < numSps {
			var idx uint32
			if len(s.ltRefPicPocLsb) > 1 {
				idx = c.bits(ceilLog2(uint32(len(s.ltRefPicPocLsb))))
			}

			if int(idx) >= len(s.ltRefPicPocLsb) {
				return lt, ErrInvalid
			}

			lt.pocLsbLt[i] = s.ltRefPicPocLsb[idx]
			lt.usedByCurrPicLt[i] = s.usedByCurrPicLt[idx]
		} else {
			lt.pocLsbLt[i] = c.bits(int(s.log2MaxPocLsb))
			lt.usedByCurrPicLt[i] = c.bit() != 0
		}

		lt.deltaPocMsbPresn[i] = c.bit() != 0
		if lt.deltaPocMsbPresn[i] {
			lt.deltaPocMsbCycle[i] = c.ue()
		}
	}

	return lt, nil
}

func parsePredWeightTable(c *getBits, h *sliceHeader, s *sps) error {
	w := &h.weights

	denom := c.ue()
	if denom > 7 {
		return ErrInvalid
	}

	w.lumaLog2Denom = uint8(denom)

	chromaDenom := int32(denom)
	if s.chromaArrayType() != 0 {
		chromaDenom += c.se()

		if chromaDenom < 0 || chromaDenom > 7 {
			return ErrInvalid
		}
	}

	w.chromaLog2Denom = uint8(chromaDenom)

	lists := 1
	if h.sliceType == sliceB {
		lists = 2
	}

	counts := [2]uint32{h.numRefIdxL0Active, h.numRefIdxL1Active}

	for l := range lists {
		n := int(counts[l])

		var lumaFlag, chromaFlag [maxRefs]bool

		for i := range n {
			lumaFlag[i] = c.bit() != 0
		}

		if s.chromaArrayType() != 0 {
			for i := range n {
				chromaFlag[i] = c.bit() != 0
			}
		}

		for i := range n {
			if lumaFlag[i] {
				w.lumaWeight[l][i] = int16(1<<w.lumaLog2Denom) + int16(c.se())
				w.lumaOffset[l][i] = int16(c.se())
			} else {
				w.lumaWeight[l][i] = 1 << w.lumaLog2Denom
			}

			if !chromaFlag[i] {
				w.chromaWeight[l][i] = [2]int16{1 << w.chromaLog2Denom, 1 << w.chromaLog2Denom}

				continue
			}

			for j := range 2 {
				dw := c.se()
				do := c.se()

				weight := int16(1<<w.chromaLog2Denom) + int16(dw)
				w.chromaWeight[l][i][j] = weight

				off := int32(do) - int32(128*int32(weight)>>w.chromaLog2Denom) + 128
				if off < -128 {
					off = -128
				} else if off > 127 {
					off = 127
				}

				w.chromaOffset[l][i][j] = int16(off)
			}
		}
	}

	return nil
}
