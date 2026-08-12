package hevc

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// The JCT-VC corpora are large and live outside the repository. Point these at
// a fluster checkout and its downloaded resources.
const conformanceRoot = "/temp/h265"

// conformanceSuite is one downloaded corpus and the tally it currently reaches.
// Raise baseline whenever the tally improves, so a regression fails rather than
// passing quietly.
type conformanceSuite struct {
	name     string
	baseline int
}

var conformanceSuites = []conformanceSuite{
	{"JCT-VC-HEVC_V1", 97},
	{"JCT-VC-RExt", 21},
}

func (s conformanceSuite) manifest() string {
	return filepath.Join(conformanceRoot, "fluster/test_suites/h.265", s.name+".json")
}

func (s conformanceSuite) dir() string {
	return filepath.Join(conformanceRoot, "conformance", s.name)
}

type conformanceVector struct {
	Name   string `json:"name"`
	Input  string `json:"input_file"`
	Result string `json:"result"`
}

func conformanceVectors(t *testing.T, s conformanceSuite) []conformanceVector {
	t.Helper()

	b, err := os.ReadFile(s.manifest())
	if err != nil {
		t.Skipf("no conformance manifest at %s", s.manifest())
	}

	var suite struct {
		Vectors []conformanceVector `json:"test_vectors"`
	}

	if err := json.Unmarshal(b, &suite); err != nil {
		t.Fatal(err)
	}

	return suite.Vectors
}

func conformanceStream(dir string, v conformanceVector) string {
	p := filepath.Join(dir, v.Name, v.Input)
	if _, err := os.Stat(p); err == nil {
		return p
	}

	all, _ := filepath.Glob(filepath.Join(dir, v.Name, "*"))

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
	// A panic is a decoder bug rather than a bad stream, so it is caught to
	// keep the sweep going but reported as itself.
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
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

	for _, suite := range conformanceSuites {
		t.Run(suite.name, func(t *testing.T) {
			runConformance(t, suite)
		})
	}
}

func runConformance(t *testing.T, suite conformanceSuite) {
	t.Helper()

	var exact, wrong, broken, absent int

	var passing, failing []string

	errs := map[string]int{}

	for _, v := range conformanceVectors(t, suite) {
		stream := conformanceStream(suite.dir(), v)
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

			failing = append(failing, v.Name+" (error)")
		case got == v.Result:
			exact++

			passing = append(passing, v.Name)
		default:
			wrong++

			failing = append(failing, v.Name)
		}
	}

	sort.Strings(passing)
	sort.Strings(failing)

	t.Logf("%s: exact=%d wrong=%d error=%d absent=%d", suite.name, exact, wrong, broken, absent)

	for k, n := range errs {
		t.Logf("  %3d x %s", n, k)
	}

	if len(passing) > 0 {
		t.Logf("  exact: %v", passing)
	}

	for _, n := range failing {
		t.Logf("  not yet: %s", n)
	}

	if exact < suite.baseline {
		t.Errorf("%s regressed: %d exact, baseline is %d", suite.name, exact, suite.baseline)
	}
}
