package hevc

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type streamInfo struct {
	width, height uint32
	chroma        uint32
	bitDepth      uint8
	profile       uint8
	level         uint8
}

// Reference values from ffprobe.
var streams = map[string]streamInfo{
	"1080p.h265":                           {1920, 1080, 1, 8, 1, 120},
	"10bit_128x128.h265":                   {128, 128, 1, 10, 2, 30},
	"angular_grad.h265":                    {16, 16, 1, 8, 3, 30},
	"angular.h265":                         {16, 16, 1, 8, 3, 30},
	"aq_intra_64.h265":                     {64, 64, 1, 8, 3, 30},
	"aq_p_320x240.h265":                    {320, 240, 1, 8, 1, 60},
	"bframes3_128x128.h265":                {128, 128, 1, 8, 1, 30},
	"constrained_intra_128x128.h265":       {128, 128, 1, 8, 1, 30},
	"ctu64_128x128.h265":                   {128, 128, 1, 8, 1, 30},
	"ctu64_64_nosis.h265":                  {64, 64, 1, 8, 4, 30},
	"ctu64_aq.h265":                        {320, 240, 1, 8, 1, 60},
	"ctu64_noqp_nosao_320x240.h265":        {320, 240, 1, 8, 1, 60},
	"ctu64_wpp.h265":                       {320, 240, 1, 8, 1, 60},
	"deblock_grad.h265":                    {32, 32, 1, 8, 3, 30},
	"deblock_sao_320x240.h265":             {320, 240, 1, 8, 1, 60},
	"deblock_sao_nodeblock.h265":           {320, 240, 1, 8, 1, 60},
	"dep_slices.h265":                      {128, 128, 1, 8, 1, 186},
	"flat64.h265":                          {64, 64, 1, 8, 3, 30},
	"fuzz_bit_depth_change.h265":           {320, 240, 1, 8, 1, 60},
	"fuzz_mvd_overflow.h265":               {384, 216, 1, 8, 1, 60},
	"grad64.h265":                          {64, 64, 1, 8, 3, 30},
	"inter_b.h265":                         {16, 16, 1, 8, 1, 30},
	"inter_p.h265":                         {16, 16, 1, 8, 1, 30},
	"motion_320x240.h265":                  {320, 240, 1, 8, 1, 60},
	"multi_ctu.h265":                       {32, 32, 1, 8, 3, 30},
	"multi_slice.h265":                     {64, 64, 1, 8, 3, 30},
	"multi_slice_sao_deblock_256x256.h265": {256, 256, 1, 8, 1, 60},
	"no_filter_across_slices_256x256.h265": {256, 256, 1, 8, 1, 186},
	"pcm.h265":                             {16, 16, 1, 8, 3, 30},
	"pcm_hm_64x64.h265":                    {64, 64, 1, 8, 0, 0},
	"qp_small.h265":                        {64, 64, 1, 8, 3, 30},
	"ramp64.h265":                          {64, 64, 1, 8, 3, 30},
	"realworld_320x240.h265":               {320, 240, 1, 8, 1, 60},
	"realworld_720p.h265":                  {1280, 720, 1, 8, 1, 93},
	"sao.h265":                             {16, 16, 1, 8, 3, 30},
	"scaling_list.h265":                    {16, 16, 1, 8, 3, 30},
	"signhide.h265":                        {16, 16, 1, 8, 3, 30},
	"signhide_scaling_320x240.h265":        {320, 240, 1, 8, 1, 60},
	"tiles.h265":                           {256, 256, 1, 8, 1, 186},
	"tiny_intra.h265":                      {16, 16, 1, 8, 3, 30},
	"transquant_bypass_64x64.h265":         {64, 64, 1, 8, 1, 255},
	"tskip_128x128.h265":                   {128, 128, 1, 8, 1, 30},
	"tu32.h265":                            {32, 32, 1, 8, 3, 30},
	"tu32_ionly.h265":                      {32, 32, 1, 8, 4, 30},
	"tu32_nowpp.h265":                      {32, 32, 1, 8, 1, 30},
	"tu32_test.h265":                       {32, 32, 1, 8, 1, 30},
	"tu8.h265":                             {16, 16, 1, 8, 3, 30},
	"tu_inter4x4_motion.h265":              {128, 128, 1, 8, 1, 30},
	"wpp_ctu16.h265":                       {384, 216, 1, 8, 1, 60},
}

func testdataFiles(t *testing.T) []string {
	t.Helper()

	files, err := filepath.Glob(filepath.Join("testdata", "*.h265"))
	if err != nil {
		t.Fatal(err)
	}

	if len(files) == 0 {
		t.Skip("no testdata streams")
	}

	return files
}

func TestParameterSets(t *testing.T) {
	for _, f := range testdataFiles(t) {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}

		name := filepath.Base(f)

		var (
			nVPS, nSPS, nPPS int
			active           *sps
		)

		for _, nal := range SplitAnnexB(data) {
			switch nal.Type {
			case NALVPS:
				if _, err := parseVPS(nal.RBSP); err != nil {
					t.Errorf("%s: VPS: %v", name, err)

					continue
				}

				nVPS++

			case NALSPS:
				s, err := parseSPS(nal.RBSP)
				if errors.Is(err, ErrUnsupported) {
					t.Skipf("%s: %v", name, err)
				}

				if err != nil {
					t.Errorf("%s: SPS: %v", name, err)

					continue
				}

				nSPS++
				active = s

			case NALPPS:
				p, err := parsePPS(nal.RBSP)
				if err != nil {
					t.Errorf("%s: PPS: %v", name, err)

					continue
				}

				nPPS++

				if active == nil {
					continue
				}

				if err := p.resolveTileGeometry(active); err != nil {
					t.Errorf("%s: tile geometry: %v", name, err)

					continue
				}

				var sum uint32
				for _, w := range p.colWidthsInCtbs {
					sum += w
				}

				if sum != active.picWidthInCtbs {
					t.Errorf("%s: tile columns sum to %d, want %d", name, sum, active.picWidthInCtbs)
				}

				sum = 0
				for _, h := range p.rowHeightsInCtbs {
					sum += h
				}

				if sum != active.picHeightInCtbs {
					t.Errorf("%s: tile rows sum to %d, want %d", name, sum, active.picHeightInCtbs)
				}
			}
		}

		if nVPS == 0 || nSPS == 0 || nPPS == 0 {
			t.Errorf("%s: got %d VPS, %d SPS, %d PPS", name, nVPS, nSPS, nPPS)
		}

		want, ok := streams[name]
		if !ok || active == nil {
			continue
		}

		got := streamInfo{
			width:    active.croppedWidth(),
			height:   active.croppedHeight(),
			chroma:   active.chromaFormatIDC,
			bitDepth: active.bitDepthLuma,
			profile:  active.ptl.profileIDC,
			level:    active.ptl.levelIDC,
		}

		if got != want {
			t.Errorf("%s: got %+v, want %+v", name, got, want)
		}
	}
}

func TestDefaultScalingList(t *testing.T) {
	sl := defaultScalingList()

	for m := range sl.sl[0] {
		for i := range 16 {
			if sl.sl[0][m][i] != 16 {
				t.Fatalf("4x4 [%d][%d] = %d", m, i, sl.sl[0][m][i])
			}
		}
	}

	for s := 1; s < maxScalingListSizes; s++ {
		for m := range sl.sl[s] {
			want := defaultScalingListIntra
			if m >= 3 {
				want = defaultScalingListInter
			}

			if sl.sl[s][m] != want {
				t.Fatalf("size %d matrix %d mismatch", s, m)
			}
		}
	}

	for s := range sl.dc {
		for m := range sl.dc[s] {
			if sl.dc[s][m] != 16 {
				t.Fatalf("dc [%d][%d] = %d", s, m, sl.dc[s][m])
			}
		}
	}
}

// bitPut writes the Exp-Golomb forms of 9.2 so a syntax structure can be
// assembled without a stream to carry it.
type bitPut struct {
	b   []byte
	n   int
	acc byte
}

func (p *bitPut) bit(v uint32) {
	p.acc = p.acc<<1 | byte(v&1)
	p.n++

	if p.n == 8 {
		p.b = append(p.b, p.acc)
		p.acc, p.n = 0, 0
	}
}

func (p *bitPut) ue(v uint32) {
	n := v + 1

	k := 0
	for n>>(k+1) != 0 {
		k++
	}

	for range k {
		p.bit(0)
	}

	for i := k; i >= 0; i-- {
		p.bit(n >> uint(i))
	}
}

func (p *bitPut) se(v int32) {
	if v > 0 {
		p.ue(uint32(2*v - 1))

		return
	}

	p.ue(uint32(-2 * v))
}

func (p *bitPut) bytes() []byte {
	for p.n != 0 {
		p.bit(0)
	}

	return append(p.b, 0, 0, 0, 0)
}

// TestPredWeightTableOffsetRange covers 7.4.7.3's WpOffsetHalfRangeC, which is
// the sample depth under high_precision_offsets_enabled_flag and 128 otherwise.
func TestPredWeightTableOffsetRange(t *testing.T) {
	for _, tt := range []struct {
		name          string
		highPrecision bool
		bitDepth      uint8
		want          [2]int16
	}{
		{"eight bit", false, 8, [2]int16{127, -128}},
		{"ten bit high precision", true, 10, [2]int16{300, -300}},
		{"ten bit", false, 10, [2]int16{127, -128}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var p bitPut

			p.ue(0)
			p.se(0)
			p.bit(0)
			p.bit(1)
			p.se(0)
			p.se(300)
			p.se(0)
			p.se(-300)

			var g getBits

			g.init(p.bytes())

			s := &sps{
				chromaFormatIDC:      1,
				bitDepthChroma:       tt.bitDepth,
				highPrecisionOffsets: tt.highPrecision,
			}

			h := &sliceHeader{sliceType: sliceP, numRefIdxL0Active: 1}

			if err := parsePredWeightTable(&g, h, s); err != nil {
				t.Fatal(err)
			}

			if got := h.weights.chromaOffset[0][0]; got != tt.want {
				t.Fatalf("chromaOffset = %v, want %v", got, tt.want)
			}
		})
	}
}

// colorDesc is what the video usability information says about the samples.
type colorDesc struct {
	primaries, transfer, matrix uint16
	fullRange                   bool
}

// TestParseVUIColorDescription covers E.2.1's video_signal_type, which is where
// a sequence declares the range and matrix its samples are in. A container with
// no description of its own falls back to it.
func TestParseVUIColorDescription(t *testing.T) {
	for _, tt := range []struct {
		name              string
		signal, desc      bool
		full              bool
		prim, trc, matrix uint32
		want              colorDesc
	}{
		{
			name: "absent", want: colorDesc{2, 2, 2, false},
		},
		{
			name: "range only", signal: true, full: true,
			want: colorDesc{2, 2, 2, true},
		},
		{
			name: "full description", signal: true, desc: true, full: true,
			prim: 1, trc: 13, matrix: 6,
			want: colorDesc{1, 13, 6, true},
		},
		{
			name: "limited range", signal: true, desc: true,
			prim: 9, trc: 16, matrix: 9,
			want: colorDesc{9, 16, 9, false},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var p bitPut

			p.bit(0) // aspect_ratio_info_present_flag
			p.bit(0) // overscan_info_present_flag

			if !tt.signal {
				p.bit(0)
			} else {
				p.bit(1)
				p.bit(0)
				p.bit(0)
				p.bit(0) // video_format

				p.bit(b2u(tt.full))

				if !tt.desc {
					p.bit(0)
				} else {
					p.bit(1)

					for _, v := range []uint32{tt.prim, tt.trc, tt.matrix} {
						for i := 7; i >= 0; i-- {
							p.bit(v >> uint(i))
						}
					}
				}
			}

			// chroma_loc, neutral_chroma, field_seq, frame_field, display
			// window, timing and bitstream restriction, all absent.
			for range 7 {
				p.bit(0)
			}

			var c getBits

			c.init(p.bytes())

			s := &sps{colourPrimaries: 2, transferChar: 2, matrixCoeffs: 2}

			if err := parseVUI(&c, s, 0); err != nil {
				t.Fatal(err)
			}

			got := colorDesc{s.colourPrimaries, s.transferChar, s.matrixCoeffs, s.fullRange}

			if got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func b2u(v bool) uint32 {
	if v {
		return 1
	}

	return 0
}
