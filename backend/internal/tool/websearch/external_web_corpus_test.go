package websearch

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Captured publisher pages are an optional external corpus, not unit fixtures.
// An explicit corpus directory must be complete; malformed or unreadable files
// are failures even when the default corpus location is used.
func readWebResearchTestCorpus(t *testing.T, name string) []byte {
	t.Helper()
	directory, explicit := os.LookupEnv("WEB_RESEARCH_TEST_CORPUS_DIR")
	if !explicit {
		directory = filepath.Join("..", "..", "..", "docs")
	}
	data, missingDefault, err := readWebResearchTestCorpusAt(directory, name, explicit)
	if missingDefault {
		t.Skipf("optional captured corpus %q is absent; set WEB_RESEARCH_TEST_CORPUS_DIR to run this test", name)
	}
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func readWebResearchTestCorpusAt(directory, name string, explicit bool) ([]byte, bool, error) {
	if explicit && strings.TrimSpace(directory) == "" {
		return nil, false, fmt.Errorf("WEB_RESEARCH_TEST_CORPUS_DIR is explicitly empty")
	}
	path := filepath.Join(directory, name)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) && !explicit {
		return nil, true, nil
	}
	if err != nil {
		return nil, false, err
	}
	if !json.Valid(data) {
		return nil, false, fmt.Errorf("captured corpus %q is not valid JSON", path)
	}
	return data, false, nil
}

func TestWebResearchOptionalCorpusReadPolicy(t *testing.T) {
	directory := t.TempDir()
	for _, fixture := range []struct{ name, body string }{
		{"valid.json", `{"pages":[]}`},
		{"malformed.json", `{"pages":`},
	} {
		if err := os.WriteFile(filepath.Join(directory, fixture.name), []byte(fixture.body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name, file                       string
		explicit, wantMissing, wantError bool
	}{
		{"missing_default", "absent.json", false, true, false},
		{"missing_explicit", "absent.json", true, false, true},
		{"malformed_default", "malformed.json", false, false, true},
		{"malformed_explicit", "malformed.json", true, false, true},
		{"read_error_default", ".", false, false, true},
		{"read_error_explicit", ".", true, false, true},
		{"valid_default", "valid.json", false, false, false},
		{"valid_explicit", "valid.json", true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, missing, err := readWebResearchTestCorpusAt(directory, tc.file, tc.explicit)
			if missing != tc.wantMissing || (err != nil) != tc.wantError {
				t.Fatalf("missing=%v, error=%v; want missing=%v, error=%v", missing, err, tc.wantMissing, tc.wantError)
			}
			if !tc.wantMissing && !tc.wantError && string(data) != `{"pages":[]}` {
				t.Fatalf("valid corpus bytes changed: %q", data)
			}
		})
	}
	if _, missing, err := readWebResearchTestCorpusAt("", "absent.json", true); missing || err == nil {
		t.Fatal("explicit empty corpus directory must fail, not use the default")
	}
}
