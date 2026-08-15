package render

import (
	"strings"
	"testing"

	"github.com/mayberuk/recall/internal/schema"
)

func sampleShow() Show {
	return Show{
		Verb:    "show",
		Session: "b5ddc1af-36ec-4345-8f75-9ccd3c827bd3",
		Repo:    "gitlab.example/acme/mobile",
		Branch:  "staging",
		Query:   "agvtool",
		Anchor:  AnchorQuery,
		Turns:   119,
		Shown:   3,
		Matches: 1,
		Windows: []Window{{
			From: 40, To: 42,
			Turns: []Turn{
				{Index: 40, TS: "2026-08-13T10:00:00Z", Tier: schema.TierConversation, Author: schema.AuthorHuman, Text: "why is the build number wrong"},
				{Index: 41, TS: "2026-08-13T10:01:00Z", Tier: schema.TierConversation, Author: schema.AuthorAssistant, Match: true, Text: "agvtool reads it\nfrom the pbxproj"},
				{Index: 42, TS: "2026-08-13T10:02:00Z", Tier: schema.TierConversation, Author: schema.AuthorHuman, Text: "that explains it"},
			},
		}},
		Coverage: Coverage{
			Sessions: 1, SessionsSearched: 1,
			Searched:   []schema.Tier{schema.TierConversation},
			Unsearched: []schema.Tier{schema.TierInvocation, schema.TierResult},
			LiveFrom:   day("2026-06-10"), ArchiveReaches: true, Refreshed: true,
			Limits: []Limit{{Flag: "--around", What: "turns", Shown: 3, Total: 119}},
		},
	}
}

// TestShowEmitsTheCoverageLine holds show to the same contract as find: it
// searches when it is given a query, so it declares what it covered.
func TestShowEmitsTheCoverageLine(t *testing.T) {
	got := string(sampleShow().Text())
	for _, want := range []string{
		"── 1 session · 1 searched · conversation only — tool output NOT searched (--results)",
		"── live to 2026-06-10 · archived before that",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output is missing the coverage line %q:\n%s", want, got)
		}
	}
}

// TestShowDeclaresThatItReturnedAWindowNotTheSession is the decision from
// docs/design.md: a whole-session fetch is the multi-megabyte lookup the
// requirements rule out, so the window and the session's real size are both
// stated.
func TestShowDeclaresThatItReturnedAWindowNotTheSession(t *testing.T) {
	got := string(sampleShow().Text())
	if !strings.Contains(got, "── showing 3 of 119 turns (--around)") {
		t.Errorf("output does not declare that it returned a window:\n%s", got)
	}
	if !strings.Contains(got, "turns 41-43 of 119") {
		t.Errorf("output does not locate the window inside the session:\n%s", got)
	}
}

func TestShowPrintsTurnTextInFullAndMarksTheMatch(t *testing.T) {
	got := string(sampleShow().Text())
	if !strings.Contains(got, "from the pbxproj") {
		t.Errorf("a turn's second line was dropped:\n%s", got)
	}
	var marked int
	for _, line := range strings.Split(got, "\n") {
		if strings.HasPrefix(line, "> ") {
			marked++
		}
	}
	if marked != 1 {
		t.Errorf("marked %d turns as the match, want 1:\n%s", marked, got)
	}
}

func TestShowSaysWhyItPickedTheEndOfTheSession(t *testing.T) {
	s := sampleShow()
	s.Query, s.Anchor = "", AnchorTail
	if got := string(s.Text()); !strings.Contains(got, "no query given, so this is the end of the session") {
		t.Errorf("the tail window was not explained:\n%s", got)
	}
}

// TestShowTurnCountFallsBackToConversationWhenNoTierWasDeclared keeps the
// count from printing an empty parenthesis when the caller didn't set Tiers.
func TestShowTurnCountFallsBackToConversationWhenNoTierWasDeclared(t *testing.T) {
	s := sampleShow()
	s.Tiers = nil
	if got := s.turnCount(); !strings.Contains(got, "(conversation)") {
		t.Errorf("turnCount() = %q, want it to name conversation as the default tier", got)
	}
}

func TestShowTurnCountNamesEveryDeclaredTier(t *testing.T) {
	s := sampleShow()
	s.Tiers = []schema.Tier{schema.TierConversation, schema.TierInvocation}
	if got := s.turnCount(); !strings.Contains(got, "(conversation + invocation)") {
		t.Errorf("turnCount() = %q, want both declared tiers joined", got)
	}
}

// TestTurnWriteTagsAgentAndNonConversationTiers covers the two qualifiers a
// printed turn can carry beyond its bare author.
func TestTurnWriteTagsAgentAndNonConversationTiers(t *testing.T) {
	var b strings.Builder
	Turn{TS: "2026-08-13T10:00:00Z", Author: schema.AuthorAssistant, Agent: "review-thread-claude", Tier: schema.TierResult, Text: "ok"}.write(&b)
	got := b.String()
	if !strings.Contains(got, "assistant/review-thread-claude") {
		t.Errorf("the agent name was dropped: %q", got)
	}
	if !strings.Contains(got, "[result]") {
		t.Errorf("the non-conversation tier was not tagged: %q", got)
	}
}

func TestTurnWriteNamesTheCommandToSeeATruncatedTurnInFull(t *testing.T) {
	var b strings.Builder
	Turn{TS: "2026-08-13T10:00:00Z", Author: schema.AuthorHuman, Text: "partial", Truncated: true, Length: 9000}.write(&b)
	if want := "… 9000 bytes in this turn; --chars 0 for all of it"; !strings.Contains(b.String(), want) {
		t.Errorf("output is missing %q:\n%s", want, b.String())
	}
}

// TestShowJSONLPrintsOnePrintedTurnPerLineEndingInCoverage mirrors find and
// turns: a caller streams show's output too, without a JSON parser buffering
// the whole answer first.
func TestShowJSONLPrintsOnePrintedTurnPerLineEndingInCoverage(t *testing.T) {
	blob, err := sampleShow().JSONL()
	if err != nil {
		t.Fatalf("JSONL: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(blob), "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("got %d lines, want 3 turns and 1 coverage record:\n%s", len(lines), blob)
	}
	for i, l := range lines[:3] {
		if !strings.HasPrefix(l, `{"type":"turn"`) {
			t.Errorf("line %d is not a turn record: %s", i+1, l)
		}
	}
	if !strings.HasPrefix(lines[3], `{"type":"coverage"`) {
		t.Errorf("last line is not the coverage record: %s", lines[3])
	}
}
