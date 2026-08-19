package hevc

import "errors"

// ErrInvalid is returned for a bitstream that cannot be decoded, and
// ErrUnsupported for one using a feature this decoder does not implement.
var (
	ErrInvalid     = errors.New("hevc: invalid bitstream")
	ErrUnsupported = errors.New("hevc: unsupported feature")
)

const (
	maxSubLayers        = 7
	maxShortTermRPS     = 64
	maxLongTermRefPics  = 32
	maxRefPicsPerRPS    = 16
	maxTileColumns      = 22
	maxTileRows         = 20
	maxPicSize          = 16384
	maxChromaQPOffsets  = 6
	minCtbLog2SizeY     = 4
	maxCtbLog2SizeY     = 6
	maxScalingListSizes = 4
	maxScalingListMats  = 6
)

type profileTierLevel struct {
	profileSpace uint8
	tierFlag     bool
	profileIDC   uint8
	levelIDC     uint8
	compatFlags  uint32
}

type shortTermRPS struct {
	deltaPocS0 []int32
	deltaPocS1 []int32
	usedS0     []bool
	usedS1     []bool
}

func (r *shortTermRPS) numDeltaPocs() int {
	return len(r.deltaPocS0) + len(r.deltaPocS1)
}

type scalingList struct {
	sl [maxScalingListSizes][maxScalingListMats][64]uint8
	dc [2][maxScalingListMats]uint8
}

type vps struct {
	id                 uint8
	maxLayersMinus1    uint8
	maxSubLayersMinus1 uint8
	temporalIDNesting  bool
	ptl                profileTierLevel
	maxDecPicBuffering [maxSubLayers]uint32
	maxNumReorderPics  [maxSubLayers]uint32
	maxLatencyIncrease [maxSubLayers]uint32
}

type sps struct {
	vpsID              uint8
	maxSubLayersMinus1 uint8
	temporalIDNesting  bool
	ptl                profileTierLevel
	id                 uint32

	chromaFormatIDC        uint32
	separateColourPlane    bool
	subWidthC, subHeightC  int
	picWidthInLumaSamples  uint32
	picHeightInLumaSamples uint32

	confWinLeft, confWinRight uint32
	confWinTop, confWinBottom uint32

	bitDepthLuma, bitDepthChroma uint8

	// E.3.1: the color description of the sequence, unspecified unless the
	// video usability information says otherwise.
	colourPrimaries uint16
	transferChar    uint16
	matrixCoeffs    uint16
	fullRange       bool
	log2MaxPocLsb   uint8

	maxDecPicBuffering uint32
	maxNumReorderPics  uint32
	maxLatencyIncrease uint32

	minCbLog2SizeY  uint8
	ctbLog2SizeY    uint8
	ctbSizeY        uint32
	minTbLog2SizeY  uint8
	maxTbLog2SizeY  uint8
	maxTrHierInter  uint32
	maxTrHierIntra  uint32
	picWidthInCtbs  uint32
	picHeightInCtbs uint32

	scalingListEnabled bool
	scalingList        scalingList

	ampEnabled bool
	saoEnabled bool

	pcmEnabled            bool
	pcmBitDepthLuma       uint8
	pcmBitDepthChroma     uint8
	log2MinPcmCbSize      uint8
	log2MaxPcmCbSize      uint8
	pcmLoopFilterDisabled bool

	stRPS                  []shortTermRPS
	longTermRefPicsPresent bool
	ltRefPicPocLsb         []uint32
	usedByCurrPicLt        []bool

	temporalMvpEnabled       bool
	strongIntraSmoothing     bool
	transformSkipRotation    bool
	transformSkipContext     bool
	implicitRdpcm            bool
	explicitRdpcm            bool
	extendedPrecision        bool
	intraSmoothingDisabled   bool
	highPrecisionOffsets     bool
	persistentRiceAdaptation bool
	cabacBypassAlignment     bool
}

type pps struct {
	id    uint32
	spsID uint32

	dependentSliceSegmentsEnabled bool
	outputFlagPresent             bool
	numExtraSliceHeaderBits       uint8
	signDataHidingEnabled         bool
	cabacInitPresent              bool

	numRefIdxL0DefaultActive uint32
	numRefIdxL1DefaultActive uint32

	initQP                int32
	constrainedIntraPred  bool
	transformSkipEnabled  bool
	cuQPDeltaEnabled      bool
	diffCuQPDeltaDepth    uint32
	cbQPOffset            int32
	crQPOffset            int32
	sliceChromaQPOffsets  bool
	weightedPred          bool
	weightedBipred        bool
	transquantBypass      bool
	tilesEnabled          bool
	entropyCodingSync     bool
	numTileColumns        int
	numTileRows           int
	uniformSpacing        bool
	columnWidthMinus1     []uint32
	rowHeightMinus1       []uint32
	loopFilterAcrossTiles bool
	colWidthsInCtbs       []uint32
	rowHeightsInCtbs      []uint32

	loopFilterAcrossSlices  bool
	deblockingControlPresen bool
	deblockingOverride      bool
	deblockingDisabled      bool
	betaOffsetDiv2          int32
	tcOffsetDiv2            int32

	scalingListPresent bool
	scalingList        scalingList

	listsModificationPresent   bool
	log2ParallelMergeLevel     uint32
	sliceHeaderExtensionPresen bool

	log2MaxTransformSkipSize uint32
	crossComponentPrediction bool
	chromaQPOffsetList       bool
	diffCuChromaQPOffsetDep  uint32
	chromaQPOffsetListLen    uint32
	cbQPOffsetList           [maxChromaQPOffsets]int32
	crQPOffsetList           [maxChromaQPOffsets]int32
	log2SaoOffsetScaleLuma   uint32
	log2SaoOffsetScaleChroma uint32
}

func parseProfileTierLevel(c *getBits, profilePresent bool, maxSubLayersMinus1 uint8) profileTierLevel {
	var p profileTierLevel

	if profilePresent {
		p.profileSpace = uint8(c.bits(2))
		p.tierFlag = c.bit() != 0
		p.profileIDC = uint8(c.bits(5))
		p.compatFlags = c.bits(32)
		c.skip(48)
	}

	p.levelIDC = uint8(c.bits(8))

	if maxSubLayersMinus1 == 0 {
		return p
	}

	var profilePresentSub, levelPresentSub [maxSubLayers]bool

	for i := range int(maxSubLayersMinus1) {
		profilePresentSub[i] = c.bit() != 0
		levelPresentSub[i] = c.bit() != 0
	}

	for range 8 - int(maxSubLayersMinus1) {
		c.bits(2)
	}

	for i := range int(maxSubLayersMinus1) {
		if profilePresentSub[i] {
			c.skip(88)
		}

		if levelPresentSub[i] {
			c.bits(8)
		}
	}

	return p
}

func parseSubLayerHRD(c *getBits, cpbCnt int, subPicPresent bool) {
	for range cpbCnt {
		c.ue()
		c.ue()

		if subPicPresent {
			c.ue()
			c.ue()
		}

		c.bit()
	}
}

func parseHRD(c *getBits, commonInfPresent bool, maxSubLayersMinus1 uint8) error {
	var nalHRD, vclHRD, subPicPresent bool

	if commonInfPresent {
		nalHRD = c.bit() != 0
		vclHRD = c.bit() != 0

		if nalHRD || vclHRD {
			subPicPresent = c.bit() != 0
			if subPicPresent {
				c.bits(8)
				c.bits(5)
				c.bit()
				c.bits(5)
			}

			c.bits(4)
			c.bits(4)

			if subPicPresent {
				c.bits(4)
			}

			c.bits(5)
			c.bits(5)
			c.bits(5)
		}
	}

	for range int(maxSubLayersMinus1) + 1 {
		fixedRate := c.bit() != 0
		if !fixedRate {
			fixedRate = c.bit() != 0
		}

		lowDelay := false

		if fixedRate {
			c.ue()
		} else {
			lowDelay = c.bit() != 0
		}

		cpbCnt := 1
		if !lowDelay {
			n := c.ue()
			if n > 31 {
				return ErrInvalid
			}

			cpbCnt = int(n) + 1
		}

		if nalHRD {
			parseSubLayerHRD(c, cpbCnt, subPicPresent)
		}

		if vclHRD {
			parseSubLayerHRD(c, cpbCnt, subPicPresent)
		}
	}

	return nil
}

func parseVUI(c *getBits, s *sps, maxSubLayersMinus1 uint8) error {
	if c.bit() != 0 {
		if c.bits(8) == 255 {
			c.bits(16)
			c.bits(16)
		}
	}

	if c.bit() != 0 {
		c.bit()
	}

	// E.2.1 video_signal_type. The color description defaults to unspecified,
	// which newPicture carries so a container with no description of its own
	// can fall back to what the sequence declares.
	if c.bit() != 0 {
		c.bits(3)

		s.fullRange = c.bit() != 0

		if c.bit() != 0 {
			s.colourPrimaries = uint16(c.bits(8))
			s.transferChar = uint16(c.bits(8))
			s.matrixCoeffs = uint16(c.bits(8))
		}
	}

	if c.bit() != 0 {
		c.ue()
		c.ue()
	}

	c.bit()
	c.bit()
	c.bit()

	if c.bit() != 0 {
		c.ue()
		c.ue()
		c.ue()
		c.ue()
	}

	if c.bit() != 0 {
		c.bits(32)
		c.bits(32)

		if c.bit() != 0 {
			c.ue()
		}

		if c.bit() != 0 {
			if err := parseHRD(c, true, maxSubLayersMinus1); err != nil {
				return err
			}
		}
	}

	if c.bit() != 0 {
		c.bit()
		c.bit()
		c.bit()
		c.ue()
		c.ue()
		c.ue()
		c.ue()
		c.ue()
	}

	return nil
}

func parseVPS(rbsp []byte) (*vps, error) {
	var c getBits
	c.init(rbsp)

	v := &vps{}
	v.id = uint8(c.bits(4))
	c.bits(2)
	v.maxLayersMinus1 = uint8(c.bits(6))
	v.maxSubLayersMinus1 = uint8(c.bits(3))

	if v.maxSubLayersMinus1 > maxSubLayers-1 {
		return nil, ErrInvalid
	}

	v.temporalIDNesting = c.bit() != 0

	if c.bits(16) != 0xffff {
		return nil, ErrInvalid
	}

	v.ptl = parseProfileTierLevel(&c, true, v.maxSubLayersMinus1)

	start := int(v.maxSubLayersMinus1)
	if c.bit() != 0 {
		start = 0
	}

	for i := start; i <= int(v.maxSubLayersMinus1); i++ {
		v.maxDecPicBuffering[i] = c.ue()
		v.maxNumReorderPics[i] = c.ue()
		v.maxLatencyIncrease[i] = c.ue()
	}

	maxLayerID := int(c.bits(6))

	numLayerSetsMinus1 := c.ue()
	if numLayerSetsMinus1 > 1023 {
		return nil, ErrInvalid
	}

	for range int(numLayerSetsMinus1) {
		c.skip(maxLayerID + 1)
	}

	if c.bit() != 0 {
		c.bits(32)
		c.bits(32)

		if c.bit() != 0 {
			c.ue()
		}

		numHRD := c.ue()
		if numHRD > 1024 {
			return nil, ErrInvalid
		}

		for i := range int(numHRD) {
			c.ue()

			commonInfPresent := true
			if i > 0 {
				commonInfPresent = c.bit() != 0
			}

			if err := parseHRD(&c, commonInfPresent, v.maxSubLayersMinus1); err != nil {
				return nil, err
			}
		}
	}

	extension := c.bit() != 0

	if err := checkTrailing(&c, extension); err != nil {
		return nil, err
	}

	return v, nil
}

func checkTrailing(c *getBits, extension bool) error {
	if c.err {
		return ErrInvalid
	}

	if !extension && c.moreRBSPData() {
		return ErrInvalid
	}

	return nil
}

func parseSPS(rbsp []byte) (*sps, error) {
	var c getBits
	c.init(rbsp)

	// E.3.1 infers all three as unspecified when the sequence does not say.
	s := &sps{colourPrimaries: 2, transferChar: 2, matrixCoeffs: 2}
	s.vpsID = uint8(c.bits(4))
	s.maxSubLayersMinus1 = uint8(c.bits(3))

	if s.maxSubLayersMinus1 > maxSubLayers-1 {
		return nil, ErrInvalid
	}

	s.temporalIDNesting = c.bit() != 0
	s.ptl = parseProfileTierLevel(&c, true, s.maxSubLayersMinus1)

	s.id = c.ue()
	if s.id > 15 {
		return nil, ErrInvalid
	}

	s.chromaFormatIDC = c.ue()
	if s.chromaFormatIDC > 3 {
		return nil, ErrInvalid
	}

	if s.chromaFormatIDC == 3 {
		s.separateColourPlane = c.bit() != 0
	}

	s.subWidthC, s.subHeightC = 1, 1

	switch s.chromaFormatIDC {
	case 1:
		s.subWidthC, s.subHeightC = 2, 2
	case 2:
		s.subWidthC = 2
	}

	s.picWidthInLumaSamples = c.ue()
	s.picHeightInLumaSamples = c.ue()

	if s.picWidthInLumaSamples == 0 || s.picHeightInLumaSamples == 0 ||
		s.picWidthInLumaSamples > maxPicSize || s.picHeightInLumaSamples > maxPicSize {
		return nil, ErrInvalid
	}

	if c.bit() != 0 {
		s.confWinLeft = c.ue()
		s.confWinRight = c.ue()
		s.confWinTop = c.ue()
		s.confWinBottom = c.ue()
	}

	bitDepthLumaMinus8 := c.ue()
	bitDepthChromaMinus8 := c.ue()

	if bitDepthLumaMinus8 > 8 || bitDepthChromaMinus8 > 8 {
		return nil, ErrUnsupported
	}

	s.bitDepthLuma = 8 + uint8(bitDepthLumaMinus8)
	s.bitDepthChroma = 8 + uint8(bitDepthChromaMinus8)

	log2MaxPocLsbMinus4 := c.ue()
	if log2MaxPocLsbMinus4 > 12 {
		return nil, ErrInvalid
	}

	s.log2MaxPocLsb = 4 + uint8(log2MaxPocLsbMinus4)

	start := int(s.maxSubLayersMinus1)
	if c.bit() != 0 {
		start = 0
	}

	for i := start; i <= int(s.maxSubLayersMinus1); i++ {
		s.maxDecPicBuffering = c.ue()
		s.maxNumReorderPics = c.ue()
		s.maxLatencyIncrease = c.ue()
	}

	log2MinCbSizeMinus3 := c.ue()
	log2DiffMaxMinCbSize := c.ue()

	if log2MinCbSizeMinus3 > 3 || log2DiffMaxMinCbSize > 3 {
		return nil, ErrInvalid
	}

	s.minCbLog2SizeY = 3 + uint8(log2MinCbSizeMinus3)
	s.ctbLog2SizeY = s.minCbLog2SizeY + uint8(log2DiffMaxMinCbSize)

	if s.ctbLog2SizeY < minCtbLog2SizeY || s.ctbLog2SizeY > maxCtbLog2SizeY {
		return nil, ErrInvalid
	}

	// 7.4.3.2: both dimensions are an integer multiple of MinCbSizeY. Without
	// it a coding unit at the edge extends past the picture, and the block
	// bookkeeping is sized for what the picture holds.
	if mask := uint32(1)<<s.minCbLog2SizeY - 1; s.picWidthInLumaSamples&mask != 0 ||
		s.picHeightInLumaSamples&mask != 0 {
		return nil, ErrInvalid
	}

	s.ctbSizeY = 1 << s.ctbLog2SizeY
	s.picWidthInCtbs = ceilDiv(s.picWidthInLumaSamples, s.ctbSizeY)
	s.picHeightInCtbs = ceilDiv(s.picHeightInLumaSamples, s.ctbSizeY)

	log2MinTbSizeMinus2 := c.ue()
	log2DiffMaxMinTbSize := c.ue()

	if log2MinTbSizeMinus2 > 3 || log2DiffMaxMinTbSize > 3 {
		return nil, ErrInvalid
	}

	s.minTbLog2SizeY = 2 + uint8(log2MinTbSizeMinus2)
	s.maxTbLog2SizeY = s.minTbLog2SizeY + uint8(log2DiffMaxMinTbSize)

	if s.minTbLog2SizeY >= s.minCbLog2SizeY || s.maxTbLog2SizeY > s.ctbLog2SizeY {
		return nil, ErrInvalid
	}

	s.maxTrHierInter = c.ue()
	s.maxTrHierIntra = c.ue()

	s.scalingList = defaultScalingList()

	s.scalingListEnabled = c.bit() != 0
	if s.scalingListEnabled && c.bit() != 0 {
		if err := parseScalingListData(&c, &s.scalingList); err != nil {
			return nil, err
		}
	}

	s.ampEnabled = c.bit() != 0
	s.saoEnabled = c.bit() != 0

	s.log2MinPcmCbSize, s.log2MaxPcmCbSize = 8, 0
	s.pcmBitDepthLuma, s.pcmBitDepthChroma = s.bitDepthLuma, s.bitDepthChroma

	s.pcmEnabled = c.bit() != 0
	if s.pcmEnabled {
		s.pcmBitDepthLuma = 1 + uint8(c.bits(4))
		s.pcmBitDepthChroma = 1 + uint8(c.bits(4))

		if s.pcmBitDepthLuma > s.bitDepthLuma || s.pcmBitDepthChroma > s.bitDepthChroma {
			return nil, ErrInvalid
		}

		log2MinPcmCbSizeMinus3 := c.ue()
		log2DiffMaxMinPcmCbSize := c.ue()

		if log2MinPcmCbSizeMinus3 > 2 || log2DiffMaxMinPcmCbSize > 2 {
			return nil, ErrInvalid
		}

		s.log2MinPcmCbSize = 3 + uint8(log2MinPcmCbSizeMinus3)
		s.log2MaxPcmCbSize = s.log2MinPcmCbSize + uint8(log2DiffMaxMinPcmCbSize)
		s.pcmLoopFilterDisabled = c.bit() != 0
	}

	numStRPS := c.ue()
	if numStRPS > maxShortTermRPS {
		return nil, ErrInvalid
	}

	s.stRPS = make([]shortTermRPS, 0, numStRPS)

	for i := range int(numStRPS) {
		rps, err := parseShortTermRPS(&c, i, int(numStRPS), s.stRPS)
		if err != nil {
			return nil, err
		}

		s.stRPS = append(s.stRPS, rps)
	}

	s.longTermRefPicsPresent = c.bit() != 0
	if s.longTermRefPicsPresent {
		n := c.ue()
		if n > maxLongTermRefPics {
			return nil, ErrInvalid
		}

		s.ltRefPicPocLsb = make([]uint32, n)
		s.usedByCurrPicLt = make([]bool, n)

		for i := range int(n) {
			s.ltRefPicPocLsb[i] = c.bits(int(s.log2MaxPocLsb))
			s.usedByCurrPicLt[i] = c.bit() != 0
		}
	}

	s.temporalMvpEnabled = c.bit() != 0
	s.strongIntraSmoothing = c.bit() != 0

	if c.bit() != 0 {
		if err := parseVUI(&c, s, s.maxSubLayersMinus1); err != nil {
			return nil, err
		}
	}

	unparsed := false

	if c.bit() != 0 {
		rangeExtension := c.bit() != 0
		multilayerExtension := c.bit() != 0
		extension3D := c.bit() != 0
		sccExtension := c.bit() != 0
		unparsed = c.bits(4) != 0

		if rangeExtension {
			s.transformSkipRotation = c.bit() != 0
			s.transformSkipContext = c.bit() != 0
			s.implicitRdpcm = c.bit() != 0
			s.explicitRdpcm = c.bit() != 0
			s.extendedPrecision = c.bit() != 0
			s.intraSmoothingDisabled = c.bit() != 0
			s.highPrecisionOffsets = c.bit() != 0
			s.persistentRiceAdaptation = c.bit() != 0
			s.cabacBypassAlignment = c.bit() != 0
		}

		// The tools below are parsed so the extension stays in step, but they
		// are not applied. A stream that uses one is refused rather than
		// decoded into something that merely looks plausible.
		if s.implicitRdpcm || s.explicitRdpcm || s.cabacBypassAlignment {
			return nil, ErrUnsupported
		}

		if multilayerExtension || extension3D || sccExtension {
			return nil, ErrUnsupported
		}
	}

	if err := checkTrailing(&c, unparsed); err != nil {
		return nil, err
	}

	return s, nil
}

func parsePPS(rbsp []byte) (*pps, error) {
	var c getBits
	c.init(rbsp)

	p := &pps{}
	p.id = c.ue()
	p.spsID = c.ue()

	if p.id > 63 || p.spsID > 15 {
		return nil, ErrInvalid
	}

	p.dependentSliceSegmentsEnabled = c.bit() != 0
	p.outputFlagPresent = c.bit() != 0
	p.numExtraSliceHeaderBits = uint8(c.bits(3))
	p.signDataHidingEnabled = c.bit() != 0
	p.cabacInitPresent = c.bit() != 0
	p.numRefIdxL0DefaultActive = c.ue() + 1
	p.numRefIdxL1DefaultActive = c.ue() + 1

	if p.numRefIdxL0DefaultActive > 16 || p.numRefIdxL1DefaultActive > 16 {
		return nil, ErrInvalid
	}

	p.initQP = c.se() + 26
	p.constrainedIntraPred = c.bit() != 0
	p.transformSkipEnabled = c.bit() != 0

	p.cuQPDeltaEnabled = c.bit() != 0
	if p.cuQPDeltaEnabled {
		p.diffCuQPDeltaDepth = c.ue()
	}

	p.cbQPOffset = c.se()
	p.crQPOffset = c.se()

	if p.cbQPOffset < -12 || p.cbQPOffset > 12 || p.crQPOffset < -12 || p.crQPOffset > 12 {
		return nil, ErrInvalid
	}

	p.sliceChromaQPOffsets = c.bit() != 0
	p.weightedPred = c.bit() != 0
	p.weightedBipred = c.bit() != 0
	p.transquantBypass = c.bit() != 0
	p.tilesEnabled = c.bit() != 0
	p.entropyCodingSync = c.bit() != 0

	p.log2MaxTransformSkipSize = 2
	p.numTileColumns, p.numTileRows = 1, 1
	p.uniformSpacing = true
	p.loopFilterAcrossTiles = true

	if p.tilesEnabled {
		p.numTileColumns = int(c.ue()) + 1
		p.numTileRows = int(c.ue()) + 1

		if p.numTileColumns > maxTileColumns || p.numTileRows > maxTileRows {
			return nil, ErrInvalid
		}

		p.uniformSpacing = c.bit() != 0
		if !p.uniformSpacing {
			p.columnWidthMinus1 = make([]uint32, p.numTileColumns-1)
			for i := range p.columnWidthMinus1 {
				p.columnWidthMinus1[i] = c.ue()
			}

			p.rowHeightMinus1 = make([]uint32, p.numTileRows-1)
			for i := range p.rowHeightMinus1 {
				p.rowHeightMinus1[i] = c.ue()
			}
		}

		p.loopFilterAcrossTiles = c.bit() != 0
	}

	p.loopFilterAcrossSlices = c.bit() != 0

	p.deblockingControlPresen = c.bit() != 0
	if p.deblockingControlPresen {
		p.deblockingOverride = c.bit() != 0
		p.deblockingDisabled = c.bit() != 0

		if !p.deblockingDisabled {
			p.betaOffsetDiv2 = c.se()
			p.tcOffsetDiv2 = c.se()

			if p.betaOffsetDiv2 < -6 || p.betaOffsetDiv2 > 6 ||
				p.tcOffsetDiv2 < -6 || p.tcOffsetDiv2 > 6 {
				return nil, ErrInvalid
			}
		}
	}

	p.scalingList = defaultScalingList()

	p.scalingListPresent = c.bit() != 0
	if p.scalingListPresent {
		if err := parseScalingListData(&c, &p.scalingList); err != nil {
			return nil, err
		}
	}

	p.listsModificationPresent = c.bit() != 0
	p.log2ParallelMergeLevel = c.ue() + 2
	p.sliceHeaderExtensionPresen = c.bit() != 0

	unparsed := false

	if c.bit() != 0 {
		rangeExtension := c.bit() != 0
		multilayerExtension := c.bit() != 0
		extension3D := c.bit() != 0
		sccExtension := c.bit() != 0
		unparsed = c.bits(4) != 0

		if rangeExtension {
			if p.transformSkipEnabled {
				p.log2MaxTransformSkipSize = c.ue() + 2
			}

			p.crossComponentPrediction = c.bit() != 0

			p.chromaQPOffsetList = c.bit() != 0
			if p.chromaQPOffsetList {
				p.diffCuChromaQPOffsetDep = c.ue()
				p.chromaQPOffsetListLen = c.ue() + 1

				if p.chromaQPOffsetListLen > maxChromaQPOffsets {
					return nil, ErrInvalid
				}

				for i := range int(p.chromaQPOffsetListLen) {
					p.cbQPOffsetList[i] = c.se()
					p.crQPOffsetList[i] = c.se()
				}
			}

			p.log2SaoOffsetScaleLuma = c.ue()
			p.log2SaoOffsetScaleChroma = c.ue()
		}

		// Parsed to keep the extension in step, but not applied.
		if p.crossComponentPrediction {
			return nil, ErrUnsupported
		}

		if multilayerExtension || extension3D || sccExtension {
			return nil, ErrUnsupported
		}
	}

	if err := checkTrailing(&c, unparsed); err != nil {
		return nil, err
	}

	return p, nil
}

func (p *pps) resolveTileGeometry(s *sps) error {
	w, h := int(s.picWidthInCtbs), int(s.picHeightInCtbs)

	if !p.tilesEnabled {
		p.colWidthsInCtbs = []uint32{uint32(w)}
		p.rowHeightsInCtbs = []uint32{uint32(h)}

		return nil
	}

	if p.numTileColumns > w || p.numTileRows > h {
		return ErrInvalid
	}

	cols := make([]uint32, p.numTileColumns)
	rows := make([]uint32, p.numTileRows)

	if p.uniformSpacing {
		for i := range cols {
			cols[i] = uint32((i+1)*w/p.numTileColumns - i*w/p.numTileColumns)
		}

		for i := range rows {
			rows[i] = uint32((i+1)*h/p.numTileRows - i*h/p.numTileRows)
		}
	} else {
		var sum uint32

		for i, v := range p.columnWidthMinus1 {
			cols[i] = v + 1
			sum += cols[i]
		}

		if sum >= uint32(w) {
			return ErrInvalid
		}

		cols[len(cols)-1] = uint32(w) - sum

		sum = 0

		for i, v := range p.rowHeightMinus1 {
			rows[i] = v + 1
			sum += rows[i]
		}

		if sum >= uint32(h) {
			return ErrInvalid
		}

		rows[len(rows)-1] = uint32(h) - sum
	}

	p.colWidthsInCtbs = cols
	p.rowHeightsInCtbs = rows

	return nil
}

func (s *sps) croppedWidth() uint32 {
	crop := uint32(s.subWidthC) * (s.confWinLeft + s.confWinRight)
	if crop >= s.picWidthInLumaSamples {
		return 0
	}

	return s.picWidthInLumaSamples - crop
}

func (s *sps) croppedHeight() uint32 {
	crop := uint32(s.subHeightC) * (s.confWinTop + s.confWinBottom)
	if crop >= s.picHeightInLumaSamples {
		return 0
	}

	return s.picHeightInLumaSamples - crop
}

func parseShortTermRPS(c *getBits, idx, numStRPS int, prev []shortTermRPS) (shortTermRPS, error) {
	var rps shortTermRPS

	interPred := false
	if idx != 0 {
		interPred = c.bit() != 0
	}

	if interPred {
		deltaIdx := 1
		if idx == numStRPS {
			deltaIdx = int(c.ue()) + 1
		}

		if deltaIdx > idx {
			return rps, ErrInvalid
		}

		ref := &prev[idx-deltaIdx]

		sign := c.bit() != 0

		absDeltaRpsMinus1 := c.ue()
		if absDeltaRpsMinus1 >= 32768 {
			return rps, ErrInvalid
		}

		deltaRps := int32(absDeltaRpsMinus1) + 1
		if sign {
			deltaRps = -deltaRps
		}

		n := ref.numDeltaPocs()
		used := make([]bool, n+1)
		useDelta := make([]bool, n+1)

		for j := range n + 1 {
			used[j] = c.bit() != 0

			useDelta[j] = true
			if !used[j] {
				useDelta[j] = c.bit() != 0
			}
		}

		numNeg, numPos := len(ref.deltaPocS0), len(ref.deltaPocS1)

		for j := numPos - 1; j >= 0; j-- {
			if d := ref.deltaPocS1[j] + deltaRps; d < 0 && useDelta[numNeg+j] {
				rps.deltaPocS0 = append(rps.deltaPocS0, d)
				rps.usedS0 = append(rps.usedS0, used[numNeg+j])
			}
		}

		if deltaRps < 0 && useDelta[n] {
			rps.deltaPocS0 = append(rps.deltaPocS0, deltaRps)
			rps.usedS0 = append(rps.usedS0, used[n])
		}

		for j := range numNeg {
			if d := ref.deltaPocS0[j] + deltaRps; d < 0 && useDelta[j] {
				rps.deltaPocS0 = append(rps.deltaPocS0, d)
				rps.usedS0 = append(rps.usedS0, used[j])
			}
		}

		for j := numNeg - 1; j >= 0; j-- {
			if d := ref.deltaPocS0[j] + deltaRps; d > 0 && useDelta[j] {
				rps.deltaPocS1 = append(rps.deltaPocS1, d)
				rps.usedS1 = append(rps.usedS1, used[j])
			}
		}

		if deltaRps > 0 && useDelta[n] {
			rps.deltaPocS1 = append(rps.deltaPocS1, deltaRps)
			rps.usedS1 = append(rps.usedS1, used[n])
		}

		for j := range numPos {
			if d := ref.deltaPocS1[j] + deltaRps; d > 0 && useDelta[numNeg+j] {
				rps.deltaPocS1 = append(rps.deltaPocS1, d)
				rps.usedS1 = append(rps.usedS1, used[numNeg+j])
			}
		}

		return rps, nil
	}

	numNeg := c.ue()
	numPos := c.ue()

	if numNeg > maxRefPicsPerRPS || numPos > maxRefPicsPerRPS {
		return rps, ErrInvalid
	}

	rps.deltaPocS0 = make([]int32, numNeg)
	rps.usedS0 = make([]bool, numNeg)

	var poc int32

	for i := range int(numNeg) {
		d := c.ue()
		if d >= 32768 {
			return rps, ErrInvalid
		}

		poc -= int32(d) + 1
		rps.deltaPocS0[i] = poc
		rps.usedS0[i] = c.bit() != 0
	}

	rps.deltaPocS1 = make([]int32, numPos)
	rps.usedS1 = make([]bool, numPos)

	poc = 0

	for i := range int(numPos) {
		d := c.ue()
		if d >= 32768 {
			return rps, ErrInvalid
		}

		poc += int32(d) + 1
		rps.deltaPocS1[i] = poc
		rps.usedS1[i] = c.bit() != 0
	}

	return rps, nil
}

func ceilDiv(a, b uint32) uint32 {
	return (a + b - 1) / b
}
