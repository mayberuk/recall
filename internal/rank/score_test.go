package rank_test

import (
	"cmp"
	"slices"
	"testing"

	"github.com/mayberuk/recall/internal/rank"
)

// TestDensityAloneFailsTheContractTable pins the defect the shrinkage term
// exists to fix, using the document's own ratio column rather than any code
// here: ordering by raw density puts one passing mention above the 554-hit
// session the query is actually about.
func TestDensityAloneFailsTheContractTable(t *testing.T) {
	passing := row(t, "e5f9a621")
	real := row(t, "6941d8f9")
	if !(passing.density > real.density) {
		t.Fatalf("contract table transcribed wrong: %s density %.4f should exceed %s density %.4f",
			passing.session, passing.density, real.session, real.density)
	}
}

func TestScoreOrdersTheContractTable(t *testing.T) {
	got := slices.Clone(walletTable)
	slices.SortFunc(got, func(a, b walletRow) int {
		if d := cmp.Compare(rank.Score(float64(b.hits), b.turns), rank.Score(float64(a.hits), a.turns)); d != 0 {
			return d
		}
		return cmp.Compare(a.session, b.session)
	})

	for i, want := range wantOrder {
		if got[i].session != want {
			t.Fatalf("rank %d is %s (%d/%d, %s), want %s\nfull order: %v",
				i+1, got[i].session, got[i].hits, got[i].turns, got[i].note, want, sessionsOf(got))
		}
	}
}

// TestScoreSinksASingleMentionAtAnySessionLength generalises the table's second
// row: the failure is not specific to 13 turns, so no session length may let one
// hit outrank the session with 554.
func TestScoreSinksASingleMentionAtAnySessionLength(t *testing.T) {
	real := row(t, "6941d8f9")
	want := rank.Score(float64(real.hits), real.turns)
	for turns := 1; turns <= real.turns; turns++ {
		if got := rank.Score(1, turns); got >= want {
			t.Fatalf("one hit in %d turns scores %.6f, at or above %s at %.6f", turns, got, real.session, want)
		}
	}
}

// TestShrinkageClearsTheDerivedMinimum records where the constant comes from: a
// single mention peaks at 1/(1+k), which must sit below the 554-hit session's
// concentration, giving k > 13.34.
func TestShrinkageClearsTheDerivedMinimum(t *testing.T) {
	real := row(t, "6941d8f9")
	peak := 1.0 / float64(1+rank.Shrinkage)
	if bound := float64(real.hits) / float64(real.turns); peak >= bound {
		t.Fatalf("Shrinkage %d lets one hit reach %.6f, at or above the %.6f bound from %s",
			rank.Shrinkage, peak, bound, real.session)
	}
}

func TestScoreHandlesAnAbsentDenominator(t *testing.T) {
	if got, want := rank.Score(5, 0), 5.0/float64(rank.Shrinkage); got != want {
		t.Fatalf("Score(5, 0) = %v, want %v — zero turns must not divide by zero", got, want)
	}
	if got := rank.Score(0, 100); got != 0 {
		t.Fatalf("Score(0, 100) = %v, want 0", got)
	}
	if got, want := rank.Score(3, -1), rank.Score(3, 0); got != want {
		t.Fatalf("Score(3, -1) = %v, want %v", got, want)
	}
}

func sessionsOf(rows []walletRow) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.session)
	}
	return out
}
