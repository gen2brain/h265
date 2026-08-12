package hevc

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// One syntax structure as trace_headers printed it.
type traceUnit struct {
	kind string
	elem map[string]int64
}

func (u traceUnit) has(name string) bool {
	_, ok := u.elem[name]

	return ok
}

var traceLine = regexp.MustCompile(`^\s*(\d+)\s+(\S+)\s+\S+ = (-?\d+)$`)

// Streams whose slice header FFmpeg also refuses.
var rejectedSlices = map[string]bool{
	"fuzz_dequant_qp_overflow.h265": true,
}

var traceKinds = map[string]string{
	"Video Parameter Set":    "vps",
	"Sequence Parameter Set": "sps",
	"Picture Parameter Set":  "pps",
	"Slice Segment Header":   "slice",
}

func runTraceHeaders(t *testing.T, file string) []traceUnit {
	t.Helper()

	cmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "trace",
		"-i", file, "-c", "copy", "-bsf:v", "trace_headers", "-f", "null", "-")

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s: ffmpeg: %v", file, err)
	}

	var (
		units []traceUnit
		cur   *traceUnit
	)

	sc := bufio.NewScanner(strings.NewReader(string(out)))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for sc.Scan() {
		line := sc.Text()

		_, rest, ok := strings.Cut(line, "] ")
		if !ok || !strings.HasPrefix(line, "[trace_headers") {
			continue
		}

		if kind, ok := traceKinds[strings.TrimSpace(rest)]; ok {
			units = append(units, traceUnit{kind: kind, elem: make(map[string]int64)})
			cur = &units[len(units)-1]

			continue
		}

		if cur == nil {
			continue
		}

		m := traceLine.FindStringSubmatch(rest)
		if m == nil {
			continue
		}

		v, err := strconv.ParseInt(m[3], 10, 64)
		if err != nil {
			continue
		}

		cur.elem[m[2]] = v
	}

	return units
}

func first(units []traceUnit, kind string) (traceUnit, bool) {
	for _, u := range units {
		if u.kind == kind {
			return u, true
		}
	}

	return traceUnit{}, false
}

type check struct {
	name string
	got  int64
}

func b2i(v bool) int64 {
	if v {
		return 1
	}

	return 0
}

func compare(t *testing.T, file, kind string, u traceUnit, checks []check) {
	t.Helper()

	for _, c := range checks {
		want, ok := u.elem[c.name]
		if !ok {
			continue
		}

		if c.got != want {
			t.Errorf("%s: %s: %s = %d, want %d", file, kind, c.name, c.got, want)
		}
	}
}

func vpsChecks(v *vps, u traceUnit) []check {
	checks := []check{
		{"vps_video_parameter_set_id", int64(v.id)},
		{"vps_max_layers_minus1", int64(v.maxLayersMinus1)},
		{"vps_max_sub_layers_minus1", int64(v.maxSubLayersMinus1)},
		{"vps_temporal_id_nesting_flag", b2i(v.temporalIDNesting)},
		{"general_profile_space", int64(v.ptl.profileSpace)},
		{"general_tier_flag", b2i(v.ptl.tierFlag)},
		{"general_profile_idc", int64(v.ptl.profileIDC)},
		{"general_level_idc", int64(v.ptl.levelIDC)},
	}

	for i := range maxSubLayers {
		idx := "[" + strconv.Itoa(i) + "]"
		if !u.has("vps_max_dec_pic_buffering_minus1" + idx) {
			continue
		}

		checks = append(checks,
			check{"vps_max_dec_pic_buffering_minus1" + idx, int64(v.maxDecPicBuffering[i])},
			check{"vps_max_num_reorder_pics" + idx, int64(v.maxNumReorderPics[i])},
			check{"vps_max_latency_increase_plus1" + idx, int64(v.maxLatencyIncrease[i])},
		)
	}

	return checks
}

func spsChecks(s *sps, u traceUnit) []check {
	checks := []check{
		{"sps_video_parameter_set_id", int64(s.vpsID)},
		{"sps_max_sub_layers_minus1", int64(s.maxSubLayersMinus1)},
		{"sps_temporal_id_nesting_flag", b2i(s.temporalIDNesting)},
		{"general_profile_space", int64(s.ptl.profileSpace)},
		{"general_tier_flag", b2i(s.ptl.tierFlag)},
		{"general_profile_idc", int64(s.ptl.profileIDC)},
		{"general_level_idc", int64(s.ptl.levelIDC)},
		{"sps_seq_parameter_set_id", int64(s.id)},
		{"chroma_format_idc", int64(s.chromaFormatIDC)},
		{"separate_colour_plane_flag", b2i(s.separateColourPlane)},
		{"pic_width_in_luma_samples", int64(s.picWidthInLumaSamples)},
		{"pic_height_in_luma_samples", int64(s.picHeightInLumaSamples)},
		{"conf_win_left_offset", int64(s.confWinLeft)},
		{"conf_win_right_offset", int64(s.confWinRight)},
		{"conf_win_top_offset", int64(s.confWinTop)},
		{"conf_win_bottom_offset", int64(s.confWinBottom)},
		{"bit_depth_luma_minus8", int64(s.bitDepthLuma) - 8},
		{"bit_depth_chroma_minus8", int64(s.bitDepthChroma) - 8},
		{"log2_max_pic_order_cnt_lsb_minus4", int64(s.log2MaxPocLsb) - 4},
		{"log2_min_luma_coding_block_size_minus3", int64(s.minCbLog2SizeY) - 3},
		{"log2_diff_max_min_luma_coding_block_size", int64(s.ctbLog2SizeY - s.minCbLog2SizeY)},
		{"log2_min_luma_transform_block_size_minus2", int64(s.minTbLog2SizeY) - 2},
		{"log2_diff_max_min_luma_transform_block_size", int64(s.maxTbLog2SizeY - s.minTbLog2SizeY)},
		{"max_transform_hierarchy_depth_inter", int64(s.maxTrHierInter)},
		{"max_transform_hierarchy_depth_intra", int64(s.maxTrHierIntra)},
		{"scaling_list_enabled_flag", b2i(s.scalingListEnabled)},
		{"amp_enabled_flag", b2i(s.ampEnabled)},
		{"sample_adaptive_offset_enabled_flag", b2i(s.saoEnabled)},
		{"pcm_enabled_flag", b2i(s.pcmEnabled)},
		{"num_short_term_ref_pic_sets", int64(len(s.stRPS))},
		{"long_term_ref_pics_present_flag", b2i(s.longTermRefPicsPresent)},
		{"num_long_term_ref_pics_sps", int64(len(s.ltRefPicPocLsb))},
		{"sps_temporal_mvp_enabled_flag", b2i(s.temporalMvpEnabled)},
		{"strong_intra_smoothing_enabled_flag", b2i(s.strongIntraSmoothing)},
		{"transform_skip_rotation_enabled_flag", b2i(s.transformSkipRotation)},
		{"transform_skip_context_enabled_flag", b2i(s.transformSkipContext)},
		{"implicit_rdpcm_enabled_flag", b2i(s.implicitRdpcm)},
		{"explicit_rdpcm_enabled_flag", b2i(s.explicitRdpcm)},
		{"extended_precision_processing_flag", b2i(s.extendedPrecision)},
		{"intra_smoothing_disabled_flag", b2i(s.intraSmoothingDisabled)},
		{"high_precision_offsets_enabled_flag", b2i(s.highPrecisionOffsets)},
		{"persistent_rice_adaptation_enabled_flag", b2i(s.persistentRiceAdaptation)},
		{"cabac_bypass_alignment_enabled_flag", b2i(s.cabacBypassAlignment)},
	}

	if s.pcmEnabled {
		checks = append(checks,
			check{"pcm_sample_bit_depth_luma_minus1", int64(s.pcmBitDepthLuma) - 1},
			check{"pcm_sample_bit_depth_chroma_minus1", int64(s.pcmBitDepthChroma) - 1},
			check{"log2_min_pcm_luma_coding_block_size_minus3", int64(s.log2MinPcmCbSize) - 3},
			check{"log2_diff_max_min_pcm_luma_coding_block_size", int64(s.log2MaxPcmCbSize - s.log2MinPcmCbSize)},
			check{"pcm_loop_filter_disabled_flag", b2i(s.pcmLoopFilterDisabled)},
		)
	}

	top := "[" + strconv.Itoa(int(s.maxSubLayersMinus1)) + "]"
	if u.has("sps_max_dec_pic_buffering_minus1" + top) {
		checks = append(checks,
			check{"sps_max_dec_pic_buffering_minus1" + top, int64(s.maxDecPicBuffering)},
			check{"sps_max_num_reorder_pics" + top, int64(s.maxNumReorderPics)},
			check{"sps_max_latency_increase_plus1" + top, int64(s.maxLatencyIncrease)},
		)
	}

	for i := range s.stRPS {
		idx := "[" + strconv.Itoa(i) + "]"
		if !u.has("num_negative_pics" + idx) {
			continue
		}

		checks = append(checks,
			check{"num_negative_pics" + idx, int64(len(s.stRPS[i].deltaPocS0))},
			check{"num_positive_pics" + idx, int64(len(s.stRPS[i].deltaPocS1))},
		)
	}

	return checks
}

func ppsChecks(p *pps) []check {
	checks := []check{
		{"pps_pic_parameter_set_id", int64(p.id)},
		{"pps_seq_parameter_set_id", int64(p.spsID)},
		{"dependent_slice_segments_enabled_flag", b2i(p.dependentSliceSegmentsEnabled)},
		{"output_flag_present_flag", b2i(p.outputFlagPresent)},
		{"num_extra_slice_header_bits", int64(p.numExtraSliceHeaderBits)},
		{"sign_data_hiding_enabled_flag", b2i(p.signDataHidingEnabled)},
		{"cabac_init_present_flag", b2i(p.cabacInitPresent)},
		{"num_ref_idx_l0_default_active_minus1", int64(p.numRefIdxL0DefaultActive) - 1},
		{"num_ref_idx_l1_default_active_minus1", int64(p.numRefIdxL1DefaultActive) - 1},
		{"init_qp_minus26", int64(p.initQP) - 26},
		{"constrained_intra_pred_flag", b2i(p.constrainedIntraPred)},
		{"transform_skip_enabled_flag", b2i(p.transformSkipEnabled)},
		{"cu_qp_delta_enabled_flag", b2i(p.cuQPDeltaEnabled)},
		{"diff_cu_qp_delta_depth", int64(p.diffCuQPDeltaDepth)},
		{"pps_cb_qp_offset", int64(p.cbQPOffset)},
		{"pps_cr_qp_offset", int64(p.crQPOffset)},
		{"pps_slice_chroma_qp_offsets_present_flag", b2i(p.sliceChromaQPOffsets)},
		{"weighted_pred_flag", b2i(p.weightedPred)},
		{"weighted_bipred_flag", b2i(p.weightedBipred)},
		{"transquant_bypass_enabled_flag", b2i(p.transquantBypass)},
		{"tiles_enabled_flag", b2i(p.tilesEnabled)},
		{"entropy_coding_sync_enabled_flag", b2i(p.entropyCodingSync)},
		{"num_tile_columns_minus1", int64(p.numTileColumns) - 1},
		{"num_tile_rows_minus1", int64(p.numTileRows) - 1},
		{"uniform_spacing_flag", b2i(p.uniformSpacing)},
		{"loop_filter_across_tiles_enabled_flag", b2i(p.loopFilterAcrossTiles)},
		{"pps_loop_filter_across_slices_enabled_flag", b2i(p.loopFilterAcrossSlices)},
		{"deblocking_filter_control_present_flag", b2i(p.deblockingControlPresen)},
		{"deblocking_filter_override_enabled_flag", b2i(p.deblockingOverride)},
		{"pps_deblocking_filter_disabled_flag", b2i(p.deblockingDisabled)},
		{"pps_beta_offset_div2", int64(p.betaOffsetDiv2)},
		{"pps_tc_offset_div2", int64(p.tcOffsetDiv2)},
		{"pps_scaling_list_data_present_flag", b2i(p.scalingListPresent)},
		{"lists_modification_present_flag", b2i(p.listsModificationPresent)},
		{"log2_parallel_merge_level_minus2", int64(p.log2ParallelMergeLevel) - 2},
		{"slice_segment_header_extension_present_flag", b2i(p.sliceHeaderExtensionPresen)},
		{"cross_component_prediction_enabled_flag", b2i(p.crossComponentPrediction)},
		{"chroma_qp_offset_list_enabled_flag", b2i(p.chromaQPOffsetList)},
		{"log2_sao_offset_scale_luma", int64(p.log2SaoOffsetScaleLuma)},
		{"log2_sao_offset_scale_chroma", int64(p.log2SaoOffsetScaleChroma)},
	}

	for i := range int(p.chromaQPOffsetListLen) {
		idx := "[" + strconv.Itoa(i) + "]"
		checks = append(checks,
			check{"cb_qp_offset_list" + idx, int64(p.cbQPOffsetList[i])},
			check{"cr_qp_offset_list" + idx, int64(p.crQPOffsetList[i])},
		)
	}

	for i, w := range p.columnWidthMinus1 {
		checks = append(checks, check{"column_width_minus1[" + strconv.Itoa(i) + "]", int64(w)})
	}

	for i, h := range p.rowHeightMinus1 {
		checks = append(checks, check{"row_height_minus1[" + strconv.Itoa(i) + "]", int64(h)})
	}

	return checks
}

func sliceChecks(h *sliceHeader, u traceUnit) []check {
	checks := []check{
		{"first_slice_segment_in_pic_flag", b2i(h.firstSliceSegmentInPic)},
		{"no_output_of_prior_pics_flag", b2i(h.noOutputOfPriorPics)},
		{"slice_pic_parameter_set_id", int64(h.ppsID)},
		{"dependent_slice_segment_flag", b2i(h.dependentSliceSegment)},
		{"slice_segment_address", int64(h.sliceSegmentAddress)},
		{"num_entry_point_offsets", int64(len(h.entryPointOffsets))},
	}

	if h.dependentSliceSegment {
		return checks
	}

	checks = append(checks,
		check{"slice_type", int64(h.sliceType)},
		check{"pic_output_flag", b2i(h.picOutputFlag)},
		check{"colour_plane_id", int64(h.colourPlaneID)},
		check{"slice_pic_order_cnt_lsb", int64(h.picOrderCntLsb)},
		check{"short_term_ref_pic_set_sps_flag", b2i(h.stRPSFromSPS)},
		check{"short_term_ref_pic_set_idx", int64(h.stRPSIdx)},
		check{"slice_temporal_mvp_enabled_flag", b2i(h.temporalMvp)},
		check{"slice_sao_luma_flag", b2i(h.saoLuma)},
		check{"slice_sao_chroma_flag", b2i(h.saoChroma)},
		check{"mvd_l1_zero_flag", b2i(h.mvdL1Zero)},
		check{"cabac_init_flag", b2i(h.cabacInit)},
		check{"collocated_from_l0_flag", b2i(h.collocatedFromL0)},
		check{"collocated_ref_idx", int64(h.collocatedRefIdx)},
		check{"five_minus_max_num_merge_cand", 5 - int64(h.maxNumMergeCand)},
		check{"slice_qp_delta", int64(h.qpDelta)},
		check{"slice_cb_qp_offset", int64(h.cbQPOffset)},
		check{"slice_cr_qp_offset", int64(h.crQPOffset)},
		check{"cu_chroma_qp_offset_enabled_flag", b2i(h.cuChromaQPOffset)},
		check{"slice_deblocking_filter_disabled_flag", b2i(h.deblockingDisabled)},
		check{"slice_beta_offset_div2", int64(h.betaOffsetDiv2)},
		check{"slice_tc_offset_div2", int64(h.tcOffsetDiv2)},
		check{"slice_loop_filter_across_slices_enabled_flag", b2i(h.loopFilterAcross)},
	)

	if h.sliceType != sliceI {
		checks = append(checks,
			check{"num_ref_idx_l0_active_minus1", int64(h.numRefIdxL0Active) - 1},
			check{"num_ref_idx_l1_active_minus1", int64(h.numRefIdxL1Active) - 1},
		)
	}

	if !h.stRPSFromSPS && u.has("num_negative_pics") {
		checks = append(checks,
			check{"num_negative_pics", int64(len(h.stRPS.deltaPocS0))},
			check{"num_positive_pics", int64(len(h.stRPS.deltaPocS1))},
		)
	}

	for i := range h.ltRPS.pocLsbLt {
		idx := "[" + strconv.Itoa(i) + "]"
		checks = append(checks,
			check{"used_by_curr_pic_lt_flag" + idx, b2i(h.ltRPS.usedByCurrPicLt[i])},
			check{"delta_poc_msb_present_flag" + idx, b2i(h.ltRPS.deltaPocMsbPresn[i])},
		)
	}

	return checks
}

func TestTraceHeaders(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}

	for _, f := range testdataFiles(t) {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}

		name := filepath.Base(f)
		units := runTraceHeaders(t, f)

		if len(units) == 0 {
			t.Errorf("%s: trace_headers produced nothing", name)

			continue
		}

		var (
			compared   int
			activeSPS  *sps
			activePPS  *pps
			prevIndep  *sliceHeader
			sliceIndex int
		)

		slices := make([]traceUnit, 0, 8)

		for _, u := range units {
			if u.kind == "slice" {
				slices = append(slices, u)
			}
		}

		for _, nal := range SplitAnnexB(data) {
			if nal.Type.IsVCL() {
				if activeSPS == nil || activePPS == nil || sliceIndex >= len(slices) {
					continue
				}

				h, err := parseSliceHeader(nal.RBSP, nal.Type, activeSPS, activePPS)

				if rejectedSlices[name] {
					if err == nil {
						t.Errorf("%s: slice %d: parsed, want rejection", name, sliceIndex)
					}

					compared++
					sliceIndex++

					continue
				}

				if err != nil {
					t.Errorf("%s: slice %d: %v", name, sliceIndex, err)
					sliceIndex++

					continue
				}

				if h.dependentSliceSegment && prevIndep != nil {
					h.inherit(prevIndep)
				} else if !h.dependentSliceSegment {
					indep := *h
					prevIndep = &indep
				}

				compare(t, name, "slice", slices[sliceIndex], sliceChecks(h, slices[sliceIndex]))

				compared++
				sliceIndex++

				continue
			}

			switch nal.Type {
			case NALVPS:
				u, ok := first(units, "vps")
				if !ok {
					continue
				}

				v, err := parseVPS(nal.RBSP)
				if err != nil {
					continue
				}

				compare(t, name, "VPS", u, vpsChecks(v, u))

				compared++

			case NALSPS:
				u, ok := first(units, "sps")
				if !ok {
					continue
				}

				s, err := parseSPS(nal.RBSP)
				if err != nil {
					continue
				}

				activeSPS = s

				compare(t, name, "SPS", u, spsChecks(s, u))

				compared++

			case NALPPS:
				u, ok := first(units, "pps")
				if !ok {
					continue
				}

				p, err := parsePPS(nal.RBSP)
				if err != nil {
					continue
				}

				activePPS = p

				compare(t, name, "PPS", u, ppsChecks(p))

				compared++
			}
		}

		if compared == 0 {
			t.Errorf("%s: nothing compared", name)
		}
	}
}
