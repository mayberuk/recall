package render

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mayberuk/recall/internal/schema"
)

func day(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}

// TestCoverageLinesMatchThePinnedFormat holds the two lines to the format
// pinned in docs/design.md's no-false-negatives decision, character for
// character. The wording is the contract, not a presentation choice: a
// caller reads "tool output NOT searched" to know its search was narrow.
func TestCoverageLinesMatchThePinnedFormat(t *testing.T) {
	c := Coverage{
		Sessions:         47,
		SessionsSearched: 40,
		Searched:         []schema.Tier{schema.TierConversation},
		Unsearched:       []schema.Tier{schema.TierInvocation, schema.TierResult},
		LiveFrom:         day("2026-06-10"),
		ArchiveReaches:   true,
		Refreshed:        true,
	}
	want := []string{
		"── 47 sessions · 40 searched · conversation only — tool output NOT searched (--results)",
		"── live to 2026-06-10 · archived before that",
	}
	got := c.Lines()
	if len(got) != len(want) {
		t.Fatalf("got %d lines, want %d: %q", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d\n got %q\nwant %q", i, got[i], want[i])
		}
	}
}

// TestFreshnessNeverClaimsAnArchiveReachThatIsNotThere pins the one honest
// statement relating the two boundaries. They are minima over different sets,
// so "archived before that" is a claim that must be earned.
func TestFreshnessNeverClaimsAnArchiveReachThatIsNotThere(t *testing.T) {
	c := Coverage{Sessions: 1, SessionsSearched: 1, LiveFrom: day("2026-06-10"), Refreshed: true}
	line := c.Lines()[1]
	if want := "── live to 2026-06-10 · nothing archived before that"; line != want {
		t.Errorf("got %q, want %q", line, want)
	}
	if strings.Contains(line, "· archived before that") {
		t.Error("an archive that does not reach before the live window must not say it does")
	}
}

func TestFreshnessDeclaresASkippedRefresh(t *testing.T) {
	c := Coverage{LiveFrom: day("2026-06-10"), ArchiveReaches: true}
	if want := "── live to 2026-06-10 · archived before that · not refreshed (--no-update)"; c.Lines()[1] != want {
		t.Errorf("got %q, want %q", c.Lines()[1], want)
	}
}

func TestTierClauseNamesWhatWasNotSearched(t *testing.T) {
	cases := []struct {
		name       string
		unsearched []schema.Tier
		want       string
	}{
		{"default", []schema.Tier{schema.TierInvocation, schema.TierResult},
			"conversation only — tool output NOT searched (--results)"},
		{"with --tools", []schema.Tier{schema.TierResult},
			"conversation and tool calls — tool output NOT searched (--results)"},
		{"with --results", []schema.Tier{schema.TierInvocation},
			"conversation and tool output — tool calls NOT searched (--tools)"},
		{"everything", nil, "every tier searched"},
		// No pinned two-tier wording fits leaving conversation itself out, so
		// this falls to the generic clause rather than one of the four fixed
		// phrasings above.
		{"an unpinned combination", []schema.Tier{schema.TierConversation},
			"NOT searched: conversation"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := TierClause(tc.unsearched); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestDeclaredLimitsAppearInTheCoverageLine is the rule that keeps a cut list
// from reading like an exhausted corpus. scan emits one hit per occurrence with
// no ceiling, so something has to cut — but it has to say so.
func TestDeclaredLimitsAppearInTheCoverageLine(t *testing.T) {
	c := Coverage{
		Sessions: 9, SessionsSearched: 9,
		Unsearched: []schema.Tier{schema.TierInvocation, schema.TierResult},
		LiveFrom:   day("2026-06-10"),
		Refreshed:  true,
		Limits: []Limit{
			{Flag: "--limit", What: "sessions", Shown: 10, Total: 47},
			{Flag: "--hits", What: "hit lines", Shown: 30, Total: 703},
		},
	}
	lines := c.Lines()
	if len(lines) != 4 {
		t.Fatalf("got %d lines, want 4: %q", len(lines), lines)
	}
	if want := "── showing 10 of 47 sessions (--limit)"; lines[2] != want {
		t.Errorf("got %q, want %q", lines[2], want)
	}
	if want := "── showing 30 of 703 hit lines (--hits)"; lines[3] != want {
		t.Errorf("got %q, want %q", lines[3], want)
	}
}

// TestCoverageJSONCarriesBothBoundariesSeparately is the machine half of the
// rule the human half already states: LiveFrom is what cleanup deletes next and
// ContentFrom is how far the words reach, and a caller must be able to read
// them apart. Per-file skew is deliberately not here — it is a doctor
// diagnostic, not a boundary, and it reaches 55 days on the real corpus.
func TestCoverageJSONCarriesBothBoundariesSeparately(t *testing.T) {
	c := Coverage{
		LiveFrom:       day("2026-06-10"),
		ContentFrom:    day("2026-04-16"),
		ContentTo:      day("2026-08-14"),
		ArchiveReaches: true,
	}
	blob, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(blob, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for field, want := range map[string]string{
		"live_from":    "2026-06-10T00:00:00Z",
		"content_from": "2026-04-16T00:00:00Z",
		"content_to":   "2026-08-14T00:00:00Z",
	} {
		if got[field] != want {
			t.Errorf("%s = %v, want %v", field, got[field], want)
		}
	}
	if got["live_from"] == got["content_from"] {
		t.Error("the two boundaries were emitted as one number")
	}
	if _, found := got["max_file_skew"]; found {
		t.Error("per-file skew is a doctor diagnostic, not a coverage boundary")
	}
}

// TestCoverageNotesAppearAsTheirOwnFooterLines: a Note is a narrowing that is
// not a count, so it belongs beside the Limits lines, not folded into one of
// the two pinned header lines.
func TestCoverageNotesAppearAsTheirOwnFooterLines(t *testing.T) {
	c := Coverage{
		Sessions: 1, SessionsSearched: 1, LiveFrom: day("2026-06-10"), Refreshed: true,
		Notes: []string{"one session's turn count could not be read"},
	}
	lines := c.Lines()
	if want := "── one session's turn count could not be read"; lines[len(lines)-1] != want {
		t.Errorf("last line = %q, want the note %q", lines[len(lines)-1], want)
	}
}
