package integration

import (
	"strings"
	"testing"

	"github.com/mayberuk/recall/internal/rank"
	"github.com/mayberuk/recall/internal/scan"
	"github.com/mayberuk/recall/internal/schema"
)

// tiersOf is every tier, because a needle planted in tool output is only
// reachable when the caller opts in and the property must hold there too.
var tiersOf = []schema.Tier{schema.TierConversation, schema.TierInvocation, schema.TierResult}

// The no-false-negative property, carried through the two changes most able to
// break it: keeping only the turns that carry the most query terms, and
// collapsing a turn's occurrences into one display line.
func TestEveryPlantedTokenSurvivesRankingAndCollapse(t *testing.T) {
	g := build(t)
	for _, n := range g.corpus.Manifest.Needles {
		t.Run(n.Token, func(t *testing.T) {
			res := scan.Search(g.turns, scan.Query{Text: n.Token, Tiers: tiersOf, NearbyMax: -1})
			if len(res.Hits) == 0 {
				t.Fatalf("scan lost the token")
			}
			ranked := rank.Rank(res.Hits, res.TurnsBySession, rank.Concentration)
			for _, s := range ranked.Sessions {
				if s.ID != n.Session {
					continue
				}
				for _, m := range s.Turnwise {
					if m.UUID == n.UUID && strings.Contains(m.Text, n.Token) {
						return
					}
				}
			}
			t.Errorf("the token reached scan but no collapsed line in session %s carries it", n.Session)
		})
	}
}

// A query with one term the corpus carries and one it does not used to return
// nothing, which an agent reads as "the tool cannot find this".
func TestAQueryWithOneImpossibleTermStillReturnsTheRest(t *testing.T) {
	g := build(t)
	needle := g.corpus.Manifest.Needles[0]
	q := needle.Token + " zzzzznothingcarriesthisanywhere"

	res := scan.Search(g.turns, scan.Query{Text: q, Tiers: tiersOf, NearbyMax: -1})
	if len(res.Hits) == 0 {
		t.Fatalf("a two-term query lost the term the corpus does carry")
	}
	if !res.Match.Relaxed() {
		t.Errorf("the search relaxed without saying so: %+v", res.Match)
	}
	if res.Match.Required != 1 || res.Match.Total != 2 {
		t.Errorf("Required %d of %d, want 1 of 2", res.Match.Required, res.Match.Total)
	}

	strict := scan.Search(g.turns, scan.Query{Text: q, Tiers: tiersOf, AllTerms: true, NearbyMax: -1})
	if len(strict.Hits) != 0 {
		t.Errorf("--all-terms returned %d hits for a term nothing carries", len(strict.Hits))
	}
}

// The scanner counts the turns a filter rejected, so the coverage line's
// searched-of-total figures state a narrowing instead of hiding it.
func TestAFilteredSearchStillReportsTheWholeCorpusItSkipped(t *testing.T) {
	g := build(t)
	needle := g.corpus.Manifest.Needles[0]

	all := scan.Search(g.turns, scan.Query{Text: needle.Token, Tiers: tiersOf, NearbyMax: -1})
	filtered := scan.Search(g.turns, scan.Query{
		Text: needle.Token, Tiers: tiersOf, NearbyMax: -1,
		Keep: func(turn *schema.Turn) bool { return turn.Session != needle.Session },
	})

	if len(filtered.Hits) != 0 {
		t.Errorf("the filter did not exclude the session holding the token")
	}
	if filtered.Turns != all.Turns || filtered.Sessions != all.Sessions {
		t.Errorf("a filtered search reported %d turns in %d sessions against %d and %d unfiltered; the totals are what make the narrowing visible",
			filtered.Turns, filtered.Sessions, all.Turns, all.Sessions)
	}
	if filtered.TurnsScanned >= all.TurnsScanned {
		t.Errorf("%d turns searched under a filter, %d without it", filtered.TurnsScanned, all.TurnsScanned)
	}
}
