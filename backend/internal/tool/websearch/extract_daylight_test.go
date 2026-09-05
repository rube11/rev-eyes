package websearch

import (
	"strings"
	"testing"
)

func TestDaylightHoursKeepDayUseRangeAlongsideNumericOfficeHours(t *testing.T) {
	for _, daylight := range []string{"sunrise to sunset", "sunrise–sunset", "dawn to dusk", "dawn-to-dusk"} {
		t.Run(daylight, func(t *testing.T) {
			markup := `<main><h1>Birch Reserve</h1><h2>Day-use grounds</h2><p>Opening hours are ` + daylight + `; overnight access is prohibited.</p>
<h2>Office</h2><p>Opening hours are 9am to 4pm on weekdays.</p>
<p>Opening hours are 10am to 2pm on Saturdays.</p>
<p>Opening hours are 11am to 1pm on Sundays.</p></main>`
			got := selectQueryChunks("Birch Reserve opening hours", extractReadableText(markup))
			if !strings.Contains(got, daylight) || !strings.Contains(got, "9am to 4pm") {
				t.Fatalf("daylight hours lost to numeric office ranges: %q", got)
			}
			for _, block := range strings.Split(got, "\n\n") {
				if strings.Contains(block, daylight) && (!strings.Contains(block, "Day-use grounds") || !strings.Contains(block, "overnight access is prohibited") || strings.Contains(block, "Office")) {
					t.Errorf("day-use ownership or qualifier changed: %q", block)
				}
				if strings.Contains(block, "9am to 4pm") && (!strings.Contains(block, "Office") || strings.Contains(block, "Day-use grounds")) {
					t.Errorf("office clock transferred to grounds: %q", block)
				}
			}
		})
	}
}

func TestDaylightHoursKeepShortRangeOnlyUnderRelevantHeading(t *testing.T) {
	const markup = `<h1>Birch Reserve</h1><h2>Day-use grounds</h2><p>dawn–dusk</p>`
	if got := selectQueryChunks("Birch Reserve hours", extractReadableText(markup)); got != "Section: Birch Reserve > Day-use grounds\ndawn–dusk" {
		t.Errorf("short owned daylight range was lost: %q", got)
	}
	for _, test := range []struct{ query, markup string }{
		{"Birch Reserve address", markup},
		{"Birch Reserve menu prices", markup},
		{"dawn dusk hours", `<p>dawn–dusk</p>`},
		{"Birch Reserve hours", `<h1>Other Place</h1><p>dawn–dusk</p>`},
	} {
		if got := selectQueryChunks(test.query, extractReadableText(test.markup)); got != "" {
			t.Errorf("bare/unrelated daylight range became evidence for %q: %q", test.query, got)
		}
	}
}

func TestDaylightHoursDoNotBoostScenicSunsetProse(t *testing.T) {
	markup := `<h1>Birch Reserve</h1><h2>Views</h2><p>Opening hours offer beautiful sunset views.</p>
<h2>Office</h2><p>Opening hours are 9am to 4pm on weekdays.</p>
<p>Opening hours are 10am to 2pm on Saturdays.</p>
<p>Opening hours are 11am to 1pm on Sundays.</p>`
	got := selectQueryChunks("Birch Reserve opening hours", extractReadableText(markup))
	if strings.Contains(got, "sunset views") || !strings.Contains(got, "11am to 1pm") {
		t.Errorf("lone scenic sunset gained the clock bonus: %q", got)
	}
}
