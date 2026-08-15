package render

import (
	"strings"
	"testing"
	"time"
)

func sampleWhen() When {
	return When{
		Verb: "when", Query: "agvtool",
		Hits:  4,
		First: "2026-07-21", Last: "2026-08-13",
		Buckets: []Bucket{
			{Month: "2026-07", Hits: 1, Sessions: 1},
			{Month: "2026-08", Hits: 3, Sessions: 2},
		},
		Sessions: []Session{
			{ID: "b5ddc1af", Repo: "gitlab.example/acme/mobile", Hits: 1, Turns: 64, TurnsKnown: true, First: "2026-07-21", Last: "2026-07-21",
				Shown: []Hit{{Snippet: "…ran agvtool what-version…"}}},
			{ID: "b16d73cc", Repo: "gitlab.example/acme/mobile", Hits: 3, Turns: 119, TurnsKnown: true, First: "2026-08-13", Last: "2026-08-13",
				Shown: []Hit{{Snippet: "…agvtool ships with Xcode…"}}},
		},
		Coverage: sampleCoverage(),
	}
}

// TestWhenTextPlacesSessionsOldestFirstWithTheirMonthlyBuckets holds the
// pinned wording: `when` answers "roughly when", so the buckets and the
// oldest-first ordering both have to appear before the sessions themselves.
func TestWhenTextPlacesSessionsOldestFirstWithTheirMonthlyBuckets(t *testing.T) {
	got := string(sampleWhen().Text())
	for _, want := range []string{
		`"agvtool"  first 2026-07-21 · last 2026-08-13 · 4 hits in 2 sessions`,
		"2026-07     1 hit · 1 session",
		"2026-08     3 hits · 2 sessions",
		"oldest first",
		"b5ddc1af",
		"b16d73cc",
		"── 2 sessions · 2 searched · conversation only",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output is missing %q:\n%s", want, got)
		}
	}
	oldest := strings.Index(got, "b5ddc1af")
	newest := strings.Index(got, "b16d73cc")
	if oldest < 0 || newest < 0 || oldest > newest {
		t.Errorf("sessions are not oldest first:\n%s", got)
	}
}

// TestWhenTextOnAMissOffersWhatExistsElsewhere mirrors find and turns: a dead
// end reports where the query does live rather than a bare zero.
func TestWhenTextOnAMissOffersWhatExistsElsewhere(t *testing.T) {
	w := When{
		Verb: "when", Query: "agvtool",
		Scope:     Scope{Repo: "acme/tooling"},
		Elsewhere: []Elsewhere{{Repo: "acme/mobile", Hits: 2, Sessions: 1}},
		Coverage:  sampleCoverage(),
	}
	got := string(w.Text())
	if want := `no hits for "agvtool" in acme/tooling`; !strings.Contains(got, want) {
		t.Errorf("output is missing %q:\n%s", want, got)
	}
	if !strings.Contains(got, "found elsewhere") {
		t.Errorf("output dropped what exists elsewhere:\n%s", got)
	}
}

func TestWhenBriefDropsSnippetsAndKeepsTheTimeline(t *testing.T) {
	full, brief := sampleWhen().Text(), sampleWhen().Brief()
	if len(brief) >= len(full) {
		t.Errorf("Brief (%d bytes) is not smaller than Text (%d bytes)", len(brief), len(full))
	}
	if !strings.Contains(string(brief), "2026-08     3 hits · 2 sessions") {
		t.Errorf("Brief dropped the timeline it exists to keep:\n%s", brief)
	}
}

func TestWhenIDsAreSessionIDsOldestFirst(t *testing.T) {
	got := string(sampleWhen().IDs())
	if got != "b5ddc1af\nb16d73cc\n" {
		t.Errorf("IDs() = %q, want the two session ids in order", got)
	}
}

// TestWhenJSONLMatchesFindsShapeSoOneParserReadsBoth is the reason When.JSONL
// exists: it delegates to Find so `when --format jsonl` and `find --format
// jsonl` are not two formats a caller has to learn.
func TestWhenJSONLMatchesFindsShapeSoOneParserReadsBoth(t *testing.T) {
	w := sampleWhen()
	got, err := w.JSONL()
	if err != nil {
		t.Fatalf("JSONL: %v", err)
	}
	want, err := (Find{Query: w.Query, Hits: w.Hits, Sessions: w.Sessions, Coverage: w.Coverage}).JSONL()
	if err != nil {
		t.Fatalf("Find.JSONL: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("When.JSONL diverged from the Find shape it is meant to match:\ngot  %s\nwant %s", got, want)
	}
}

func TestDashStandsInForAnEmptyBoundary(t *testing.T) {
	if got := dash(""); got != "-" {
		t.Errorf("dash(\"\") = %q, want %q", got, "-")
	}
	if got := dash("2026-08-01"); got != "2026-08-01" {
		t.Errorf("dash(non-empty) = %q, want it unchanged", got)
	}
}

func TestParseStampRejectsWhatIsNotRFC3339RatherThanGuessing(t *testing.T) {
	want := time.Date(2026, 8, 13, 10, 1, 0, 0, time.UTC)
	if got := parseStamp("2026-08-13T10:01:00Z"); !got.Equal(want) {
		t.Errorf("parseStamp(valid) = %v, want %v", got, want)
	}
	if got := parseStamp("not a timestamp"); !got.IsZero() {
		t.Errorf("parseStamp(garbage) = %v, want the zero time", got)
	}
	if got := parseStamp(""); !got.IsZero() {
		t.Errorf("parseStamp(\"\") = %v, want the zero time", got)
	}
}
