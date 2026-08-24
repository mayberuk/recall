package main

import (
	"strings"
	"testing"

	"github.com/mayberuk/recall/internal/fixtures"
	"github.com/mayberuk/recall/internal/rank"
	"github.com/mayberuk/recall/internal/render"
	"github.com/mayberuk/recall/internal/schema"
)

// TestResolveSessionReportsAnAmbiguousPrefixWithACappedList is the branch a
// unique-prefix contract needs: two sessions sharing a prefix must be
// reported, not silently resolved to whichever sorts first, and the list is
// capped so an ambiguous one-character prefix does not dump the archive.
func TestResolveSessionReportsAnAmbiguousPrefixWithACappedList(t *testing.T) {
	turns := []schema.Turn{
		{Session: "aaa11111"}, {Session: "aaa22222"}, {Session: "aaa33333"},
		{Session: "aaa44444"}, {Session: "aaa55555"}, {Session: "aaa66666"},
		{Session: "bbb00000"},
	}
	_, err := resolveSession(turns, "aaa")
	if err == nil {
		t.Fatal("an ambiguous prefix resolved silently")
	}
	if !strings.Contains(err.Error(), "matches 6 sessions") {
		t.Errorf("error does not state the count: %v", err)
	}
	if !strings.Contains(err.Error(), "…") {
		t.Errorf("error does not say the list was capped: %v", err)
	}
}

func TestResolveSessionAcceptsAnExactID(t *testing.T) {
	turns := []schema.Turn{{Session: "aaa11111"}, {Session: "aaa22222"}}
	got, err := resolveSession(turns, "aaa22222")
	if err != nil {
		t.Fatalf("resolveSession: %v", err)
	}
	if got != "aaa22222" {
		t.Errorf("got %q, want the exact id even though it is also a prefix of another", got)
	}
}

func TestResolveSessionReportsNoMatchForAnUnknownPrefix(t *testing.T) {
	turns := []schema.Turn{{Session: "aaa11111"}}
	if _, err := resolveSession(turns, "zzz"); err == nil {
		t.Fatal("an unknown prefix was accepted")
	}
}

func TestHeadKeepsShortListsWhole(t *testing.T) {
	ids := []string{"a", "b", "c"}
	got := head(ids, 5)
	if len(got) != 3 {
		t.Errorf("head(3 items, cap 5) = %v, want all 3 unchanged", got)
	}
}

func TestHeadCapsALongListAndSaysSo(t *testing.T) {
	ids := []string{"a", "b", "c", "d", "e", "f"}
	got := head(ids, 3)
	want := []string{"a", "b", "c", "…"}
	if len(got) != len(want) {
		t.Fatalf("head(6 items, cap 3) = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("head()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestSessionTurnsOrdersTiedRecordsByTierRank is the interleaving rule show
// depends on: internal/archive keeps a file per tier, and a record's
// conversation turn must sort before its own tool call and result when the
// timestamp and uuid tie.
func TestSessionTurnsOrdersTiedRecordsByTierRank(t *testing.T) {
	turns := []schema.Turn{
		{Session: "s", UUID: "u1", TS: "same", Tier: schema.TierResult, Text: "result"},
		{Session: "s", UUID: "u1", TS: "same", Tier: schema.TierConversation, Text: "reply"},
		{Session: "s", UUID: "u1", TS: "same", Tier: schema.TierInvocation, Text: "call"},
	}
	got := sessionTurns(turns, "s")
	if len(got) != 3 {
		t.Fatalf("got %d turns, want 3", len(got))
	}
	want := []schema.Tier{schema.TierConversation, schema.TierInvocation, schema.TierResult}
	for i, tier := range want {
		if got[i].Tier != tier {
			t.Errorf("position %d is tier %s, want %s", i, got[i].Tier, tier)
		}
	}
}

func TestTierRankOrdersConversationBeforeInvocationBeforeResult(t *testing.T) {
	if !(tierRank(schema.TierConversation) < tierRank(schema.TierInvocation)) {
		t.Error("conversation does not rank before invocation")
	}
	if !(tierRank(schema.TierInvocation) < tierRank(schema.TierResult)) {
		t.Error("invocation does not rank before result")
	}
}

func TestQuoteAroundLeavesAShortTurnWhole(t *testing.T) {
	text, cut := quoteAround("short text", 0, 5, 0)
	if cut || text != "short text" {
		t.Errorf("quoteAround(chars=0) = (%q, %v), want the text unchanged and uncut", text, cut)
	}
	text, cut = quoteAround("short", 0, 5, 100)
	if cut || text != "short" {
		t.Errorf("quoteAround(text shorter than chars) = (%q, %v), want unchanged and uncut", text, cut)
	}
}

func TestQuoteAroundCutsALongTurnAndSaysSo(t *testing.T) {
	text := strings.Repeat("a ", 200) + "needle" + strings.Repeat(" b", 200)
	got, cut := quoteAround(text, strings.Index(text, "needle"), len("needle"), 40)
	if !cut {
		t.Fatal("a turn longer than the cap was not marked truncated")
	}
	if len(got) >= len(text) {
		t.Errorf("quoteAround did not shorten the text: %d bytes against %d", len(got), len(text))
	}
	if !strings.Contains(got, "needle") {
		t.Errorf("the quote lost the match it is centred on: %q", got)
	}
}

func TestClampBoundsAValueToTheRange(t *testing.T) {
	if got := clamp(-5, 0, 10); got != 0 {
		t.Errorf("clamp(-5, 0, 10) = %d, want 0", got)
	}
	if got := clamp(15, 0, 10); got != 10 {
		t.Errorf("clamp(15, 0, 10) = %d, want 10", got)
	}
	if got := clamp(5, 0, 10); got != 5 {
		t.Errorf("clamp(5, 0, 10) = %d, want 5 (unchanged)", got)
	}
}

func matched(signal float64, occ int, ts string) rank.Matched {
	return rank.Matched{Hit: schema.Hit{TS: ts}, Occurrences: occ, Signal: signal}
}

// TestBestTurnsBreaksTiesInDeclaredOrder pins all four tiebreak levels named
// in the doc comment: a turn's own signal leads, then how often it occurred,
// then the session's own standing, then recency.
func TestBestTurnsBreaksTiesInDeclaredOrder(t *testing.T) {
	sessions := []rank.Session{
		{ID: "low-score", Score: 1, Turnwise: []rank.Matched{matched(5, 1, "2026-08-01T00:00:00Z")}},
		{ID: "high-score", Score: 9, Turnwise: []rank.Matched{matched(5, 1, "2026-08-02T00:00:00Z")}},
		{ID: "top-signal", Score: 1, Turnwise: []rank.Matched{matched(9, 1, "2026-08-01T00:00:00Z")}},
	}
	got := bestTurns(sessions)
	if len(got) != 3 {
		t.Fatalf("got %d turns, want 3", len(got))
	}
	if got[0].Signal != 9 {
		t.Errorf("first turn has signal %v, want the highest (9) to lead regardless of session score", got[0].Signal)
	}
	// Of the two signal-5 turns, the one from the higher-scoring session wins.
	if got[1].TS != "2026-08-02T00:00:00Z" {
		t.Errorf("second turn TS = %s, want the higher-scoring session's turn to come first among ties", got[1].TS)
	}
}

func TestBestTurnsBreaksATieOnOccurrencesThenRecency(t *testing.T) {
	sessions := []rank.Session{
		{ID: "s", Score: 1, Turnwise: []rank.Matched{
			matched(5, 1, "2026-08-01T00:00:00Z"),
			matched(5, 3, "2026-07-01T00:00:00Z"),
		}},
	}
	got := bestTurns(sessions)
	if len(got) != 2 {
		t.Fatalf("got %d turns, want 2", len(got))
	}
	if got[0].Occurrences != 3 {
		t.Errorf("first turn has %d occurrences, want the higher count (3) to lead a signal tie", got[0].Occurrences)
	}
}

// TestWarningsOfNamesEveryDegradationDoctorCanReport covers the three
// warnings alongside TypedLabelsMissing, which the existing render tests
// already exercise through Doctor.Text.
func TestWarningsOfNamesEveryDegradationDoctorCanReport(t *testing.T) {
	d := render.Doctor{
		UnknownTotal: 3,
		Malformed:    2,
		Vanished:     []string{"a.jsonl"},
		Unreadable:   []string{"b.jsonl", "c.jsonl"},
	}
	got := warningsOf(d)
	if len(got) != 4 {
		t.Fatalf("got %d warnings, want one per degradation: %v", len(got), got)
	}
	for _, want := range []string{"3 records carry a type", "2 lines did not parse", "1 transcripts disappeared", "2 transcripts could not be read"} {
		found := false
		for _, w := range got {
			if strings.Contains(w, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("warnings %v missing one containing %q", got, want)
		}
	}
}

func TestWarningsOfIsEmptyOnAHealthyStore(t *testing.T) {
	if got := warningsOf(render.Doctor{}); len(got) != 0 {
		t.Errorf("warningsOf(healthy) = %v, want none", got)
	}
}

// TestShowWordsFlagReportsAWordCountAndItsAbsenceOmitsIt is show's own half of
// the --words contract: newShowCmd binds the flag independently of
// searchFlags, and it has to reach the same scan.Query.CountWords every other
// verb's --words does.
func TestShowWordsFlagReportsAWordCountAndItsAbsenceOmitsIt(t *testing.T) {
	c := harness(t)
	t.Chdir(c.Scratch)

	withWords, err := callShow(t, fixtures.SessNeedle, fixtures.NeedleConversation, "--words")
	if err != nil {
		t.Fatalf("show --words: %v", err)
	}
	if !strings.Contains(withWords, " words") {
		t.Errorf("show --words did not report a word count:\n%s", withWords)
	}

	without, err := callShow(t, fixtures.SessNeedle, fixtures.NeedleConversation)
	if err != nil {
		t.Fatalf("show: %v", err)
	}
	if strings.Contains(without, " words") {
		t.Errorf("show without --words still reported a word count:\n%s", without)
	}
}
