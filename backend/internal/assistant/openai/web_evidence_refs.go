package openai

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"unicode/utf8"
)

// References identify immutable, turn-local capture rows and single fetched
// blocks. They prove retrieval identity, not the meaning of a model's claim.
type webEvidenceReference struct {
	ID     string            `json:"id"`
	Source webEvidenceSource `json:"source"`
}

type webEvidenceReferences struct {
	namespace string
	nextRow   uint64
	rows      map[string]webEvidenceSource
	active    map[string]webEvidenceSource
}

// Match the fetcher's maximum three selected blocks per source. Malformed
// captures cannot expand the registry; extra blocks are omitted whole.
const maxReferencedBlocksPerSource = 3

func newWebEvidenceReferences() (*webEvidenceReferences, error) {
	var namespace [16]byte
	if _, err := rand.Read(namespace[:]); err != nil {
		return nil, fmt.Errorf("create web evidence reference namespace: %w", err)
	}
	return &webEvidenceReferences{
		namespace: "e" + hex.EncodeToString(namespace[:]),
		rows:      make(map[string]webEvidenceSource),
		active:    make(map[string]webEvidenceSource),
	}, nil
}

// The collector owns URL/row caps. Rebuild only from its retained rows; an
// evicted reference cannot resolve, and a reused slice index cannot rebind it.
func (r *webEvidenceReferences) sync(sources map[string][]webEvidenceSource) {
	if r == nil {
		return
	}
	rows := make(map[string]webEvidenceSource)
	active := make(map[string]webEvidenceSource)
	urls := make([]string, 0, len(sources))
	for endpoint := range sources {
		urls = append(urls, endpoint)
	}
	sort.Strings(urls)
	for _, endpoint := range urls {
		for index := range sources[endpoint] {
			source := sources[endpoint][index]
			stamp := source.referenceRowID
			previous, known := r.rows[stamp]
			_, repeated := rows[stamp]
			if !known || repeated || !sameReferenceSource(previous, source) {
				r.nextRow++
				stamp = r.namespace + "r" + strconv.FormatUint(r.nextRow, 10)
			}
			source.referenceRowID = stamp
			sources[endpoint][index] = source
			rows[stamp] = cloneReferenceSource(source)
			if source.URL != endpoint || source.ExtractionStatus != "succeeded" {
				continue
			}
			if _, valid := sourceHostname(source.URL); !valid {
				continue
			}
			for blockIndex, block := range source.PageExcerpts {
				if blockIndex >= maxReferencedBlocksPerSource {
					break
				}
				// Preserve the whole record or omit it. Never shorten away an
				// owner, cancellation, availability restriction, or late qualifier.
				if utf8.RuneCountInString(block) > 1000 || utf8.RuneCountInString(normalizeEvidence(block)) < 8 {
					continue
				}
				bound := cloneReferenceSource(source)
				bound.PageExcerpts = []string{block}
				active[referenceBlockID(stamp, blockIndex)] = bound
			}
		}
	}
	r.rows, r.active = rows, active
}

func referenceBlockID(rowID string, blockIndex int) string {
	return rowID + "b" + strconv.Itoa(blockIndex+1)
}

func cloneReferenceSource(source webEvidenceSource) webEvidenceSource {
	if source.PageExcerpts != nil {
		excerpts := make([]string, len(source.PageExcerpts))
		copy(excerpts, source.PageExcerpts)
		source.PageExcerpts = excerpts
	}
	return source
}

func sameReferenceSource(left, right webEvidenceSource) bool {
	left.referenceRowID, right.referenceRowID = "", ""
	return reflect.DeepEqual(left, right)
}

func (r *webEvidenceReferences) resolve(id string) (webEvidenceSource, bool) {
	if r == nil {
		return webEvidenceSource{}, false
	}
	source, found := r.active[id]
	return cloneReferenceSource(source), found
}

func (r *webEvidenceReferences) snapshot() []webEvidenceReference {
	if r == nil {
		return nil
	}
	ids := make([]string, 0, len(r.active))
	for id := range r.active {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	references := make([]webEvidenceReference, 0, len(ids))
	for _, id := range ids {
		references = append(references, webEvidenceReference{ID: id, Source: cloneReferenceSource(r.active[id])})
	}
	return references
}

// Only private model input changes. Raw tool outputs remain byte-for-byte
// untouched for provider compatibility and evaluation capture. Unretained or
// ineligible blocks get no IDs, including contradictory upstream provenance.
func (r *webEvidenceReferences) project(calls []toolCall, outputs []json.RawMessage, sources map[string][]webEvidenceSource) []json.RawMessage {
	projected := make([]json.RawMessage, len(outputs))
	for index, original := range outputs {
		projected[index] = append(json.RawMessage(nil), original...)
		if index >= len(calls) || calls[index].Name != "search_web" {
			continue
		}
		var envelope map[string]json.RawMessage
		var content string
		var payload map[string]json.RawMessage
		var provider string
		var resultRows []json.RawMessage
		if json.Unmarshal(original, &envelope) != nil || json.Unmarshal(envelope["output"], &content) != nil ||
			json.Unmarshal([]byte(content), &payload) != nil || json.Unmarshal(payload["provider"], &provider) != nil ||
			provider != "searxng" || json.Unmarshal(payload["results"], &resultRows) != nil {
			continue
		}
		changed := false
		for rowIndex, raw := range resultRows {
			var fields map[string]json.RawMessage
			if json.Unmarshal(raw, &fields) != nil {
				continue
			}
			if _, present := fields["page_excerpts"]; !present {
				continue
			}
			blocks := make([]struct {
				ID   string `json:"id"`
				Text string `json:"text"`
			}, 0)
			var source webEvidenceSource
			if json.Unmarshal(raw, &source) == nil && r != nil {
				for _, retained := range sources[source.URL] {
					stored, known := r.rows[retained.referenceRowID]
					if !known || !sameReferenceSource(source, retained) || !sameReferenceSource(stored, retained) {
						continue
					}
					for blockIndex, text := range source.PageExcerpts {
						id := referenceBlockID(retained.referenceRowID, blockIndex)
						if _, found := r.active[id]; found {
							blocks = append(blocks, struct {
								ID   string `json:"id"`
								Text string `json:"text"`
							}{id, text})
						}
					}
					break
				}
			}
			fields["page_excerpts"], _ = json.Marshal(blocks)
			resultRows[rowIndex], _ = json.Marshal(fields)
			changed = true
		}
		if changed {
			payload["results"], _ = json.Marshal(resultRows)
			encoded, _ := json.Marshal(payload)
			envelope["output"], _ = json.Marshal(string(encoded))
			projected[index], _ = json.Marshal(envelope)
		}
	}
	return projected
}

// Re-present selected stored references, never model-supplied evidence text.
// The existing final review owns interpretation; this does no new retrieval.
func (r *webEvidenceReferences) review(raw string) string {
	var answer struct {
		Claims []struct {
			SupportExcerptID string `json:"support_excerpt_id"`
		} `json:"claims"`
	}
	if json.Unmarshal([]byte(raw), &answer) != nil {
		return ""
	}
	var references []webEvidenceReference
	seen := make(map[string]bool)
	for index, claim := range answer.Claims {
		if index >= 3 {
			break
		}
		if seen[claim.SupportExcerptID] {
			continue
		}
		seen[claim.SupportExcerptID] = true
		if source, found := r.resolve(claim.SupportExcerptID); found {
			references = append(references, webEvidenceReference{ID: claim.SupportExcerptID, Source: source})
		}
	}
	for len(references) > 0 {
		encoded, err := json.Marshal(references)
		if err != nil {
			return ""
		}
		if len(encoded) <= 20000 {
			return string(encoded)
		}
		references = references[:len(references)-1]
	}
	return ""
}
