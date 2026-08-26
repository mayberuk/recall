package scan

import (
	"reflect"
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

func TestATypoIsAnsweredWithTheWordTheCorpusCarries(t *testing.T) {
	turns, _ := corpus(t)

	typo := "quixotrupe"
	res := Search(turns, Query{Text: typo})
	if len(res.Hits) == 0 {
		t.Fatalf("%q found nothing, though the corpus carries %q one edit away",
			typo, fixtures.NeedleConversation)
	}
	for _, h := range res.Hits {
		if !strings.Contains(strings.ToLower(h.Text), fixtures.NeedleConversation) {
			t.Errorf("a returned turn carries neither %q nor %q: %q", typo, fixtures.NeedleConversation, h.Text)
		}
	}
	if got, want := res.Match.Expanded, []Expansion{{Term: typo, Variants: []string{fixtures.NeedleConversation}, Distance: 1}}; !reflect.DeepEqual(got, want) {
		t.Errorf("Expanded %+v, want %+v", got, want)
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

// spelledBytes is derived from the fixture text, not read back from a run, so
// the pass-counting tests below stay non-circular.
const (
	spelledText  = "the settlement batch cleared"
	spelledBytes = int64(len(spelledText))
)

func spelledCorpus() []schema.Turn {
	return []schema.Turn{turn("s1", schema.TierConversation, spelledText)}
}

func TestAMissIsReRunWithTheCorpusWordOneEditAway(t *testing.T) {
	res := Search(spelledCorpus(), Query{Text: "settlemint"})

	if len(res.Hits) != 1 {
		t.Fatalf("%d hits, want 1 — the turn carrying the word one edit away", len(res.Hits))
	}
	// The span must locate the substituted word, not the typed term's length.
	h := res.Hits[0]
	if got := h.Text[h.Offset : h.Offset+h.Length]; got != "settlement" {
		t.Errorf("the hit locates %q, want %q", got, "settlement")
	}
	want := []Expansion{{Term: "settlemint", Variants: []string{"settlement"}, Distance: 1}}
	if !reflect.DeepEqual(res.Match.Expanded, want) {
		t.Errorf("Expanded %+v, want %+v", res.Match.Expanded, want)
	}
	if res.Passes != 3 {
		t.Errorf("reported %d passes, want 3 — the walk, the survey, and the re-run", res.Passes)
	}
	if res.BytesScanned != 3*spelledBytes {
		t.Errorf("charged %d bytes, want %d — three readings of the corpus", res.BytesScanned, 3*spelledBytes)
	}
}

// Two edits away is offered, never substituted, unlike the one-edit case above.
func TestATwoEditNeighbourIsSuggestedAndNeverSubstituted(t *testing.T) {
	res := Search(spelledCorpus(), Query{Text: "settlamint"})

	if len(res.Hits) != 0 {
		t.Fatalf("%d hits, want 0 — two edits is often a different word", len(res.Hits))
	}
	if len(res.Match.Expanded) != 0 {
		t.Errorf("Expanded %+v, want nothing substituted", res.Match.Expanded)
	}
	got := report(t, res, "settlamint")
	if len(got.Nearby) == 0 || got.Nearby[0].Text != "settlement" {
		t.Fatalf("suggestions %v, want settlement offered", names(got.Nearby))
	}
	if got.Nearby[0].Distance != 2 {
		t.Fatalf("suggested %q at distance %d, want 2 — the fixture is wrong",
			got.Nearby[0].Text, got.Nearby[0].Distance)
	}
	if res.Passes != 2 {
		t.Errorf("reported %d passes, want 2 — nothing was close enough to re-run for", res.Passes)
	}
}

func TestExactRunsNoExpansion(t *testing.T) {
	res := Search(spelledCorpus(), Query{Text: "settlemint", Exact: true})

	if len(res.Hits) != 0 {
		t.Errorf("%d hits under --exact, want 0", len(res.Hits))
	}
	if len(res.Match.Expanded) != 0 {
		t.Errorf("Expanded %+v under --exact, want nothing", res.Match.Expanded)
	}
	// Offering survives --exact; only substituting does not.
	if got := report(t, res, "settlemint"); len(got.Nearby) == 0 || got.Nearby[0].Text != "settlement" {
		t.Errorf("suggestions %v, want settlement still offered", names(got.Nearby))
	}
	if relaxed := Search(spelledCorpus(), Query{Text: "settlemint"}); len(relaxed.Hits) == 0 {
		t.Error("the same query without --exact found nothing, so --exact is not what suppressed it")
	}
}

// Passes and bytes are asserted together: a no-op pass can't satisfy both.
func TestASearchCarryingEveryTermReadsTheCorpusOnce(t *testing.T) {
	res := Search(spelledCorpus(), Query{Text: "settlement batch"})

	if len(res.Hits) != 2 {
		t.Fatalf("%d hits, want 2 — one per term of the turn carrying both", len(res.Hits))
	}
	if res.Passes != 1 {
		t.Errorf("reported %d passes, want 1", res.Passes)
	}
	if res.BytesScanned != spelledBytes {
		t.Errorf("charged %d bytes, want %d — one reading of the corpus", res.BytesScanned, spelledBytes)
	}
	if len(res.Terms) != 0 || len(res.Match.Expanded) != 0 {
		t.Errorf("a search that found everything produced %d term reports and %d expansions, want none",
			len(res.Terms), len(res.Match.Expanded))
	}
}

func TestARelaxedResultExpandsTheTermNoTurnCarries(t *testing.T) {
	turns := []schema.Turn{
		turn("s1", schema.TierConversation, "the batch was replayed"),
		turn("s2", schema.TierConversation, "the settlement batch cleared"),
	}
	before := Search(turns, Query{Text: "batch settlemint", Exact: true})
	if before.Match.Required != 1 || before.Match.Total != 2 {
		t.Fatalf("without expansion the search carried %d of %d terms, want 1 of 2 — the fixture is wrong",
			before.Match.Required, before.Match.Total)
	}

	res := Search(turns, Query{Text: "batch settlemint"})
	if res.Match.Required != 2 || res.Match.Total != 2 {
		t.Fatalf("carried %d of %d terms, want both", res.Match.Required, res.Match.Total)
	}
	for _, h := range res.Hits {
		if h.Session != "s2" {
			t.Errorf("hit from %s, want only the turn carrying both", h.Session)
		}
	}
	want := []Expansion{{Term: "settlemint", Variants: []string{"settlement"}, Distance: 1}}
	if !reflect.DeepEqual(res.Match.Expanded, want) {
		t.Errorf("Expanded %+v, want %+v", res.Match.Expanded, want)
	}
	if len(res.Terms) != 0 {
		t.Errorf("%d term reports on a search that returned turns, want 0", len(res.Terms))
	}
}

func TestARelaxedResultKeepsAReRunThatCarriesATermNothingDid(t *testing.T) {
	turns := []schema.Turn{
		turn("s1", schema.TierConversation, "the batch was replayed"),
		turn("s2", schema.TierConversation, "the settlement cleared overnight"),
	}
	res := Search(turns, Query{Text: "batch settlemint"})

	if res.Match.Required != 1 {
		t.Fatalf("Required %d, want 1 — no turn carries both words even after the substitution", res.Match.Required)
	}
	want := []string{"batch", "settlemint"}
	if !reflect.DeepEqual(res.Match.Carried, want) {
		t.Errorf("Carried %v, want %v — the term nothing carried is now carried", res.Match.Carried, want)
	}
	sessions := map[string]bool{}
	for _, h := range res.Hits {
		sessions[h.Session] = true
	}
	if !sessions["s1"] || !sessions["s2"] {
		t.Errorf("hits came from %v, want both turns", sessions)
	}
	if len(res.Match.Expanded) != 1 || res.Match.Expanded[0].Term != "settlemint" {
		t.Errorf("Expanded %+v, want the substitution declared", res.Match.Expanded)
	}
}

func TestAReRunThatReachesNothingNewIsDiscardedAndStillCharged(t *testing.T) {
	turns := []schema.Turn{
		turn("s1", schema.TierConversation, "the batch was replayed"),
		turn("s2", schema.TierConversation, "the settlement batch is deprecated"),
	}
	res := Search(turns, Query{Text: "batch settlemint", Not: []string{"deprecated"}})

	if res.Passes != 3 {
		t.Fatalf("reported %d passes, want 3 — the walk, the survey, and a re-run that came to nothing", res.Passes)
	}
	for _, h := range res.Hits {
		if h.Session != "s1" {
			t.Errorf("hit from %s, want only the turn --not left standing", h.Session)
		}
	}
	if len(res.Match.Expanded) != 0 {
		t.Errorf("Expanded %+v, want nothing — the substitution reached no turn", res.Match.Expanded)
	}
	if got := res.Match.Carried; len(got) != 1 || got[0] != "batch" {
		t.Errorf("Carried %v, want [batch] — the answer is the one the first walk produced", got)
	}
}

func TestARelaxedResultCarryingEveryTermIsNotExpanded(t *testing.T) {
	turns := []schema.Turn{
		turn("s1", schema.TierConversation, "alpha was decided here"),
		turn("s2", schema.TierConversation, "bravo was decided here"),
	}
	res := Search(turns, Query{Text: "alpha bravo"})

	if !res.Match.Relaxed() {
		t.Fatalf("carried %d of %d terms, want a relaxed result — the fixture is wrong",
			res.Match.Required, res.Match.Total)
	}
	if len(res.Match.Expanded) != 0 {
		t.Errorf("Expanded %+v, want nothing — every term is carried by some turn", res.Match.Expanded)
	}
	if res.Passes != 1 {
		t.Errorf("reported %d passes, want 1 — nothing was left unanswered to explain", res.Passes)
	}
}

// twoSpellings orders its words opposite to ranking order, so a test can't
// pass by accident.
func twoSpellings() []schema.Turn {
	return []schema.Turn{turn("s1", schema.TierConversation, "cursos and cursor in one turn")}
}

func TestATermStaysOneSlotHoweverManyNeedlesBackIt(t *testing.T) {
	res := Search(twoSpellings(), Query{Text: "curson batch"})

	if res.Match.Total != 2 {
		t.Fatalf("Total %d, want 2 — the query has two words", res.Match.Total)
	}
	if res.Match.Required != 1 {
		t.Errorf("Required %d, want 1 — two spellings of one term are one term carried", res.Match.Required)
	}
	if got := res.Match.Carried; len(got) != 1 || got[0] != "curson" {
		t.Errorf("Carried %v, want [curson] — the term is named as typed, not as substituted", got)
	}
	if len(res.Hits) != 2 {
		t.Fatalf("%d hits, want 2 — one per occurrence of either needle", len(res.Hits))
	}
}

func TestOneTermsSeveralNeedlesAreCollectedInOffsetOrder(t *testing.T) {
	res := Search(twoSpellings(), Query{Text: "curson"})

	want := []span{{offset: 0, length: 6}, {offset: 11, length: 6}}
	if len(res.Hits) != len(want) {
		t.Fatalf("%d hits, want %d", len(res.Hits), len(want))
	}
	for i, w := range want {
		got := res.Hits[i]
		if got.Offset != w.offset || got.Length != w.length {
			t.Errorf("hit %d at offset %d length %d, want %d and %d", i, got.Offset, got.Length, w.offset, w.length)
		}
	}
	if got := res.Hits[0].Text[res.Hits[0].Offset : res.Hits[0].Offset+res.Hits[0].Length]; got != "cursos" {
		t.Errorf("the first hit locates %q, want the earlier spelling %q", got, "cursos")
	}
}

// Candidates are ordered nearest-first, matching what the collector hands in,
// so the cut at four relies on stopping rather than filtering the whole list.
func TestSubstitutionsTakeTheNearestFourAndStopAtTwoEdits(t *testing.T) {
	reports := []TermReport{
		{Term: "carried", Turns: 3, Nearby: nil},
		{Term: "typo", Nearby: []Term{
			{Text: "typa", Distance: 1}, {Text: "typb", Distance: 1},
			{Text: "typc", Distance: 1}, {Text: "typd", Distance: 1},
			{Text: "type", Distance: 1}, {Text: "taupe", Distance: 2},
		}},
		{Term: "distant", Nearby: []Term{{Text: "distend", Distance: 2}}},
	}
	got := substitutions(reports)
	want := []Expansion{{Term: "typo", Variants: []string{"typa", "typb", "typc", "typd"}, Distance: 1}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("substitutions(...) = %+v, want %+v", got, want)
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
