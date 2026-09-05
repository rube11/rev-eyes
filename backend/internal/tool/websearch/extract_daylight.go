package websearch

import "regexp"

const daylightRangeSeparator = `(?:\s+(?:to|until|through)\s+|\s*[-–—]\s*(?:(?:to|until|through)\s*[-–—]?\s*)?)`

var daylightHoursPattern = regexp.MustCompile(`(?i)\b(?:sunrise` + daylightRangeSeparator + `sunset|dawn` + daylightRangeSeparator + `dusk)\b`)

// Paired daylight endpoints can express hours just as numeric clocks do. A lone
// sunrise/sunset mention is not a range and receives no timing-specific weight.
func hasTimingEvidence(text string) bool {
	return clockEvidencePattern.MatchString(text) || daylightHoursPattern.MatchString(text)
}
