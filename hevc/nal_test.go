package hevc

import (
	"bytes"
	"slices"
	"testing"
)

func nalHeader(t NALType, layerID, temporalID uint8) []byte {
	return []byte{
		byte(t)&0x3f<<1 | layerID>>5&0x01,
		layerID&0x1f<<3 | (temporalID+1)&0x07,
	}
}

func TestNALHeader(t *testing.T) {
	tests := []struct {
		typ  NALType
		want []byte
	}{
		{NALVPS, []byte{0x40, 0x01}},
		{NALSPS, []byte{0x42, 0x01}},
		{NALIdrWRadl, []byte{0x26, 0x01}},
	}

	for _, tt := range tests {
		if got := nalHeader(tt.typ, 0, 0); !bytes.Equal(got, tt.want) {
			t.Fatalf("header %d = %#x, want %#x", tt.typ, got, tt.want)
		}

		nal, ok := ParseNAL(append(tt.want, 0xaa))
		if !ok {
			t.Fatalf("header %d did not parse", tt.typ)
		}

		if nal.Type != tt.typ || nal.LayerID != 0 || nal.TemporalID != 0 {
			t.Fatalf("got %+v, want type %d", nal, tt.typ)
		}
	}
}

func TestParseNALLayerTemporal(t *testing.T) {
	nal, ok := ParseNAL(append(nalHeader(NALTrailR, 37, 5), 0x00))
	if !ok {
		t.Fatal("did not parse")
	}

	if nal.Type != NALTrailR || nal.LayerID != 37 || nal.TemporalID != 5 {
		t.Fatalf("got %+v", nal)
	}
}

func TestParseNALMalformed(t *testing.T) {
	if _, ok := ParseNAL([]byte{0x40}); ok {
		t.Fatal("short header parsed")
	}

	if _, ok := ParseNAL([]byte{0xc0, 0x01}); ok {
		t.Fatal("forbidden zero bit parsed")
	}

	if _, ok := ParseNAL([]byte{0x40, 0x00}); ok {
		t.Fatal("zero temporal id plus one parsed")
	}
}

func TestSplitAnnexB(t *testing.T) {
	var data []byte

	data = append(data, 0x00, 0x00, 0x00, 0x01)
	data = append(data, nalHeader(NALVPS, 0, 0)...)
	data = append(data, 0xaa, 0xbb, 0xcc)
	data = append(data, 0x00, 0x00, 0x01)
	data = append(data, nalHeader(NALSPS, 0, 0)...)
	data = append(data, 0xdd, 0xee)

	nals := SplitAnnexB(data)
	if len(nals) != 2 {
		t.Fatalf("got %d NAL units", len(nals))
	}

	if nals[0].Type != NALVPS || !bytes.Equal(nals[0].RBSP, []byte{0xaa, 0xbb, 0xcc}) {
		t.Fatalf("got %+v", nals[0])
	}

	if nals[1].Type != NALSPS || !bytes.Equal(nals[1].RBSP, []byte{0xdd, 0xee}) {
		t.Fatalf("got %+v", nals[1])
	}
}

func TestSplitAnnexBTrailingZeros(t *testing.T) {
	var data []byte

	data = append(data, 0x00, 0x00, 0x01)
	data = append(data, nalHeader(NALPPS, 0, 0)...)
	data = append(data, 0x11, 0x00, 0x00)
	data = append(data, 0x00, 0x00, 0x01)
	data = append(data, nalHeader(NALIdrNLP, 0, 0)...)
	data = append(data, 0x22)

	nals := SplitAnnexB(data)
	if len(nals) != 2 {
		t.Fatalf("got %d NAL units", len(nals))
	}

	if !bytes.Equal(nals[0].RBSP, []byte{0x11}) {
		t.Fatalf("trailing zeros kept: %#x", nals[0].RBSP)
	}
}

func TestSplitHVCC(t *testing.T) {
	data := []byte{
		0x00, 0x00, 0x00, 0x06,
		0x40, 0x01,
		0x00, 0x00, 0x00, 0x00,
	}

	nals := SplitHVCC(data, 4)
	if len(nals) != 1 || nals[0].Type != NALVPS {
		t.Fatalf("got %+v", nals)
	}

	if got := SplitHVCC(data, 0); got != nil {
		t.Fatal("zero length size accepted")
	}

	if got := SplitHVCC(data, 5); got != nil {
		t.Fatal("oversized length size accepted")
	}

	if got := SplitHVCC(data[:8], 4); got != nil {
		t.Fatal("truncated payload accepted")
	}
}

func TestUnescape(t *testing.T) {
	tests := []struct {
		in   []byte
		want []byte
		epb  []uint32
	}{
		{[]byte{0x01, 0x02, 0x03}, []byte{0x01, 0x02, 0x03}, nil},
		{[]byte{0x00, 0x00, 0x03, 0x01}, []byte{0x00, 0x00, 0x01}, []uint32{2}},
		{[]byte{0x00, 0x00, 0x03}, []byte{0x00, 0x00}, []uint32{2}},
		{
			[]byte{0xaa, 0x00, 0x00, 0x03, 0x00, 0x00, 0x00, 0x03, 0x01},
			[]byte{0xaa, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01},
			[]uint32{3, 7},
		},
	}

	for _, tt := range tests {
		got, epb := unescape(tt.in)

		if !bytes.Equal(got, tt.want) {
			t.Fatalf("unescape(%#x) = %#x, want %#x", tt.in, got, tt.want)
		}

		if !slices.Equal(epb, tt.epb) {
			t.Fatalf("unescape(%#x) epb = %v, want %v", tt.in, epb, tt.epb)
		}
	}
}

func TestNALTypeClass(t *testing.T) {
	if !NALTrailR.IsVCL() || !NALCra.IsVCL() || NALVPS.IsVCL() {
		t.Fatal("IsVCL")
	}

	if !NALIdrNLP.IsIDR() || NALCra.IsIDR() {
		t.Fatal("IsIDR")
	}

	if !NALCra.IsIRAP() || !NALBlaWLP.IsIRAP() || NALTrailR.IsIRAP() {
		t.Fatal("IsIRAP")
	}
}
