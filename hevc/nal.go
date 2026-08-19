package hevc

import "bytes"

// NALType is the nal_unit_type field of a NAL unit header.
type NALType uint8

const (
	NALTrailN NALType = iota
	NALTrailR
	NALTsaN
	NALTsaR
	NALStsaN
	NALStsaR
	NALRadlN
	NALRadlR
	NALRaslN
	NALRaslR
)

const (
	NALBlaWLP NALType = iota + 16
	NALBlaWRadl
	NALBlaNLP
	NALIdrWRadl
	NALIdrNLP
	NALCra
)

const (
	NALVPS NALType = iota + 32
	NALSPS
	NALPPS
	NALAUD
	NALEOS
	NALEOB
	NALFD
	NALPrefixSEI
	NALSuffixSEI
)

// IsVCL reports whether the unit carries a coded slice segment.
func (t NALType) IsVCL() bool {
	return t < 32
}

// IsIDR reports whether the unit starts an instantaneous decoding refresh picture.
func (t NALType) IsIDR() bool {
	return t == NALIdrWRadl || t == NALIdrNLP
}

// IsIRAP reports whether the unit starts an intra random access point picture.
func (t NALType) IsIRAP() bool {
	return t >= NALBlaWLP && t <= NALCra
}

// NALUnit is one parsed NAL unit.
type NALUnit struct {
	Type       NALType
	LayerID    uint8
	TemporalID uint8
	RBSP       []byte

	EPB []uint32
}

// ParseNAL parses a single NAL unit with a two-byte header and no framing.
func ParseNAL(data []byte) (NALUnit, bool) {
	if len(data) < 2 {
		return NALUnit{}, false
	}

	b0, b1 := data[0], data[1]
	if b0&0x80 != 0 {
		return NALUnit{}, false
	}

	tidPlus1 := b1 & 0x07
	if tidPlus1 == 0 {
		return NALUnit{}, false
	}

	rbsp, epb := unescape(data[2:])

	return NALUnit{
		Type:       NALType(b0 >> 1 & 0x3f),
		LayerID:    b0&0x01<<5 | b1>>3,
		TemporalID: tidPlus1 - 1,
		RBSP:       rbsp,
		EPB:        epb,
	}, true
}

// SplitAnnexB splits a start-code delimited byte stream into NAL units.
func SplitAnnexB(data []byte) []NALUnit {
	var nals []NALUnit

	i, _, ok := startCode(data, 0)
	if !ok {
		return nals
	}

	for i < len(data) {
		next, scStart, found := startCode(data, i)

		end := len(data)
		if found {
			end = scStart
			for end > i && data[end-1] == 0 {
				end--
			}
		}

		if nal, ok := ParseNAL(data[i:end]); ok {
			nals = append(nals, nal)
		}

		if !found {
			break
		}

		i = next
	}

	return nals
}

// SplitHVCC splits length-prefixed NAL units, as framed inside hvcC.
func SplitHVCC(data []byte, lengthSize int) []NALUnit {
	var nals []NALUnit

	if lengthSize < 1 || lengthSize > 4 {
		return nals
	}

	for i := 0; i+lengthSize <= len(data); {
		n := 0
		for j := range lengthSize {
			n = n<<8 | int(data[i+j])
		}

		i += lengthSize
		if n == 0 || i+n > len(data) {
			break
		}

		if nal, ok := ParseNAL(data[i : i+n]); ok {
			nals = append(nals, nal)
		}

		i += n
	}

	return nals
}

func startCode(data []byte, off int) (next, start int, ok bool) {
	for i := off; i+2 < len(data); i++ {
		if data[i] != 0 || data[i+1] != 0 {
			continue
		}

		if data[i+2] == 1 {
			return i + 3, i, true
		}

		if i+3 < len(data) && data[i+2] == 0 && data[i+3] == 1 {
			return i + 4, i, true
		}
	}

	return 0, 0, false
}

func unescape(data []byte) ([]byte, []uint32) {
	if !bytes.Contains(data, []byte{0, 0, 3}) {
		return data, nil
	}

	rbsp := make([]byte, 0, len(data))

	var epb []uint32

	for i := 0; i < len(data); {
		if i+2 < len(data) && data[i] == 0 && data[i+1] == 0 && data[i+2] == 3 {
			rbsp = append(rbsp, 0, 0)
			epb = append(epb, uint32(i+2))
			i += 3

			continue
		}

		rbsp = append(rbsp, data[i])
		i++
	}

	return rbsp, epb
}

// RBSPOffset converts a payload-relative byte position, as entry point offsets
// count them, into an index into RBSP.
func (n NALUnit) RBSPOffset(off int) int {
	rbsp := off

	for _, p := range n.EPB {
		if int(p) >= off {
			break
		}

		rbsp--
	}

	return rbsp
}

// NALOffset is the inverse of RBSPOffset.
func (n NALUnit) NALOffset(off int) int {
	for _, p := range n.EPB {
		if int(p) <= off {
			off++

			continue
		}

		break
	}

	return off
}

func marshalNAL(typ NALType, layerID, temporalID uint8, rbsp []byte) []byte {
	out := make([]byte, 0, len(rbsp)+2)
	out = append(out,
		byte(typ)<<1|layerID>>5,
		layerID<<3|(temporalID+1)&7,
	)

	return append(out, escapeRBSP(rbsp)...)
}

func MarshalNAL(nal NALUnit) []byte {
	return marshalNAL(nal.Type, nal.LayerID, nal.TemporalID, nal.RBSP)
}

func marshalAnnexB(nals [][]byte) []byte {
	var out []byte

	for _, nal := range nals {
		out = append(out, 0, 0, 0, 1)
		out = append(out, nal...)
	}

	return out
}

func MarshalAnnexB(nals []NALUnit) []byte {
	data := make([][]byte, len(nals))
	for i := range nals {
		data[i] = MarshalNAL(nals[i])
	}

	return marshalAnnexB(data)
}

func marshalHVCC(nals [][]byte, lengthSize int) []byte {
	if lengthSize < 1 || lengthSize > 4 {
		return nil
	}

	var out []byte

	for _, nal := range nals {
		if len(nal) == 0 || uint64(len(nal)) >= 1<<uint(lengthSize*8) {
			return nil
		}

		for i := lengthSize - 1; i >= 0; i-- {
			out = append(out, byte(len(nal)>>uint(i*8)))
		}

		out = append(out, nal...)
	}

	return out
}

func escapeRBSP(rbsp []byte) []byte {
	out := make([]byte, 0, len(rbsp))
	zeros := 0

	for _, b := range rbsp {
		if zeros == 2 && b <= 3 {
			out = append(out, 3)
			zeros = 0
		}

		out = append(out, b)

		if b == 0 {
			zeros++
		} else {
			zeros = 0
		}
	}

	return out
}

// ProfileTierLevel returns the twelve bytes of profile_tier_level, from
// general_profile_space through general_level_idc, out of a sequence parameter
// set RBSP. A container's decoder configuration record repeats them.
func ProfileTierLevel(sps []byte) ([]byte, bool) {
	if len(sps) < 13 {
		return nil, false
	}

	return sps[1:13], true
}

// SPSFormat is the chroma_format_idc and the two sample sizes a sequence
// parameter set declares. A container's decoder configuration record repeats
// them, and a reader that trusts it over the bitstream has to be told the
// truth.
func SPSFormat(sps []byte) (chromaFormat, bitDepthLuma, bitDepthChroma int, ok bool) {
	s, err := parseSPS(sps)
	if err != nil {
		return 0, 0, 0, false
	}

	return int(s.chromaFormatIDC), int(s.bitDepthLuma), int(s.bitDepthChroma), true
}
