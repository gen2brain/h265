package hevc

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// The JCT-VC corpus is large and lives outside the repository. Point these at
// a fluster checkout and its downloaded resources.
const (
	conformanceManifest = "/temp/h265/fluster/test_suites/h.265/JCT-VC-HEVC_V1.json"
	conformanceDir      = "/temp/h265/conformance/JCT-VC-HEVC_V1"
)

type conformanceVector struct {
	Name   string `json:"name"`
	Input  string `json:"input_file"`
	Result string `json:"result"`
}

func conformanceVectors(t *testing.T) []conformanceVector {
	t.Helper()

	b, err := os.ReadFile(conformanceManifest)
	if err != nil {
		t.Skipf("no conformance manifest at %s", conformanceManifest)
	}

	var suite struct {
		Vectors []conformanceVector `json:"test_vectors"`
	}

	if err := json.Unmarshal(b, &suite); err != nil {
		t.Fatal(err)
	}

	return suite.Vectors
}

func conformanceStream(v conformanceVector) string {
	p := filepath.Join(conformanceDir, v.Name, v.Input)
	if _, err := os.Stat(p); err == nil {
		return p
	}

	all, _ := filepath.Glob(filepath.Join(conformanceDir, v.Name, "*"))

	for _, m := range all {
		switch filepath.Ext(m) {
		case ".bit", ".bin", ".hevc":
			return m
		}
	}

	return ""
}

// decodeMD5 returns the digest of the decoded output in the same layout the
// manifest records, which is what FFmpeg writes with -f rawvideo.
func decodeMD5(data []byte) (digest string, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = ErrInvalid
		}
	}()

	var d Decoder

	h := md5.New()

	for _, nal := range SplitAnnexB(data) {
		out, err := d.DecodeNAL(nal)
		if err != nil {
			return "", err
		}

		for _, p := range out {
			h.Write(planarYUV(p))
		}
	}

	for _, p := range d.Flush() {
		h.Write(planarYUV(p))
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

// TestConformance reports how the decoder stands against the JCT-VC HEVC v1
// vectors. It is a scoreboard rather than a gate: it logs the tally and fails
// only if a stream that was passing stops.
func TestConformance(t *testing.T) {
	if testing.Short() {
		t.Skip("conformance corpus is large")
	}

	var exact, wrong, broken, absent int

	var passing []string

	errs := map[string]int{}

	for _, v := range conformanceVectors(t) {
		stream := conformanceStream(v)
		if stream == "" {
			absent++

			continue
		}

		data, err := os.ReadFile(stream)
		if err != nil {
			absent++

			continue
		}

		got, err := decodeMD5(data)

		switch {
		case err != nil:
			broken++

			errs[err.Error()]++
		case got == v.Result:
			exact++

			passing = append(passing, v.Name)
		default:
			wrong++
		}
	}

	sort.Strings(passing)

	t.Logf("JCT-VC-HEVC_V1: exact=%d wrong=%d error=%d absent=%d", exact, wrong, broken, absent)

	for k, n := range errs {
		t.Logf("  %3d x %s", n, k)
	}

	if len(passing) > 0 {
		t.Logf("  exact: %v", passing)
	}

	if exact < conformanceBaseline {
		t.Errorf("conformance regressed: %d exact, baseline is %d", exact, conformanceBaseline)
	}
}

// conformanceBaseline is what the decoder currently reaches. Raise it whenever
// the tally improves, so a regression fails rather than passing quietly.
const conformanceBaseline = 97
