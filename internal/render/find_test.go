package render

import (
	"strings"
	"testing"

	"github.com/mayberuk/recall/internal/schema"
)

func sampleCoverage() Coverage {
	return Coverage{
		Sessions:         2,
		SessionsSearched: 2,
		Searched:         []schema.Tier{schema.TierConversation},
		Unsearched:       []schema.Tier{schema.TierInvocation, schema.TierResult},
		LiveFrom:         day("2026-06-10"),
		ArchiveReaches:   true,
		Refreshed:        true,
	}
}

func sampleFind() Find {
	return Find{
		Verb:  "find",
		Query: "agvtool",
		Scope: Scope{Repo: "gitlab.example/acme/mobile"},
		Hits:  4,
		Sessions: []Session{
			{
				ID: "b5ddc1af-36ec-4345-8f75-9ccd3c827bd3", Repo: "gitlab.example/acme/mobile",
				Branch: "staging", Hits: 3, Turns: 119, TurnsKnown: true,
				First: "2026-07-21", Last: "2026-08-13",
				Shown: []Hit{{Author: schema.AuthorAssistant, Tier: schema.TierConversation, Snippet: "…ran agvtool what-version…"}},
			},
			{
				ID: "b16d73cc-d8c1-4af1-b2cd-14adbf2298b5", Repo: "gitlab.example/acme/mobile",
				Hits: 1, Turns: 64, TurnsKnown: true, First: "2026-08-14", Last: "2026-08-14",
				Shown: []Hit{{Author: schema.AuthorAgent, Tier: schema.TierConversation, Snippet: "…agvtool ships with Xcode…"}},
			},
		},
		Coverage: sampleCoverage(),
	}
}

// TestFindTextAlwaysEndsWithTheCoverageLine holds the coverage contract: a
// command that searches without emitting the coverage line is a defect, so this
// holds for a result set and a miss alike.
func TestFindTextAlwaysEndsWithTheCoverageLine(t *testing.T) {
	for _, tc := range []struct {
		name string
		find Find
	}{
		{"with results", sampleFind()},
		{"with nothing found", Find{Verb: "find", Query: "nothing", Coverage: sampleCoverage()}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := string(tc.find.Text())
			for _, want := range []string{
				"── 2 sessions · 2 searched · conversation only — tool output NOT searched (--results)",
				"── live to 2026-06-10 · archived before that",
			} {
				if !strings.Contains(got, want) {
					t.Errorf("output is missing the coverage line %q:\n%s", want, got)
				}
			}
			if !strings.Contains(got, "tool output NOT searched (--results)") {
				t.Errorf("output does not declare the unsearched tier:\n%s", got)
			}
		})
	}
}

func TestFindTextNamesEverySessionAndItsCounts(t *testing.T) {
	got := string(sampleFind().Text())
	for _, want := range []string{
		"b5ddc1af-36ec-4345-8f75-9ccd3c827bd3",
		"b16d73cc-d8c1-4af1-b2cd-14adbf2298b5",
		"3 hits of 119 turns",
		"2026-07-21..2026-08-13",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output is missing %q:\n%s", want, got)
		}
	}
}

// TestMissReportsWhatExistsElsewhere is acceptance case a5's output half:
// reporting a bare zero when the answer sits in another checkout is the
// failure the tool exists to invert.
func TestMissReportsWhatExistsElsewhere(t *testing.T) {
	f := Find{
		Verb: "find", Query: "agvtool",
		Scope:     Scope{Repo: "gitlab.example/acme/tooling"},
		Elsewhere: []Elsewhere{{Repo: "gitlab.example/acme/mobile", Hits: 28, Sessions: 2}},
		Coverage:  sampleCoverage(),
	}
	got := string(f.Text())
	for _, want := range []string{
		`no hits for "agvtool" in gitlab.example/acme/tooling`,
		"found elsewhere: 28 hits in 1 other repo",
		"gitlab.example/acme/mobile",
		"run: recall find agvtool --all",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output is missing %q:\n%s", want, got)
		}
	}
}

func TestMissWithNoHitsAnywhereOffersTheNearbyTerms(t *testing.T) {
	f := Find{
		Verb: "find", Query: "retry",
		Terms:    []Term{{Term: "retry", Nearby: []string{"retries", "retried"}}},
		Coverage: sampleCoverage(),
	}
	got := string(f.Text())
	if !strings.Contains(got, "no turn carries it; nearby: retries, retried") {
		t.Errorf("a dead end was reported without a next query:\n%s", got)
	}
}

func TestMissQuotesAMultiWordQueryForThePastedCommand(t *testing.T) {
	f := Find{
		Verb: "find", Query: "build number",
		Elsewhere: []Elsewhere{{Repo: "acme/mobile", Hits: 2, Sessions: 1}},
		Coverage:  sampleCoverage(),
	}
	if want := "run: recall find 'build number' --all"; !strings.Contains(string(f.Text()), want) {
		t.Errorf("suggested command is not pasteable, want %q:\n%s", want, f.Text())
	}
}

// TestFZFRecordShape holds the interactive contract: field 1 is the bare
// session id, because --with-nth can only hide it if it is a field and
// --preview '{1}' plus every key binding resolve it the same way.
func TestFZFRecordShape(t *testing.T) {
	f := sampleFind()
	records, note := f.FZF()
	if note != nil {
		t.Errorf("a search with sessions wrote a note instead of records: %q", note)
	}
	body := string(records)
	if !strings.HasSuffix(body, "\x00") {
		t.Error("records are terminated by NUL, not separated; the last one has no terminator")
	}

	parts := strings.Split(strings.TrimSuffix(body, "\x00"), "\x00")
	if len(parts) != len(f.Sessions) {
		t.Fatalf("got %d records, want one per session (%d)", len(parts), len(f.Sessions))
	}
	for i, rec := range parts {
		fields := strings.SplitN(rec, "\x1f", 2)
		if len(fields) != 2 {
			t.Fatalf("record %d has no field separator: %q", i, rec)
		}
		if fields[0] != f.Sessions[i].ID {
			t.Errorf("record %d field 1 = %q, want the bare session id %q", i, fields[0], f.Sessions[i].ID)
		}
		if strings.ContainsAny(fields[0], " \t\n") {
			t.Errorf("record %d field 1 carries whitespace, so fzf cannot address it: %q", i, fields[0])
		}
		// Checked against the input, not against Block(): asserting through
		// the renderer would let a mutation move the expectation with it.
		want := f.Sessions[i]
		for _, fragment := range []string{want.ID, want.Repo, want.Shown[0].Snippet} {
			if !strings.Contains(fields[1], fragment) {
				t.Errorf("record %d field 2 is missing %q:\n%s", i, fragment, fields[1])
			}
		}
	}

	last := parts[len(parts)-1]
	for _, want := range []string{
		"── 2 sessions · 2 searched · conversation only — tool output NOT searched (--results)",
		"── live to 2026-06-10 · archived before that",
	} {
		if !strings.Contains(last, want) {
			t.Errorf("coverage line %q is missing from the last record's field 2:\n%s", want, last)
		}
	}
	for i, rec := range parts[:len(parts)-1] {
		if strings.Contains(rec, "── ") {
			t.Errorf("record %d carries coverage text; it belongs in the last record only", i)
		}
	}
}

// TestFZFOrderIsRecallsOwn matters because fzf --filter with a non-empty query
// re-ranks by its own fuzzy score. The stream has to arrive already ordered.
func TestFZFOrderIsRecallsOwn(t *testing.T) {
	f := sampleFind()
	records, _ := f.FZF()
	body := strings.TrimSuffix(string(records), "\x00")
	for i, rec := range strings.Split(body, "\x00") {
		if id := strings.SplitN(rec, "\x1f", 2)[0]; id != f.Sessions[i].ID {
			t.Errorf("record %d is session %s, want %s", i, id, f.Sessions[i].ID)
		}
	}
}

// TestWriteTermsNamesEveryOutcomeADeadEndTermCanHave covers all three shapes a
// term's fate can take: carried by turns that missed the rest of the query,
// close to something in the corpus, or nowhere near anything at all.
func TestWriteTermsNamesEveryOutcomeADeadEndTermCanHave(t *testing.T) {
	var b strings.Builder
	writeTerms(&b, []Term{
		{Term: "bitrise", Turns: 4},
		{Term: "retry", Nearby: []string{"retries"}},
		{Term: "quixotic"},
	})
	got := b.String()
	for _, want := range []string{
		"bitrise              4 turns carry it, but not together with the rest",
		"retry                no turn carries it; nearby: retries",
		"quixotic             no turn carries it, and nothing in the corpus is close",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output is missing %q:\n%s", want, got)
		}
	}
}

// TestDateRangeNamesEveryCombinationOfBoundaries pins the four shapes a
// session's first/last pair can take, direct against the doc comment rather
// than through a fixture that could hide which branch actually ran.
func TestDateRangeNamesEveryCombinationOfBoundaries(t *testing.T) {
	cases := []struct {
		name        string
		first, last string
		want        string
	}{
		{"no first", "", "2026-08-01", "2026-08-01"},
		{"no last", "2026-07-01", "", "2026-07-01"},
		{"same day both ends", "2026-08-01", "2026-08-01", "2026-08-01"},
		{"a real range", "2026-07-01", "2026-08-01", "2026-07-01..2026-08-01"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := dateRange(tc.first, tc.last); got != tc.want {
				t.Errorf("dateRange(%q, %q) = %q, want %q", tc.first, tc.last, got, tc.want)
			}
		})
	}
}

func TestAgentNoteNamesTheCountOnlyWhenThereIsOne(t *testing.T) {
	if got := agentNote(0); got != "" {
		t.Errorf("agentNote(0) = %q, want empty — a zero count is not a narrowing", got)
	}
	if got := agentNote(3); got != "3 from subagents" {
		t.Errorf("agentNote(3) = %q, want %q", got, "3 from subagents")
	}
}

// TestTallyStatesTurnCountUnknownRatherThanZero is why the three numbers are
// never conflated: a session whose turn count could not be read must not be
// reported as a zero-turn session.
func TestTallyStatesTurnCountUnknownRatherThanZero(t *testing.T) {
	s := Session{Hits: 2, TurnsKnown: false}
	if got := s.tally(); got != "2 hits of turn count unknown" {
		t.Errorf("tally() = %q, want %q", got, "2 hits of turn count unknown")
	}
}

// TestTallySeparatesHitsFromTheTurnsTheySitIn is the reason tally exists: hits,
// hit turns and session length answer different questions and conflating them
// read as confusing.
func TestTallySeparatesHitsFromTheTurnsTheySitIn(t *testing.T) {
	s := Session{Hits: 5, HitTurns: 2, Turns: 40, TurnsKnown: true}
	if got := s.tally(); got != "5 hits in 2 turns of 40 turns" {
		t.Errorf("tally() = %q, want %q", got, "5 hits in 2 turns of 40 turns")
	}
}

// TestHitWriteTagsByTierWhenItIsNotConversation is the same rule show and
// turns both hold: a tool call or its output is labelled by tier because the
// author alone would misreport it as ordinary conversation.
func TestHitWriteTagsByTierWhenItIsNotConversation(t *testing.T) {
	var b strings.Builder
	Hit{Author: schema.AuthorAssistant, Tier: schema.TierResult, Snippet: "build succeeded", Occurrences: 3}.write(&b)
	got := b.String()
	if !strings.Contains(got, "result") {
		t.Errorf("a result-tier hit was tagged by author instead of tier: %q", got)
	}
	if strings.Contains(got, "assistant") {
		t.Errorf("a non-conversation hit still carried the author tag: %q", got)
	}
	if !strings.Contains(got, "×3") {
		t.Errorf("repeated occurrences were not reported: %q", got)
	}
}

func TestFindBriefDropsSnippetsButKeepsSessionsAndCoverage(t *testing.T) {
	got := string(findFixture().Brief())
	if strings.Contains(got, "snippet one") || strings.Contains(got, "snippet two") {
		t.Errorf("Brief printed a snippet:\n%s", got)
	}
	if !strings.Contains(got, "b5ddc1af") || !strings.Contains(got, "b16d73cc") {
		t.Errorf("Brief dropped a session it exists to list:\n%s", got)
	}
}

// TestFZFMissGoesToTheNoteNotAFakeRecord keeps stdout to records alone: a
// record whose field 1 is not a session id would break every key binding.
func TestFZFMissGoesToTheNoteNotAFakeRecord(t *testing.T) {
	f := Find{
		Verb: "find", Query: "agvtool",
		Scope:     Scope{Repo: "acme/tooling"},
		Elsewhere: []Elsewhere{{Repo: "acme/mobile", Hits: 3, Sessions: 1}},
		Coverage:  sampleCoverage(),
	}
	records, note := f.FZF()
	if len(records) != 0 {
		t.Errorf("a search with no sessions wrote %d bytes to the record stream: %q", len(records), records)
	}
	body := string(note)
	if !strings.Contains(body, "found elsewhere") {
		t.Errorf("the note lost the elsewhere report:\n%s", body)
	}
	for _, want := range []string{
		"── 2 sessions · 2 searched · conversation only — tool output NOT searched (--results)",
		"── live to 2026-06-10 · archived before that",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the note lost the coverage line %q:\n%s", want, body)
		}
	}
}
