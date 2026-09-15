package memory

import (
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestCandidateProfileLayers(t *testing.T) {
	for _, tc := range []struct {
		layer     ProfileLayer
		retention Retention
		want      ProfileLayer
		invalid   bool
	}{
		{"", RetentionDurable, ProfileDetail, false},
		{"", RetentionTemporary, ProfileRecent, false},
		{ProfileCore, RetentionDurable, ProfileCore, false},
		{ProfileRecent, RetentionTemporary, ProfileRecent, false},
		{ProfileDetail, RetentionTemporary, ProfileDetail, false},
		{ProfileRecent, RetentionDurable, ProfileDetail, false},
		{ProfileCore, RetentionTemporary, ProfileRecent, false},
		{"invented", RetentionDurable, "invented", true},
	} {
		candidate := Candidate{MemoryKey: "profile.role.student", Retention: tc.retention, ProfileLayer: tc.layer,
			Card: Card{Title: "Student", Summary: "The user is a student.", Kind: KindFact, Topics: []Topic{TopicPersonal}}}.Normalize()
		if candidate.ProfileLayer != tc.want || (candidate.Validate() != nil) != tc.invalid {
			t.Fatalf("layer=%q retention=%q: got=%q err=%v", tc.layer, tc.retention, candidate.ProfileLayer, candidate.Validate())
		}
		if candidate.Retention != tc.retention || candidate.ExpiresAt != nil {
			t.Fatal("profile normalization changed the fact's lifecycle")
		}
	}
}

func TestProfileRenderingBoundsDeduplicatesAndKeepsSources(t *testing.T) {
	expiry := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	entries := []profileEntry{
		{ID: "one", Summary: "Student at North College.", Layer: ProfileCore},
		{ID: "duplicate", Summary: " Student at North College. ", Layer: ProfileCore},
		{ID: "now", Summary: "Just left the gym.", Layer: ProfileRecent, ExpiresAt: &expiry},
		{ID: "detail", Summary: "Likes one cafe.", Layer: ProfileDetail},
	}
	got := renderProfile(entries)
	for _, want := range []string{"## Core", "## Recent", "[memory:one]", "expires 2026-09-05T12:00:00Z"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Count(got, "Student at North College.") != 1 || strings.Contains(got, "Likes one cafe") {
		t.Fatal("profile repeated a fact or included a detail")
	}
	for i := 0; i < 40; i++ {
		entries = append(entries, profileEntry{ID: fmt.Sprint(i), Layer: ProfileCore, Summary: strings.Repeat("é", 450) + fmt.Sprint(i)})
	}
	got = renderProfile(entries)
	if utf8.RuneCountInString(got) > 2*profileSectionRunes+500 || !strings.Contains(got, "omitted for space") {
		t.Fatal("profile is unbounded or silently truncated")
	}
	if !strings.Contains(got, "Just left the gym") {
		t.Fatal("core entries crowded out recent context")
	}
}
