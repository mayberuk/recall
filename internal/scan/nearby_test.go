package scan

import (
	"strings"
	"testing"

	"github.com/mayberuk/recall/internal/fixtures"
	"github.com/mayberuk/recall/internal/schema"
)

func report(t *testing.T, res Result, term string) TermReport {
	t.Helper()
	for _, r := range res.Terms {
		if r.Term == term {
			return r
		}
	}
	t.Fatalf("no report for %q in %v", term, res.Terms)
	return TermReport{}
}

func names(terms []Term) []string {
	out := make([]string, len(terms))
	for i, term := range terms {
		out[i] = term.Text
	}
	return out
}

// The miss path costs more than the hit path, so it must not run on a hit.
func TestNoTermReportWhenSomethingMatched(t *testing.T) {
	turns := []schema.Turn{turn("s1", schema.TierConversation, "the wallet button")}
	if res := Search(turns, Query{Text: "wallet"}); len(res.Terms) != 0 {
		t.Errorf("%d term reports on a hit, want 0", len(res.Terms))
	}
}

// A typo is the case this exists for: nothing came back, and the corpus does
// carry a word one edit away.
func TestATypoIsAnsweredWithTheWordTheCorpusCarries(t *testing.T) {
	turns, _ := corpus(t)

	typo := "quixotrupe"
	res := Search(turns, Query{Text: typo})
	if len(res.Hits) != 0 {
		t.Fatalf("%q matched %d turns, so it cannot pin the miss path", typo, len(res.Hits))
	}

	got := report(t, res, typo)
	if got.Turns != 0 {
		t.Errorf("%q reported %d turns, want 0", typo, got.Turns)
	}
	if len(got.Nearby) == 0 {
		t.Fatalf("%q produced no suggestion", typo)
	}
	if got.Nearby[0].Text != fixtures.NeedleConversation {
		t.Errorf("nearest term %q, want %q (suggestions: %v)",
			got.Nearby[0].Text, fixtures.NeedleConversation, names(got.Nearby))
	}
	if got.Nearby[0].Distance != 1 {
		t.Errorf("distance %d, want 1", got.Nearby[0].Distance)
	}
	if got.Nearby[0].Count != 1 {
		t.Errorf("count %d, want 1 — the token is planted once", got.Nearby[0].Count)
	}
}

// Every term exists, no turn carries them together. Under --all-terms there is
// nothing to suggest and the counts are the whole answer: each term is a query
// that would work. Without it the search relaxes and returns those turns
// instead, which is the same information already answered.
func TestTermsThatNeverCoOccurReportCountsAndNoSuggestions(t *testing.T) {
	turns := []schema.Turn{
		turn("s1", schema.TierConversation, "alpha was decided here"),
		turn("s1", schema.TierConversation, "bravo was decided here"),
		turn("s1", schema.TierConversation, "bravo again"),
	}
	if relaxed := Search(turns, Query{Text: "alpha bravo"}); len(relaxed.Hits) != 3 {
		t.Fatalf("relaxed search returned %d hits, want 3 — one per turn carrying either term", len(relaxed.Hits))
	}
	res := Search(turns, Query{Text: "alpha bravo", AllTerms: true})
	if len(res.Hits) != 0 {
		t.Fatalf("%d hits, want 0", len(res.Hits))
	}

	alpha, bravo := report(t, res, "alpha"), report(t, res, "bravo")
	if alpha.Turns != 1 || bravo.Turns != 2 {
		t.Errorf("turns alpha=%d bravo=%d, want 1 and 2", alpha.Turns, bravo.Turns)
	}
	if len(alpha.Nearby) != 0 || len(bravo.Nearby) != 0 {
		t.Errorf("suggestions offered for terms the corpus carries: %v %v",
			names(alpha.Nearby), names(bravo.Nearby))
	}
}

// The family rule, not the edit-distance rule: "retries" is three edits from
// "retry". Exact keeps the stem from bridging the two on the hit path, which is
// the same expansion measured elsewhere as buying nearly nothing.
func TestAWordFamilyIsSuggestedThoughItIsThreeEditsAway(t *testing.T) {
	turns := []schema.Turn{
		turn("s1", schema.TierConversation, "the retries were replayed twice"),
	}
	res := Search(turns, Query{Text: "retry", Exact: true})
	if len(res.Hits) != 0 {
		t.Fatalf("%d hits, want 0", len(res.Hits))
	}

	got := report(t, res, "retry")
	if len(got.Nearby) == 0 || got.Nearby[0].Text != "retries" {
		t.Fatalf("suggestions %v, want retries first", names(got.Nearby))
	}
	if got.Nearby[0].Distance != 3 {
		t.Errorf("distance %d, want 3", got.Nearby[0].Distance)
	}
	if expanded := Search(turns, Query{Text: "retry"}); len(expanded.Hits) != 1 {
		t.Errorf("stem expansion returned %d hits for the same query, want 1", len(expanded.Hits))
	}
}

// A suggestion drawn from a tier nobody searched would send the caller after a
// word this search could not have found.
func TestSuggestionsComeOnlyFromTheSearchedTiers(t *testing.T) {
	turns := []schema.Turn{
		turn("s1", schema.TierConversation, "the settlement batch"),
		turn("s1", schema.TierResult, "the settlemant batch"),
	}
	res := Search(turns, Query{Text: "settlemint"})
	got := report(t, res, "settlemint")
	for _, term := range got.Nearby {
		if term.Text == "settlemant" {
			t.Errorf("suggested %q, which lives only in the unsearched result tier", term.Text)
		}
	}
	if len(got.Nearby) == 0 || got.Nearby[0].Text != "settlement" {
		t.Errorf("suggestions %v, want settlement", names(got.Nearby))
	}

	withResults := Search(turns, Query{Text: "settlemint", Tiers: Tiers(true, false)})
	if len(names(report(t, withResults, "settlemint").Nearby)) != 2 {
		t.Errorf("with the result tier searched, suggestions are %v, want both spellings",
			names(report(t, withResults, "settlemint").Nearby))
	}
}

// Nearest first, then the term the corpus uses most: a caller re-queries with
// the first suggestion, so the order is the recommendation.
func TestSuggestionsRankNearestThenMostUsed(t *testing.T) {
	turns := []schema.Turn{
		turn("s1", schema.TierConversation, "cursor cursor cursor cursos curses"),
	}
	res := Search(turns, Query{Text: "curson"})
	got := names(report(t, res, "curson").Nearby)

	want := []string{"cursor", "cursos", "curses"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("suggestions %v, want %v", got, want)
	}
}

func TestNearbyMaxCapsAndDisables(t *testing.T) {
	turns := []schema.Turn{
		turn("s1", schema.TierConversation, "cursor cursos curses cursed"),
	}
	if got := report(t, Search(turns, Query{Text: "curson", NearbyMax: 2}), "curson"); len(got.Nearby) != 2 {
		t.Errorf("%d suggestions with NearbyMax 2, want 2", len(got.Nearby))
	}
	if res := Search(turns, Query{Text: "curson", NearbyMax: -1}); len(res.Terms) != 0 {
		t.Errorf("%d term reports with the pass disabled, want 0", len(res.Terms))
	}
}

// Counting is a claim about the corpus and is exhaustive; suggesting is an offer
// and is budgeted. Under a budget that runs out on the first turn, the term in
// the last turn is still counted as present and simply is not offered.
func TestCountsAreExhaustiveAndSuggestionsAreBudgeted(t *testing.T) {
	turns := make([]schema.Turn, 0, 64)
	for i := 0; i < 63; i++ {
		turns = append(turns, turn("s1", schema.TierConversation, "filler text carrying no marker"))
	}
	turns = append(turns, turn("s1", schema.TierConversation, "the settlement batch"))

	full := Search(turns, Query{Text: "settlement settlemint", AllTerms: true})
	if got := report(t, full, "settlement"); got.Turns != 1 {
		t.Errorf("settlement reported %d turns, want 1", got.Turns)
	}
	if got := report(t, full, "settlemint"); len(got.Nearby) == 0 || got.Nearby[0].Text != "settlement" {
		t.Errorf("within budget, suggestions are %v, want settlement", names(got.Nearby))
	}

	restore := nearbyBudget
	nearbyBudget = 1
	defer func() { nearbyBudget = restore }()

	starved := Search(turns, Query{Text: "settlement settlemint", AllTerms: true})
	if got := report(t, starved, "settlement"); got.Turns != 1 {
		t.Errorf("the budget cut the count short: %d turns, want 1", got.Turns)
	}
	if got := report(t, starved, "settlemint"); len(got.Nearby) != 0 {
		t.Errorf("the budget was spent on the first turn but still offered %v", names(got.Nearby))
	}
}
