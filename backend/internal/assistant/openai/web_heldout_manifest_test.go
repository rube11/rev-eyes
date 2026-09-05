package openai

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
)

func heldoutManifestFixtureCase() map[string]any {
	return map[string]any{"id": "independent_case", "question": "Find public opening hours.", "time_zone": "America/Los_Angeles", "as_of": "2026-09-04T18:00:00-07:00"}
}

func heldoutManifestFixtureJSON(cases ...map[string]any) string {
	encoded, err := json.Marshal(map[string]any{"cases": cases})
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

func TestHeldoutManifestIndependentInputBounds(t *testing.T) {
	t.Parallel()
	for _, scenario := range []struct {
		name, key string
		value     any
		wantValid bool
	}{
		{"id_at_limit", "id", strings.Repeat("a", 100), true},
		{"id_over_limit", "id", strings.Repeat("a", 101), false},
		{"empty_id", "id", "", false},
		{"slash_id", "id", "one/two", false},
		{"backslash_id", "id", `one\two`, false},
		{"space_id", "id", "one two", false},
		{"tab_id", "id", "one\ttwo", false},
		{"newline_id", "id", "one\ntwo", false},
		{"question_at_rune_limit", "question", strings.Repeat("界", 1600), true},
		{"question_over_rune_limit", "question", strings.Repeat("界", 1601), false},
		{"empty_question", "question", "", false},
		{"whitespace_question", "question", " \t\r\n", false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			item := heldoutManifestFixtureCase()
			item[scenario.key] = scenario.value
			cases, digest, err := loadHeldoutWebCases(strings.NewReader(heldoutManifestFixtureJSON(item)))
			if (err == nil) != scenario.wantValid {
				t.Errorf("valid=%t, want %t: %v", err == nil, scenario.wantValid, err)
			}
			if err != nil && (cases != nil || digest != "") {
				t.Errorf("invalid input returned partially usable cases/digest: cases=%v digest=%q", cases, digest)
			}
		})
	}
	valid := heldoutManifestFixtureJSON(heldoutManifestFixtureCase())
	for _, size := range []int{1 << 20, (1 << 20) + 1} {
		t.Run(fmt.Sprintf("manifest_bytes_%d", size), func(t *testing.T) {
			input := valid + strings.Repeat(" ", size-len(valid))
			_, _, err := loadHeldoutWebCases(strings.NewReader(input))
			if (err == nil) != (size == 1<<20) {
				t.Errorf("manifest byte bound not enforced at %d: %v", size, err)
			}
		})
	}
}

func TestHeldoutManifestIndependentCaseCountAndUniqueIDs(t *testing.T) {
	t.Parallel()
	for _, count := range []int{0, 1, 20, 21} {
		t.Run(fmt.Sprintf("count_%d", count), func(t *testing.T) {
			var input []map[string]any
			for index := 0; index < count; index++ {
				item := heldoutManifestFixtureCase()
				item["id"] = fmt.Sprintf("case_%02d", index)
				input = append(input, item)
			}
			cases, _, err := loadHeldoutWebCases(strings.NewReader(heldoutManifestFixtureJSON(input...)))
			wantValid := count >= 1 && count <= 20
			if (err == nil) != wantValid || wantValid && len(cases) != count {
				t.Errorf("count=%d returned %d cases err=%v", count, len(cases), err)
			}
		})
	}
	first, second := heldoutManifestFixtureCase(), heldoutManifestFixtureCase()
	second["question"] = "A different question must not make a duplicate ID valid."
	if _, _, err := loadHeldoutWebCases(strings.NewReader(heldoutManifestFixtureJSON(first, second))); err == nil {
		t.Error("duplicate ID accepted despite different case content")
	}
}

func TestHeldoutManifestIndependentRejectsNonStringRunnableFields(t *testing.T) {
	t.Parallel()
	for _, key := range []string{"id", "question", "time_zone", "as_of"} {
		for _, value := range []any{nil, 123, true, []any{"text"}, map[string]any{"text": "value"}} {
			t.Run(fmt.Sprintf("%s_%T", key, value), func(t *testing.T) {
				item := heldoutManifestFixtureCase()
				item[key] = value
				if _, _, err := loadHeldoutWebCases(strings.NewReader(heldoutManifestFixtureJSON(item))); err == nil {
					t.Errorf("non-string field accepted: %s=%#v", key, value)
				}
			})
		}
		t.Run(key+"_missing", func(t *testing.T) {
			item := heldoutManifestFixtureCase()
			delete(item, key)
			if _, _, err := loadHeldoutWebCases(strings.NewReader(heldoutManifestFixtureJSON(item))); err == nil {
				t.Errorf("missing runnable field accepted: %s", key)
			}
		})
	}
}

func TestHeldoutManifestIndependentTimezoneAndDSTAgreement(t *testing.T) {
	t.Parallel()
	for _, scenario := range []struct {
		name, zone, asOf string
		wantValid        bool
	}{
		{"explicit_utc", "UTC", "2026-09-04T18:00:00Z", true},
		{"quarter_hour_offset", "Asia/Kathmandu", "2026-09-04T18:00:00+05:45", true},
		{"summer_offset", "America/Los_Angeles", "2026-07-04T18:00:00-07:00", true},
		{"winter_offset", "America/Los_Angeles", "2026-01-04T18:00:00-08:00", true},
		{"summer_wrong_offset", "America/Los_Angeles", "2026-07-04T18:00:00-08:00", false},
		{"winter_wrong_offset", "America/Los_Angeles", "2026-01-04T18:00:00-07:00", false},
		{"dst_spring_gap_standard_offset", "America/Los_Angeles", "2026-03-08T02:30:00-08:00", false},
		{"dst_spring_gap_daylight_offset", "America/Los_Angeles", "2026-03-08T02:30:00-07:00", false},
		{"dst_fall_first_occurrence", "America/Los_Angeles", "2026-11-01T01:30:00-07:00", true},
		{"dst_fall_second_occurrence", "America/Los_Angeles", "2026-11-01T01:30:00-08:00", true},
		{"unknown_zone", "Unknown/Observatory", "2026-09-04T18:00:00Z", false},
		{"implicit_local_zone", "Local", "2026-09-04T18:00:00-07:00", false},
		{"empty_zone", "", "2026-09-04T18:00:00Z", false},
		{"missing_offset", "UTC", "2026-09-04T18:00:00", false},
		{"date_only", "UTC", "2026-09-04", false},
		{"invalid_calendar_date", "UTC", "2026-02-30T18:00:00Z", false},
		{"invalid_clock", "UTC", "2026-09-04T25:00:00Z", false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			item := heldoutManifestFixtureCase()
			item["time_zone"], item["as_of"] = scenario.zone, scenario.asOf
			_, _, err := loadHeldoutWebCases(strings.NewReader(heldoutManifestFixtureJSON(item)))
			if (err == nil) != scenario.wantValid {
				t.Errorf("valid=%t want=%t: zone=%q as_of=%q err=%v", err == nil, scenario.wantValid, scenario.zone, scenario.asOf, err)
			}
		})
	}
}

func TestHeldoutManifestIndependentRejectsTrailingOrMalformedJSON(t *testing.T) {
	t.Parallel()
	valid := heldoutManifestFixtureJSON(heldoutManifestFixtureCase())
	for _, input := range []string{"", "null", "[]", `{"cases":{}}`, `{"cases":[null]}`, valid + " {}", valid + " true", valid + " trailing", valid[:len(valid)-1]} {
		if _, _, err := loadHeldoutWebCases(strings.NewReader(input)); err == nil {
			t.Errorf("malformed/non-single-document manifest accepted: %q", input)
		}
	}
}

type heldoutManifestFailingReader struct{}

func (heldoutManifestFailingReader) Read([]byte) (int, error) {
	return 0, errors.New("synthetic reader error")
}

func TestHeldoutManifestIndependentRejectsReadErrorsAfterValidPrefix(t *testing.T) {
	t.Parallel()
	reader := io.MultiReader(strings.NewReader(heldoutManifestFixtureJSON(heldoutManifestFixtureCase())), heldoutManifestFailingReader{})
	if cases, digest, err := loadHeldoutWebCases(reader); err == nil || cases != nil || digest != "" {
		t.Errorf("reader failure returned usable manifest: cases=%v digest=%q err=%v", cases, digest, err)
	}
}

func TestHeldoutManifestIndependentGradingFieldsCannotLeakIntoRunnableCases(t *testing.T) {
	t.Parallel()
	baseline := heldoutManifestFixtureCase()
	decorated := heldoutManifestFixtureCase()
	for _, key := range []string{"rubric", "expected", "expected_facts", "expected_answer", "answer", "source_hints", "primary_source_validation", "validation_links", "label", "score"} {
		decorated[key] = map[string]any{"question": "GRADING_SENTINEL_" + key, "source_url": "https://hints.example/" + key, "nested": []any{"PRIVATE_REFERENCE"}}
	}
	baseCases, _, baseErr := loadHeldoutWebCases(strings.NewReader(heldoutManifestFixtureJSON(baseline)))
	withGrading, _, decoratedErr := loadHeldoutWebCases(strings.NewReader(heldoutManifestFixtureJSON(decorated)))
	if baseErr != nil || decoratedErr != nil || !reflect.DeepEqual(baseCases, withGrading) {
		t.Fatalf("grading fields altered runnable input: baseline=%v decorated=%v errors=%v/%v", baseCases, withGrading, baseErr, decoratedErr)
	}
	encoded, err := json.Marshal(withGrading)
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{"GRADING_SENTINEL", "PRIVATE_REFERENCE", "hints.example", "expected_facts", "source_hints"} {
		if strings.Contains(string(encoded), marker) {
			t.Errorf("grading marker leaked into runnable serialization: %s", marker)
		}
	}
	var serialized []map[string]any
	if err := json.Unmarshal(encoded, &serialized); err != nil {
		t.Fatal(err)
	}
	if len(serialized) != 1 || len(serialized[0]) != 4 {
		t.Errorf("runnable case must expose exactly id/question/time_zone/as_of: %s", encoded)
	}
}

func TestHeldoutManifestIndependentDigestCoversTamperedRunnableAndGradingData(t *testing.T) {
	t.Parallel()
	item := heldoutManifestFixtureCase()
	item["expected_facts"] = []any{"Frozen grading reference"}
	input := heldoutManifestFixtureJSON(item)
	baseCases, digest, err := loadHeldoutWebCases(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	wantDigest := sha256.Sum256([]byte(input))
	if digest != hex.EncodeToString(wantDigest[:]) {
		t.Fatalf("digest is not SHA256 of exact manifest bytes: got %q", digest)
	}
	for _, scenario := range []struct {
		name, mutated string
		sameCases     bool
	}{
		{"runnable_question", strings.Replace(input, "Find public opening hours.", "Find a different public fact.", 1), false},
		{"grading_reference", strings.Replace(input, "Frozen grading reference", "Tampered grading reference", 1), true},
		{"whitespace", input + "\n", true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			cases, changedDigest, err := loadHeldoutWebCases(strings.NewReader(scenario.mutated))
			if err != nil {
				t.Fatal(err)
			}
			if changedDigest == digest {
				t.Error("manifest tampering did not change recorded digest")
			}
			if reflect.DeepEqual(cases, baseCases) != scenario.sameCases {
				t.Errorf("runnable-case changes disagree with mutation kind: base=%v changed=%v", baseCases, cases)
			}
		})
	}
}
