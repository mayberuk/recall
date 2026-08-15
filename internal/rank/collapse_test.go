package rank_test

import (
	"testing"

	"github.com/mayberuk/recall/internal/rank"
	"github.com/mayberuk/recall/internal/schema"
)

func hitAt(session, uuid string, offset int, kind schema.MatchKind, author schema.Author, text string) schema.Hit {
	return schema.Hit{
		Session: session,
		UUID:    uuid,
		TS:      "2026-08-01T10:00:00Z",
		Tier:    schema.TierConversation,
		Author:  author,
		Offset:  offset,
		Length:  5,
		Match:   kind,
		Terms:   1,
		Text:    text,
	}
}

// Six query terms landing in one long turn used to print as six lines, each a
// window onto the same sentence shifted by a few characters.
func TestOneTurnIsOneLineHoweverOftenItMatched(t *testing.T) {
	hits := []schema.Hit{
		hitAt("s", "u1", 0, schema.MatchWord, schema.AuthorAssistant, "build build build"),
		hitAt("s", "u1", 6, schema.MatchWord, schema.AuthorAssistant, "build build build"),
		hitAt("s", "u1", 12, schema.MatchWord, schema.AuthorAssistant, "build build build"),
		hitAt("s", "u2", 0, schema.MatchWord, schema.AuthorAssistant, "build once"),
	}
	got := rank.Rank(hits, map[string]int{"s": 10}, rank.Concentration)

	s := got.Sessions[0]
	if len(s.Turnwise) != 2 {
		t.Fatalf("%d display lines, want 2 — one per matched turn", len(s.Turnwise))
	}
	if s.Turnwise[0].Occurrences != 3 {
		t.Errorf("first turn reports %d occurrences, want 3", s.Turnwise[0].Occurrences)
	}
	if s.HitCount != 4 {
		t.Errorf("HitCount %d, want 4 — collapsing what is printed must not change what is counted", s.HitCount)
	}
}

// The line a reader acts on should quote a whole word, not the middle of an
// identifier that happened to come first in the turn.
func TestTheBestOccurrenceIsTheOneKept(t *testing.T) {
	hits := []schema.Hit{
		hitAt("s", "u1", 0, schema.MatchInside, schema.AuthorAssistant, "iosbuild and build"),
		hitAt("s", "u1", 13, schema.MatchWord, schema.AuthorAssistant, "iosbuild and build"),
	}
	got := rank.Rank(hits, map[string]int{"s": 10}, rank.Concentration)

	kept := got.Sessions[0].Turnwise[0]
	if kept.Match != schema.MatchWord {
		t.Errorf("kept the %q match, want the whole-word one", kept.Match)
	}
	if kept.Offset != 13 {
		t.Errorf("kept offset %d, want 13", kept.Offset)
	}
}

// 479 of 629 hits on the query that prompted this were an injected skill
// preamble. Nothing is filtered, but the prose has to outrank the boilerplate.
func TestInjectedTextRanksBelowProse(t *testing.T) {
	// Same words, same match, same length: only the author differs, so the
	// comparison cannot pass on anything else.
	const text = "the build number was bumped"
	injected := rank.Signal(hitAt("s", "u1", 0, schema.MatchWord, schema.AuthorSystem, text))
	prose := rank.Signal(hitAt("s", "u2", 0, schema.MatchWord, schema.AuthorAssistant, text))
	if injected >= prose {
		t.Errorf("injected text scores %v against prose at %v", injected, prose)
	}
}

// "no" matching inside "know" is a real occurrence and a poor answer.
func TestAMatchInsideAWordRanksBelowAWholeWord(t *testing.T) {
	const text = "we know the answer"
	inside := rank.Signal(hitAt("s", "u1", 0, schema.MatchInside, schema.AuthorAssistant, text))
	whole := rank.Signal(hitAt("s", "u2", 0, schema.MatchWord, schema.AuthorAssistant, text))
	if inside >= whole {
		t.Errorf("interior match scores %v against a whole word at %v", inside, whole)
	}
}

// A 20 KB compact summary carries most query terms because it carries most
// words. Length has to discount it or a degraded query returns nothing else.
func TestALongTurnRanksBelowAShortOneCarryingTheSameQuery(t *testing.T) {
	long := hitAt("s", "u1", 0, schema.MatchWord, schema.AuthorAssistant, string(make([]byte, 40000)))
	short := hitAt("s", "u2", 0, schema.MatchWord, schema.AuthorAssistant, "the build number was 1817")
	if rank.Signal(long) >= rank.Signal(short) {
		t.Errorf("a 40 KB turn scores %v against a one-line turn at %v", rank.Signal(long), rank.Signal(short))
	}
}

// Taking the first three chronologically is what buried the answer: the
// injected preamble at the top of a session outranked nothing, it came first.
func TestBestKeepsTheMostWorthwhileTurnsInTimeOrder(t *testing.T) {
	matched := []rank.Matched{
		{Hit: hitAt("s", "u1", 0, schema.MatchWord, schema.AuthorSystem, "injected"), Occurrences: 9, Signal: 0.2},
		{Hit: hitAt("s", "u2", 0, schema.MatchWord, schema.AuthorAssistant, "prose"), Occurrences: 1, Signal: 3},
		{Hit: hitAt("s", "u3", 0, schema.MatchWord, schema.AuthorAssistant, "more prose"), Occurrences: 1, Signal: 2},
	}
	got := rank.Best(matched, 2)

	if len(got) != 2 {
		t.Fatalf("kept %d, want 2", len(got))
	}
	if got[0].UUID != "u2" || got[1].UUID != "u3" {
		t.Errorf("kept %s and %s, want u2 and u3 in the order they happened", got[0].UUID, got[1].UUID)
	}
}

func TestBestReturnsEverythingWhenItFits(t *testing.T) {
	matched := []rank.Matched{
		{Hit: hitAt("s", "u1", 0, schema.MatchWord, schema.AuthorAssistant, "a"), Occurrences: 1, Signal: 1},
	}
	if got := rank.Best(matched, 5); len(got) != 1 {
		t.Errorf("kept %d of 1, want 1", len(got))
	}
	if got := rank.Best(matched, 0); got != nil {
		t.Errorf("a zero cap kept %v, want nothing", got)
	}
}
