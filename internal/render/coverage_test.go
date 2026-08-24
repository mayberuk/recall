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

// TestCoverageLinesMatchThePinnedFormat holds the two lines to their pinned
// format, character for character. The wording is the contract, not a
// presentation choice: the no-false-negatives guarantee is absolute only over the
// tier that was searched, and a caller reads "tool output NOT searched" to know
// its search was narrow.
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
		LiveFromAt:     Stamp(day("2026-06-10")),
		ContentFromAt:  Stamp(day("2026-04-16")),
		ContentToAt:    Stamp(day("2026-08-14")),
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

// TestStatsLineSegments covers every combination of the optional segments: a
// zero-byte search still reports, lines and words are each gated on their own
// Known flag, and passes only shows up once there was more than one.
func TestStatsLineSegments(t *testing.T) {
	cases := []struct {
		name string
		s    Stats
		want string
	}{
		{
			"bytes and turns only",
			Stats{Bytes: 50_000_000, Turns: 61384, ElapsedMS: 27},
			"── scanned 47.7 MB · 61384 turns · 27 ms",
		},
		{
			"lines known, words not",
			Stats{Bytes: 50_000_000, Lines: 1204831, LinesKnown: true, Turns: 61384, ElapsedMS: 27},
			"── scanned 47.7 MB · 1204831 lines · 61384 turns · 27 ms",
		},
		{
			"lines and words both known",
			Stats{Bytes: 50_000_000, Lines: 1204831, LinesKnown: true, Words: 9000000, WordsKnown: true, Turns: 61384, ElapsedMS: 27},
			"── scanned 47.7 MB · 1204831 lines · 9000000 words · 61384 turns · 27 ms",
		},
		{
			"passes greater than one",
			Stats{Bytes: 50_000_000, Turns: 61384, Passes: 2, ElapsedMS: 27},
			"── scanned 47.7 MB · 61384 turns · 2 passes · 27 ms",
		},
		{
			"passes of exactly one is omitted",
			Stats{Bytes: 50_000_000, Turns: 61384, Passes: 1, ElapsedMS: 27},
			"── scanned 47.7 MB · 61384 turns · 27 ms",
		},
		{
			"a zero-byte search still prints the line",
			Stats{ElapsedMS: 4},
			"── scanned 0 B · 0 turns · 4 ms",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.s.line(); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestStatsLineIsLastAndDoesNotMoveThePinnedLines checks the stats line's
// position — after the query lines, the limits and the notes — and that the
// two lines the build contract pins stay at positions one and two.
func TestStatsLineIsLastAndDoesNotMoveThePinnedLines(t *testing.T) {
	c := Coverage{
		Sessions: 9, SessionsSearched: 9,
		Unsearched: []schema.Tier{schema.TierInvocation, schema.TierResult},
		LiveFrom:   day("2026-06-10"),
		Refreshed:  true,
		Limits:     []Limit{{Flag: "--limit", What: "sessions", Shown: 10, Total: 47}},
		Notes:      []string{"one session's turn count could not be read"},
		Stats:      &Stats{Bytes: 100, Turns: 5, ElapsedMS: 1},
	}
	lines := c.Lines()
	want := []string{
		"── 9 sessions · 9 searched · conversation only — tool output NOT searched (--results)",
		"── live to 2026-06-10 · nothing archived before that",
		"── showing 10 of 47 sessions (--limit)",
		"── one session's turn count could not be read",
		"── scanned 100 B · 5 turns · 1 ms",
	}
	if len(lines) != len(want) {
		t.Fatalf("got %d lines, want %d: %q", len(lines), len(want), lines)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Errorf("line %d\n got %q\nwant %q", i, lines[i], want[i])
		}
	}
}

// TestNilStatsProducesNoLineAndNoJSONKey is the off switch: RECALL_NO_STATS
// leaves Coverage.Stats nil, and that must be invisible in both the text
// footer and the JSON, not merely an empty section.
func TestNilStatsProducesNoLineAndNoJSONKey(t *testing.T) {
	c := Coverage{Sessions: 1, SessionsSearched: 1, LiveFrom: day("2026-06-10"), Refreshed: true}
	for _, l := range c.Lines() {
		if strings.HasPrefix(l, "── scanned ") {
			t.Errorf("nil Stats produced a stats line: %q", l)
		}
	}
	blob, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(blob), `"stats"`) {
		t.Errorf("nil Stats emitted a stats key: %s", blob)
	}
}

// TestStatsJSONShapeIncludesElapsedMS is the wire contract for the field a
// consumer times a search by: one decimal place, not an integer that rounds
// sub-millisecond work away.
func TestStatsJSONShapeIncludesElapsedMS(t *testing.T) {
	c := Coverage{Stats: &Stats{Bytes: 1024, Lines: 10, LinesKnown: true, Words: 50, WordsKnown: true, Turns: 3, Passes: 2, ElapsedMS: 27.4}}
	blob, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(blob, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	stats, ok := got["stats"].(map[string]any)
	if !ok {
		t.Fatalf("no stats object in %s", blob)
	}
	for field, want := range map[string]any{
		"bytes": float64(1024), "lines": float64(10), "lines_known": true,
		"words": float64(50), "words_known": true, "turns": float64(3),
		"passes": float64(2), "elapsed_ms": 27.4,
	} {
		if stats[field] != want {
			t.Errorf("%s = %v, want %v", field, stats[field], want)
		}
	}
	if !strings.Contains(string(blob), `"elapsed_ms":27.4`) {
		t.Errorf("elapsed_ms did not serialise with its decimal: %s", blob)
	}
}

// TestCoverageWithStatsRoundTrips proves the struct and the wire agree: no
// custom MarshalJSON means an unmarshal, remarshal round trip reproduces both
// the struct and the exact bytes.
func TestCoverageWithStatsRoundTrips(t *testing.T) {
	c := Coverage{
		Sessions: 3, SessionsSearched: 3,
		LiveFrom: day("2026-06-10"), LiveFromAt: Stamp(day("2026-06-10")),
		ArchiveReaches: true,
		Stats:          &Stats{Bytes: 2048, Lines: 40, LinesKnown: true, Words: 300, WordsKnown: true, Turns: 12, Passes: 2, ElapsedMS: 5.6},
	}
	first, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var round Coverage
	if err := json.Unmarshal(first, &round); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	second, err := json.Marshal(round)
	if err != nil {
		t.Fatalf("remarshal: %v", err)
	}
	if string(first) != string(second) {
		t.Errorf("round trip changed the bytes:\n first: %s\nsecond: %s", first, second)
	}
	if round.Stats == nil || *round.Stats != *c.Stats {
		t.Errorf("Stats did not round-trip: got %+v, want %+v", round.Stats, c.Stats)
	}
	if round.LiveFromAt != c.LiveFromAt {
		t.Errorf("LiveFromAt = %q, want %q", round.LiveFromAt, c.LiveFromAt)
	}
}

// TestCoverageJSONMatchesTheBytesTheRemovedMarshalJSONProduced is the proof,
// not an assumption, that dropping the custom MarshalJSON changed nothing:
// each golden string was captured from the pre-removal implementation before
// this change, by marshalling the same fixture.
func TestCoverageJSONMatchesTheBytesTheRemovedMarshalJSONProduced(t *testing.T) {
	cases := []struct {
		name string
		c    Coverage
		want string
	}{
		{"zero", Coverage{},
			`{"sessions":0,"sessions_searched":0,"turns":0,"turns_searched":0,"searched":null,"unsearched":null,"archive_reaches_before_live":false,"refreshed":false,"query":{"required":0,"total":0},"live_from":"","content_from":"","content_to":""}`},
		{"zero-times", Coverage{Sessions: 1, SessionsSearched: 1},
			`{"sessions":1,"sessions_searched":1,"turns":0,"turns_searched":0,"searched":null,"unsearched":null,"archive_reaches_before_live":false,"refreshed":false,"query":{"required":0,"total":0},"live_from":"","content_from":"","content_to":""}`},
		{"boundaries", Coverage{
			LiveFrom: day("2026-06-10"), LiveFromAt: Stamp(day("2026-06-10")),
			ContentFrom: day("2026-04-16"), ContentFromAt: Stamp(day("2026-04-16")),
			ContentTo: day("2026-08-14"), ContentToAt: Stamp(day("2026-08-14")),
			ArchiveReaches: true,
		}, `{"sessions":0,"sessions_searched":0,"turns":0,"turns_searched":0,"searched":null,"unsearched":null,"archive_reaches_before_live":true,"refreshed":false,"query":{"required":0,"total":0},"live_from":"2026-06-10T00:00:00Z","content_from":"2026-04-16T00:00:00Z","content_to":"2026-08-14T00:00:00Z"}`},
		{"full", Coverage{
			Sessions: 47, SessionsSearched: 40, Turns: 900, TurnsSearched: 800,
			Searched:   []schema.Tier{schema.TierConversation},
			Unsearched: []schema.Tier{schema.TierInvocation, schema.TierResult},
			LiveFrom:   day("2026-06-10"), LiveFromAt: Stamp(day("2026-06-10")),
			ContentFrom: day("2026-04-16"), ContentFromAt: Stamp(day("2026-04-16")),
			ContentTo: day("2026-08-14"), ContentToAt: Stamp(day("2026-08-14")),
			ArchiveReaches: true, Refreshed: true, RefreshedAgo: "3 h ago",
			Query:  Query{Terms: []string{"foo", "bar"}, Dropped: []string{"the"}, Excluded: []string{"baz"}, Required: 1, Total: 2, Carried: []string{"foo"}},
			Limits: []Limit{{Flag: "--limit", What: "sessions", Shown: 10, Total: 47}},
			Notes:  []string{"one session's turn count could not be read"},
		}, `{"sessions":47,"sessions_searched":40,"turns":900,"turns_searched":800,"searched":["conversation"],"unsearched":["invocation","result"],"archive_reaches_before_live":true,"refreshed":true,"refreshed_ago":"3 h ago","query":{"terms":["foo","bar"],"dropped":["the"],"excluded":["baz"],"required":1,"total":2,"carried":["foo"]},"limits":[{"flag":"--limit","what":"sessions","shown":10,"total":47}],"notes":["one session's turn count could not be read"],"live_from":"2026-06-10T00:00:00Z","content_from":"2026-04-16T00:00:00Z","content_to":"2026-08-14T00:00:00Z"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := json.Marshal(tc.c)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if string(got) != tc.want {
				t.Errorf("got  %s\nwant %s", got, tc.want)
			}
		})
	}
}
