package websearch

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
	"unicode/utf8"
)

// Receives library-extracted HTML as data. No network, model or alternate
// ranking algorithm: all arms use the current Go normalizer/chunk selector.
func TestOfflineExtractorAblation(t *testing.T) {
	path := os.Getenv("OFFLINE_EXTRACTOR_ABLATION_INPUT")
	if path == "" {
		t.Skip("set OFFLINE_EXTRACTOR_ABLATION_INPUT and OFFLINE_EXTRACTOR_ABLATION_REPORT")
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	data, readErr := io.ReadAll(io.LimitReader(file, (32<<20)+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || len(data) > 32<<20 {
		t.Fatal("cannot read bounded ablation input")
	}
	type arm struct {
		Name                  string   `json:"name"`
		HTML                  string   `json:"html,omitempty"`
		PreserveStructured    bool     `json:"preserve_original_structured"`
		Error                 string   `json:"error,omitempty"`
		Elapsed               float64  `json:"extraction_ms,omitempty"`
		Text                  string   `json:"full_text"`
		TextRunes             int      `json:"full_text_runes"`
		Excerpts              []string `json:"selected_excerpts"`
		CapturedQueryExcerpts []string `json:"captured_search_query_excerpts,omitempty"`
	}
	var report struct {
		Mode            string            `json:"mode"`
		Scope           string            `json:"scope"`
		ManifestSHA     string            `json:"manifest_sha256"`
		QueryTraceSHA   string            `json:"query_trace_sha256"`
		Versions        map[string]string `json:"versions"`
		Captures        json.RawMessage   `json:"captures"`
		NetworkAttempts []string          `json:"network_attempts"`
		InputSHA        string            `json:"ablation_input_sha256"`
		Pages           []struct {
			URL              string `json:"url"`
			CaseID           string `json:"case_id"`
			Query            string `json:"query"`
			CapturedQuery    string `json:"last_captured_search_query,omitempty"`
			AcquisitionError string `json:"acquisition_error,omitempty"`
			MarkupSHA        string `json:"markup_sha256,omitempty"`
			Arms             []arm  `json:"arms"`
		} `json:"pages"`
	}
	if json.Unmarshal(data, &report) != nil || report.Mode != "offline_identical_html_extractor_ablation" || len(report.NetworkAttempts) != 0 || len(report.Pages) == 0 || len(report.Pages) > 20 {
		t.Fatal("invalid offline ablation input")
	}
	for pageIndex := range report.Pages {
		page := &report.Pages[pageIndex]
		if page.AcquisitionError != "" {
			continue
		}
		if len(page.Arms) == 0 || page.Arms[0].Name != "go_current" {
			t.Fatal("missing identical-HTML baseline")
		}
		original := page.Arms[0].HTML
		digest := sha256.Sum256([]byte(original))
		if hex.EncodeToString(digest[:]) != page.MarkupSHA {
			t.Fatal("original markup hash mismatch")
		}
		structured := extractStructuredEvidence(original)
		for armIndex := range page.Arms {
			candidate := &page.Arms[armIndex]
			if candidate.Error != "" {
				continue
			}
			candidate.Text = extractReadableText(candidate.HTML)
			if candidate.PreserveStructured && structured != "" {
				candidate.Text = strings.TrimSpace(candidate.Text + "\n" + structured)
			}
			candidate.HTML = "" // Raw inputs remain in the separately hashed input.
			candidate.TextRunes = utf8.RuneCountInString(candidate.Text)
			candidate.Excerpts = completePageExcerpts(selectQueryChunks(page.Query, candidate.Text), maxSnippetLength)
			if page.CapturedQuery != "" {
				candidate.CapturedQueryExcerpts = completePageExcerpts(selectQueryChunks(page.CapturedQuery, candidate.Text), maxSnippetLength)
			}
			if utf8.RuneCountInString(strings.Join(candidate.Excerpts, "\n\n")) > maxSnippetLength || len(candidate.Excerpts) > maxChunksPerSource {
				t.Fatal("production excerpt bounds changed")
			}
			t.Logf("case=%s arm=%s full_runes=%d selected_blocks=%d", page.CaseID, candidate.Name, candidate.TextRunes, len(candidate.Excerpts))
		}
	}
	digest := sha256.Sum256(data)
	report.InputSHA = hex.EncodeToString(digest[:])
	report.Scope += " All selected_excerpts use the same production Go normalizer, selector and source budget. This diagnostic does not assign semantic grades."
	output, err := os.OpenFile(os.Getenv("OFFLINE_EXTRACTOR_ABLATION_REPORT"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	writeErr := encoder.Encode(report)
	closeErr = output.Close()
	if writeErr != nil || closeErr != nil {
		t.Fatal("cannot write new ablation report")
	}
}
