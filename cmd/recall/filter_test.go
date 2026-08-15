package main

import (
	"strings"
	"testing"
	"time"

	"github.com/mayberuk/recall/internal/schema"
)

var now = time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)

func TestParseWhenReadsRelativeAgesAndDates(t *testing.T) {
	cases := []struct {
		in   string
		want time.Time
	}{
		{"", time.Time{}},
		{"12h", now.Add(-12 * time.Hour)},
		{"3d", now.Add(-72 * time.Hour)},
		{"2w", now.Add(-14 * 24 * time.Hour)},
		{"1m", now.Add(-30 * 24 * time.Hour)},
		{"1y", now.Add(-365 * 24 * time.Hour)},
		{"2026-08-01", time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)},
		{"2026-08-01T09:30:00Z", time.Date(2026, 8, 1, 9, 30, 0, 0, time.UTC)},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := parseWhen("--since", tc.in, now)
			if err != nil {
				t.Fatalf("parseWhen(%q): %v", tc.in, err)
			}
			if !got.Equal(tc.want) {
				t.Errorf("parseWhen(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseWhenRejectsWhatItCannotPlace(t *testing.T) {
	if _, err := parseWhen("--since", "last tuesday", now); err == nil {
		t.Error("an unparseable age was accepted, so the filter would silently do nothing")
	}
}

func TestSinceAndUntilBoundTheTurnsSearched(t *testing.T) {
	f := filter{
		since: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		until: time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC),
	}
	keep := f.keep()
	cases := []struct {
		ts   string
		want bool
	}{
		{"2026-07-31T23:59:59Z", false},
		{"2026-08-01T00:00:00Z", true},
		{"2026-08-05T12:00:00Z", true},
		{"2026-08-10T00:00:00Z", true},
		{"2026-08-10T00:00:01Z", false},
		// An undated turn cannot be placed either side of the boundary, and
		// dropping it would be a false negative made by the filter itself.
		{"", true},
	}
	for _, tc := range cases {
		if got := keep(&schema.Turn{TS: tc.ts}); got != tc.want {
			t.Errorf("ts %q kept=%v, want %v", tc.ts, got, tc.want)
		}
	}
}

func TestAuthorBranchAgentAndSessionNarrowIndependently(t *testing.T) {
	turn := schema.Turn{
		Session: "5fd86b00-f55c-4a8f-9cfd-0ff34b08058b",
		Author:  schema.AuthorAssistant,
		Branch:  "staging",
		Agent:   "review-thread-claude",
		TS:      "2026-08-05T12:00:00Z",
	}
	cases := []struct {
		name string
		f    filter
		want bool
	}{
		{"matching author", filter{author: schema.AuthorAssistant}, true},
		{"other author", filter{author: schema.AuthorHuman}, false},
		{"matching branch, case-insensitive", filter{branch: "STAGING"}, true},
		{"other branch", filter{branch: "main"}, false},
		{"agent by substring", filter{agent: "review"}, true},
		{"other agent", filter{agent: "explorer"}, false},
		{"session by prefix", filter{session: "5fd86b00"}, true},
		{"other session", filter{session: "b5ddc1af"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := tc.f
			if got := f.keep()(&turn); got != tc.want {
				t.Errorf("kept=%v, want %v", got, tc.want)
			}
		})
	}
}

// Asking what happened before and getting back the session doing the asking is
// worse than useless, and Claude Code names the session in the environment, so
// the exclusion is exact rather than a guess.
func TestTheCallingSessionIsExcludedAndCounted(t *testing.T) {
	f := filter{self: "mine"}
	keep := f.keep()
	if keep(&schema.Turn{Session: "mine"}) {
		t.Error("the calling session was searched")
	}
	if !keep(&schema.Turn{Session: "other"}) {
		t.Error("another session was excluded")
	}
	if f.dropped.self != 1 {
		t.Errorf("counted %d skipped turns, want 1 — the footer reports the count, not the setting", f.dropped.self)
	}
	if got := f.snapshot().narrowings(); len(got) != 1 {
		t.Errorf("narrowings %v, want one line naming --include-self", got)
	}
}

func TestAnExclusionThatSkippedNothingIsNotDeclared(t *testing.T) {
	f := filter{self: "mine", dropRecall: true}
	keep := f.keep()
	keep(&schema.Turn{Session: "other", Tier: schema.TierConversation, Text: "ordinary prose"})
	if got := f.snapshot().narrowings(); len(got) != 0 {
		t.Errorf("declared %v, want nothing — a narrowing that removed nothing narrowed nothing", got)
	}
}

// recall reads the transcripts it is itself recorded in, so its own commands
// and output rank top for the query being asked, and worsen with every use.
func TestRecallsOwnCommandsAndOutputAreRecognised(t *testing.T) {
	cases := []struct {
		name string
		turn schema.Turn
		want bool
	}{
		{"its own output, by the coverage line", schema.Turn{
			Tier: schema.TierResult,
			Text: "2 sessions · 28 hits for \"agvtool\"\n── 56 sessions · 56 searched · conversation only\n",
		}, true},
		{"a tool result that merely says the word", schema.Turn{
			Tier: schema.TierResult,
			Text: "recall is a command-line tool that searches past sessions",
		}, false},
		{"its own command line", schema.Turn{
			Tier: schema.TierInvocation,
			Text: "Bash recall find agvtool --all",
		}, true},
		{"a command line after a directory change", schema.Turn{
			Tier: schema.TierInvocation,
			Text: "Bash cd ~/dev/api-server-3 && recall show 5fd86b00",
		}, true},
		{"the flag-only form", schema.Turn{
			Tier: schema.TierInvocation,
			Text: "Bash recall --version",
		}, true},
		{"another program whose name ends in recall", schema.Turn{
			Tier: schema.TierInvocation,
			Text: "Bash total-recall find agvtool",
		}, false},
		{"building it, which is not asking it anything", schema.Turn{
			Tier: schema.TierInvocation,
			Text: "Bash go build -o ~/.local/bin/recall ./cmd/recall",
		}, false},
		{"conversation about it stays searchable", schema.Turn{
			Tier: schema.TierConversation,
			Text: "── 56 sessions · 56 searched · conversation only",
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isRecallOutput(&tc.turn); got != tc.want {
				t.Errorf("isRecallOutput = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestMineAndAuthorAreOneNarrowingWithTwoSpellings(t *testing.T) {
	f := newSearchFlags()
	f.Mine = true
	if err := f.buildFilter(now); err != nil {
		t.Fatalf("buildFilter: %v", err)
	}
	if f.filter.author != schema.AuthorHuman {
		t.Errorf("--mine resolved to author %q, want human", f.filter.author)
	}
	if got := f.filter.limits(1, 2); len(got) != 1 || got[0].Flag != "--mine" {
		t.Errorf("coverage quoted %v, want the spelling the caller typed", got)
	}

	clash := newSearchFlags()
	clash.Mine, clash.Author = true, "assistant"
	if err := clash.buildFilter(now); err == nil {
		t.Error("--mine with --author assistant asks for two different things and was accepted")
	}
}

func TestUntilBeforeSinceIsRejected(t *testing.T) {
	f := newSearchFlags()
	f.Since, f.Until = "2026-08-10", "2026-08-01"
	err := f.buildFilter(now)
	if err == nil {
		t.Fatal("a window that cannot contain anything was accepted")
	}
	if !strings.Contains(err.Error(), "--until") {
		t.Errorf("error does not name the flag to change: %v", err)
	}
}

// TestRelativeRejectsAnAgeTooShortToCarryAUnit is the length guard: a bare
// unit letter with no digit is not an age, and reading it as one would
// silently accept a typo as a time filter.
func TestRelativeRejectsAnAgeTooShortToCarryAUnit(t *testing.T) {
	if _, ok := relative("d"); ok {
		t.Error(`relative("d") = ok, want false — no digit to parse`)
	}
	if _, ok := relative(""); ok {
		t.Error(`relative("") = ok, want false`)
	}
}

// TestParseAuthorAcceptsEveryDeclaredSpellingAndRejectsTheRest pins every
// branch of the switch: each of the four schema.Author values on its own
// spelling, and an unrecognised one reported rather than silently ignored.
func TestParseAuthorAcceptsEveryDeclaredSpellingAndRejectsTheRest(t *testing.T) {
	cases := []struct {
		in   string
		want schema.Author
	}{
		{"", ""},
		{"human", schema.AuthorHuman},
		{"HUMAN", schema.AuthorHuman},
		{"assistant", schema.AuthorAssistant},
		{"agent", schema.AuthorAgent},
		{"system", schema.AuthorSystem},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := parseAuthor(tc.in)
			if err != nil {
				t.Fatalf("parseAuthor(%q): %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("parseAuthor(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
	if _, err := parseAuthor("bot"); err == nil {
		t.Error(`parseAuthor("bot") was accepted; it is not one of the four declared authors`)
	}
}

// TestSnapshotOnAFilterThatNeverDroppedAnythingIsZero covers the filter
// built with no exclusions at all: keep() was never called, so dropped is
// still nil, and snapshot must report zero rather than dereference it.
func TestSnapshotOnAFilterThatNeverDroppedAnythingIsZero(t *testing.T) {
	var f filter
	if got := f.snapshot(); got != (drops{}) {
		t.Errorf("snapshot() on an untouched filter = %+v, want the zero value", got)
	}
}

// TestKeepIsNilWhenNothingNarrows lets the scanner skip the predicate call
// entirely on the common case of an unfiltered search.
func TestKeepIsNilWhenNothingNarrows(t *testing.T) {
	var f filter
	if got := f.keep(); got != nil {
		t.Error("keep() on an empty filter is not nil, so every turn now pays a predicate call")
	}
}

// TestDropRecallExcludesAndCountsRecallsOwnOutput is the other half of
// TestTheCallingSessionIsExcludedAndCounted: --include-recall's own
// exclusion, dropped and counted the same way --include-self is.
func TestDropRecallExcludesAndCountsRecallsOwnOutput(t *testing.T) {
	f := filter{dropRecall: true}
	keep := f.keep()
	recallOutput := &schema.Turn{
		Tier: schema.TierResult,
		Text: "2 sessions · 28 hits for \"agvtool\"\n── 56 sessions · 56 searched · conversation only\n",
	}
	if keep(recallOutput) {
		t.Error("recall's own output was searched despite --include-recall being unset")
	}
	if f.dropped.recall != 1 {
		t.Errorf("counted %d dropped turns, want 1", f.dropped.recall)
	}
	if !keep(&schema.Turn{Tier: schema.TierConversation, Text: "ordinary prose"}) {
		t.Error("ordinary conversation was excluded by --include-recall's default")
	}
	if got := f.snapshot().narrowings(); len(got) != 1 || !strings.Contains(got[0], "--include-recall") {
		t.Errorf("narrowings %v, want one line naming --include-recall", got)
	}
}

// TestLimitsNamesEveryNarrowingFlagThatWasSet covers the four flags no other
// test reaches: each has to appear in the coverage line on its own, quoted
// with the value the caller typed.
func TestLimitsNamesEveryNarrowingFlagThatWasSet(t *testing.T) {
	f := filter{
		branch:  "staging",
		agent:   "review",
		session: "5fd86b00",
		since:   time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		until:   time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC),
	}
	got := f.limits(3, 10)
	if len(got) != 1 {
		t.Fatalf("limits() = %v, want exactly one Limit combining every narrowing flag", got)
	}
	for _, want := range []string{"--branch staging", "--agent review", "--session 5fd86b00", "--since 2026-08-01", "--until 2026-08-10"} {
		if !strings.Contains(got[0].Flag, want) {
			t.Errorf("Flag = %q, missing %q", got[0].Flag, want)
		}
	}
	if got[0].Shown != 3 || got[0].Total != 10 {
		t.Errorf("Shown/Total = %d/%d, want 3/10", got[0].Shown, got[0].Total)
	}
}

func TestLimitsIsNilWhenNothingWasNarrowed(t *testing.T) {
	var f filter
	if got := f.limits(5, 5); got != nil {
		t.Errorf("limits() on an unfiltered search = %v, want nil", got)
	}
}

func TestNotIsRepeatable(t *testing.T) {
	var l stringList
	if err := l.Set("testbuild"); err != nil {
		t.Fatal(err)
	}
	if err := l.Set("preamble"); err != nil {
		t.Fatal(err)
	}
	if len(l) != 2 {
		t.Errorf("--not kept %d of 2 terms; the last one would silently win", len(l))
	}
}
