package render

import (
	"strings"
	"testing"

	"github.com/mayberuk/recall/internal/schema"
)

func samplePassage() Passage {
	return Passage{
		Session: "b5ddc1af-36ec-4345-8f75-9ccd3c827bd3",
		UUID:    "6f1e9f0a-40a6-4b0e-9f6f-1b6b9f6f6f6f",
		Cite:    "b5ddc1af/6f1e9f0a",
		TS:      "2026-08-13T10:01:00Z",
		Repo:    "gitlab.example/acme/mobile",
		Branch:  "staging",
		Tier:    schema.TierConversation,
		Author:  schema.AuthorAssistant,
		Text:    "agvtool reads it\nfrom the pbxproj",
	}
}

func sampleTurns() Turns {
	return Turns{
		Verb:    "turns",
		Query:   "agvtool",
		Matched: 1,
		Passages: []Passage{
			samplePassage(),
		},
		Coverage: sampleCoverage(),
	}
}

// TestTurnsTextNamesTheMatchCountAndPrintsTheWords is the contract that makes
// `turns` cheaper than a round trip through find and show: the words
// themselves have to be on the page, not just a citation.
func TestTurnsTextNamesTheMatchCountAndPrintsTheWords(t *testing.T) {
	got := string(sampleTurns().Text())
	for _, want := range []string{
		`1 of 1 matched turn for "agvtool"`,
		"b5ddc1af/6f1e9f0a",
		"agvtool reads it",
		"from the pbxproj",
		"── 2 sessions · 2 searched · conversation only",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output is missing %q:\n%s", want, got)
		}
	}
}

// TestTurnsTextOnAMissOffersTheNearbyTerms mirrors find's miss report: a dead
// end has to hand back the next query, not just a bare zero.
func TestTurnsTextOnAMissOffersTheNearbyTerms(t *testing.T) {
	tu := Turns{
		Verb: "turns", Query: "retry",
		Terms:    []Term{{Term: "retry", Nearby: []string{"retries"}}},
		Coverage: sampleCoverage(),
	}
	got := string(tu.Text())
	for _, want := range []string{`no turns carry "retry"`, "nearby: retries"} {
		if !strings.Contains(got, want) {
			t.Errorf("output is missing %q:\n%s", want, got)
		}
	}
}

func TestTurnsTruncatedPassageNamesTheCommandToSeeTheRest(t *testing.T) {
	p := samplePassage()
	p.Truncated, p.Length = true, 4096
	tu := Turns{Verb: "turns", Query: "agvtool", Matched: 1, Passages: []Passage{p}, Coverage: sampleCoverage()}
	got := string(tu.Text())
	if want := "… 4096 bytes in this turn; `recall show b5ddc1af --turn 6f1e9f0a-40a6-4b0e-9f6f-1b6b9f6f6f6f` for all of it"; !strings.Contains(got, want) {
		t.Errorf("output does not name the way to see the whole turn:\n%s\nwant substring: %s", got, want)
	}
}

// TestTurnsBriefKeepsCitationsAndDropsWords is the cheap triage form: a
// citation is enough to pick a passage without paying for its text.
func TestTurnsBriefKeepsCitationsAndDropsWords(t *testing.T) {
	got := string(sampleTurns().Brief())
	if strings.Contains(got, "agvtool reads it") {
		t.Errorf("Brief printed the passage text:\n%s", got)
	}
	if !strings.Contains(got, "b5ddc1af/6f1e9f0a") {
		t.Errorf("Brief dropped the citation it exists to keep:\n%s", got)
	}
	if !strings.Contains(got, "2026-08-13") {
		t.Errorf("Brief dropped the date:\n%s", got)
	}
}

func TestTurnsIDsAreCitationsOnePerLine(t *testing.T) {
	got := string(sampleTurns().IDs())
	if got != "b5ddc1af/6f1e9f0a\n" {
		t.Errorf("IDs() = %q, want one bare citation per line", got)
	}
}

// TestTurnsJSONLEndsWithACoverageRecordThatCountsDistinctSessions checks the
// session tally is deduplicated: two passages from the same session must not
// double-count it.
func TestTurnsJSONLEndsWithACoverageRecordThatCountsDistinctSessions(t *testing.T) {
	p2 := samplePassage()
	p2.UUID, p2.Cite = "other-uuid", "b5ddc1af/other-uuid"
	tu := Turns{Verb: "turns", Query: "agvtool", Hits: 2, Passages: []Passage{samplePassage(), p2}, Coverage: sampleCoverage()}

	blob, err := tu.JSONL()
	if err != nil {
		t.Fatalf("JSONL: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(blob), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 2 passages and 1 coverage record:\n%s", len(lines), blob)
	}
	for i, l := range lines[:2] {
		if !strings.HasPrefix(l, `{"type":"passage"`) {
			t.Errorf("line %d is not a passage record: %s", i+1, l)
		}
	}
	if !strings.Contains(lines[2], `"sessions":1`) {
		t.Errorf("both passages share a session, so the coverage record must count it once: %s", lines[2])
	}
}

// TestPassageWhoNamesTheAgentAndTheNonConversationTier covers both
// qualifiers who() adds beyond the bare author.
func TestPassageWhoNamesTheAgentAndTheNonConversationTier(t *testing.T) {
	p := samplePassage()
	p.Agent = "review-thread-claude"
	p.Tier = schema.TierResult
	if got := p.who(); got != "assistant/review-thread-claude [result]" {
		t.Errorf("who() = %q, want %q", got, "assistant/review-thread-claude [result]")
	}
}

func TestPassageRepeatIsBlankForASingleOccurrence(t *testing.T) {
	if got := samplePassage().repeat(); got != "" {
		t.Errorf("repeat() = %q, want empty for a single occurrence", got)
	}
	p := samplePassage()
	p.Occurrences = 4
	if got := p.repeat(); got != "×4" {
		t.Errorf("repeat() = %q, want %q", got, "×4")
	}
}

func TestShortKeepsShortIDsWhole(t *testing.T) {
	if got := short("b5ddc1af"); got != "b5ddc1af" {
		t.Errorf("short(8 chars) = %q, want it unchanged", got)
	}
	if got := short("b5ddc1af-36ec-4345"); got != "b5ddc1af" {
		t.Errorf("short(long id) = %q, want the first 8 chars", got)
	}
}
