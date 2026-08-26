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

// A typo is the case this exists for: nothing came back for what was typed, and
// the corpus does carry a word one edit away. The word is both searched in the
// typo's place and still reported, because the report is what a caller checks
// the substitution against.
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

// spelledCorpus carries one word — "settlement" — and nothing else close to it,
// so a query against it either reaches that word or reaches nothing, and which
// of the two is not a matter of degree. Its byte total is written out here
// rather than measured, because the pass-counting tests below assert how many
// times the corpus was read and a figure taken from a run would agree with any
// number of readings.
const (
	spelledText  = "the settlement batch cleared"
	spelledBytes = int64(len(spelledText))
)

func spelledCorpus() []schema.Turn {
	return []schema.Turn{turn("s1", schema.TierConversation, spelledText)}
}

// The substitution's positive case, stated as the whole chain: what came back,
// what was declared, and what it cost. "settlemint" is one edit from
// "settlement" and matches nothing on its own — the stem cut leaves
// "settlemint" whole, because none of the suffixes it recognises ends it.
func TestAMissIsReRunWithTheCorpusWordOneEditAway(t *testing.T) {
	res := Search(spelledCorpus(), Query{Text: "settlemint"})

	if len(res.Hits) != 1 {
		t.Fatalf("%d hits, want 1 — the turn carrying the word one edit away", len(res.Hits))
	}
	// The hit locates "settlement", not the ten bytes of the typed term: a span
	// measured off the wrong needle would highlight the wrong extent.
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

// The negative control for the test above, and the distance rule stated as a
// behaviour: "settlamint" is two edits from "settlement", so the corpus word is
// offered and never searched. A rule that substituted whatever the collector
// ranked first would pass the positive case and fail here.
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

// --exact means match what was typed. Substituting a different word under it
// would be a lie, so the same query that expands by default returns nothing here
// and declares nothing.
func TestExactRunsNoExpansion(t *testing.T) {
	res := Search(spelledCorpus(), Query{Text: "settlemint", Exact: true})

	if len(res.Hits) != 0 {
		t.Errorf("%d hits under --exact, want 0", len(res.Hits))
	}
	if len(res.Match.Expanded) != 0 {
		t.Errorf("Expanded %+v under --exact, want nothing", res.Match.Expanded)
	}
	// The offer survives --exact, because offering is not substituting.
	if got := report(t, res, "settlemint"); len(got.Nearby) == 0 || got.Nearby[0].Text != "settlement" {
		t.Errorf("suggestions %v, want settlement still offered", names(got.Nearby))
	}
	if relaxed := Search(spelledCorpus(), Query{Text: "settlemint"}); len(relaxed.Hits) == 0 {
		t.Error("the same query without --exact found nothing, so --exact is not what suppressed it")
	}
}

// The hit path pays for none of this. A search whose turns carry every term
// reads the corpus once, and the byte figure is asserted alongside the pass
// count because a pass that read nothing would satisfy either one alone.
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

// The other miss shape: turns came back, but between them they carry nothing at
// all about one of the words typed. That term is expanded and the answer rises
// from one term of two to both — which is the only outcome that adopts the
// re-run, because an expansion that answers no more of the query than the first
// walk did is a different question asked for nothing.
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
	// The relaxed shape is not a zero-result search, so it owes no term report —
	// the footer's expansion line is where it says what it did.
	if len(res.Terms) != 0 {
		t.Errorf("%d term reports on a search that returned turns, want 0", len(res.Terms))
	}
}

// The other way a re-run earns its place: no turn carries both words even after
// the substitution, so the term count does not rise — but a term nothing carried
// at all is now carried by something, and those turns are the half of the query
// the caller had no answer about.
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

// The negative control for the two tests above, and the rule that keeps a
// re-run from swapping in a different question for nothing. The corpus word one
// edit away sits in a turn --not rules out, so the re-run reaches no turn the
// first walk did not: the answer, and the footer, stay exactly as they were.
// The pass is still charged, because it was still read.
func TestAReRunThatReachesNothingNewIsDiscardedAndStillCharged(t *testing.T) {
	turns := []schema.Turn{
		turn("s1", schema.TierConversation, "the batch was replayed"),
		turn("s2", schema.TierConversation, "the settlement batch is deprecated"),
	}
	res := Search(turns, Query{Text: "batch settlemint", Not: []string{"deprecated"}})

	// Three passes is what says the re-run happened at all: the survey gathers
	// from every searched turn, exclusions included, so "settlement" is offered
	// and searched even though the turn holding it is ruled out. At two passes
	// nothing was ever substituted and the rest of this would prove nothing. The
	// reading is charged either way, because it was read either way.
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

// The negative control for the test above. Both terms exist and simply never
// co-occur, so every term is carried by some returned turn and there is nothing
// for an expansion to answer. This is the relaxed shape that must stay at one
// pass.
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

// twoSpellings carries both words the collector offers for "curson", in the
// opposite order to the one it offers them in — nearest, then most used, then
// alphabetically, which puts "cursor" ahead of "cursos".
func twoSpellings() []schema.Turn {
	return []schema.Turn{turn("s1", schema.TierConversation, "cursos and cursor in one turn")}
}

// A term backed by several needles is still one query slot, and a turn carrying
// two of them is still one carrier of one term. Counting the needles instead
// would let a single turn score as if it had answered two parts of the query.
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

// One term with two needles is the only shape whose spans can arrive out of
// offset order with a single word in the query, because collect walks one needle
// to the end of the turn before starting the next. Ranking is handed hits in
// offset order within a turn, so the order is part of the answer — and this is
// the case the two-term test above cannot see, since a second term makes collect
// re-sort whatever the needles did.
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

// substitutions is the distance and cap rule on its own, over a candidate list
// hand-built to hold more of each than the rule allows. The collector orders its
// offers nearest first, which is what lets this stop at the first neighbour too
// far away rather than filtering the whole list.
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
