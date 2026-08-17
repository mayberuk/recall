package scan

import (
	"fmt"
	"reflect"
	"slices"
	"testing"

	"github.com/mayberuk/recall/internal/schema"
)

// TestTheSuggestionBudgetStopsAtTheSameTurnHoweverTheCorpusIsCut is the one way
// scanning the miss path concurrently could change an answer. Counting and
// collecting are both addition, so the order ranges finish in cannot matter —
// but the byte budget is a running total, and a budget handed out range by range
// would stop at a different turn on a machine with a different core count. The
// word planted just past the cut is the witness: it must never be offered.
func TestTheSuggestionBudgetStopsAtTheSameTurnHoweverTheCorpusIsCut(t *testing.T) {
	const (
		inside  = 100
		outside = 150
	)
	corpus := make([]schema.Turn, 0, 200)
	for i := range 200 {
		text := "walk value return path offset reader window"
		switch i {
		case inside:
			text = "the cursor moved"
		case outside:
			text = "the cursos moved"
		}
		corpus = append(corpus, turnOf("s1", fmt.Sprintf("u%03d", i), schema.AuthorAssistant, text))
	}

	// The budget is tested before a turn is read, so a turn is covered when
	// everything ahead of it fits. Paying for turns 0 through `inside` therefore
	// buys exactly those and stops.
	spent := 0
	for i := range inside + 1 {
		spent += len(corpus[i].Text)
	}
	restore := nearbyBudget
	nearbyBudget = spent
	t.Cleanup(func() { nearbyBudget = restore })

	want := searchWith(t, corpus, Query{Text: "curson"}, len(corpus)+1)
	got := names(report(t, want, "curson").Nearby)
	if !slices.Contains(got, "cursor") {
		t.Fatalf("the single pass offered %v, missing the word inside the budget — the fixture is wrong", got)
	}
	if slices.Contains(got, "cursos") {
		t.Fatalf("the single pass offered %v, including the word past the budget — the fixture is wrong", got)
	}

	for _, per := range []int{1, 7, 33, 64} {
		sharded := searchWith(t, corpus, Query{Text: "curson"}, per)
		if !reflect.DeepEqual(want.Terms, sharded.Terms) {
			t.Errorf("%d turns per range moved the budget: offered %v, want %v",
				per, names(report(t, sharded, "curson").Nearby), got)
		}
	}
}

// TestTermCountsAddUpAcrossRanges guards the counting walk's merge. Each range
// counts the turns it saw and the merge sums them, so a range whose count was
// dropped or overwritten would under-report — and an under-counted claim about
// the corpus is the failure shape this tool exists to avoid.
func TestTermCountsAddUpAcrossRanges(t *testing.T) {
	const carriers = 40
	corpus := make([]schema.Turn, 0, 400)
	for i := range 400 {
		text := "walk value return path offset reader window"
		if i%10 == 0 {
			text = "the cursor moved here"
		}
		corpus = append(corpus, turnOf("s1", fmt.Sprintf("u%03d", i), schema.AuthorAssistant, text))
	}

	// Both terms are needed, and no turn carries the absent one, so the search
	// misses and the survey runs over a corpus one term is spread all through.
	q := Query{Text: "cursor jabberwock", AllTerms: true}
	for _, per := range []int{len(corpus) + 1, 1, 7, 33, 64} {
		res := searchWith(t, corpus, q, per)
		if len(res.Hits) != 0 {
			t.Fatalf("%d turns per range returned %d hits, so this does not measure the miss path", per, len(res.Hits))
		}
		if n := report(t, res, "cursor").Turns; n != carriers {
			t.Errorf("%d turns per range counted %d turns carrying \"cursor\", want %d", per, n, carriers)
		}
	}
}

// TestASuggestedWordIsCountedInEveryRangeItAppearsIn guards the collector merge.
// Occurrences are what orders two suggestions at the same edit distance, so a
// merge that took one range's tally instead of the sum would reorder the offer.
func TestASuggestedWordIsCountedInEveryRangeItAppearsIn(t *testing.T) {
	corpus := make([]schema.Turn, 0, 400)
	for i := range 400 {
		// "cursor" in most turns and "cursos" in a handful, both one edit from the
		// query. Ordering them is the count's whole job here.
		text := "the cursor moved here"
		if i%40 == 0 {
			text = "the cursos moved here"
		}
		corpus = append(corpus, turnOf("s1", fmt.Sprintf("u%03d", i), schema.AuthorAssistant, text))
	}
	const (
		cursos = 400 / 40
		cursor = 400 - cursos
	)

	for _, per := range []int{len(corpus) + 1, 1, 7, 33, 64} {
		nearby := report(t, searchWith(t, corpus, Query{Text: "curson"}, per), "curson").Nearby
		if len(nearby) < 2 {
			t.Fatalf("%d turns per range offered %v, want both spellings", per, names(nearby))
		}
		if nearby[0].Text != "cursor" || nearby[0].Count != cursor {
			t.Errorf("%d turns per range offered %q first with %d occurrences, want \"cursor\" with %d",
				per, nearby[0].Text, nearby[0].Count, cursor)
		}
		if nearby[1].Text != "cursos" || nearby[1].Count != cursos {
			t.Errorf("%d turns per range offered %q second with %d occurrences, want \"cursos\" with %d",
				per, nearby[1].Text, nearby[1].Count, cursos)
		}
	}
}
