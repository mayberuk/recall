package rank_test

import (
	"math/rand"
	"slices"
	"testing"

	"github.com/mayberuk/recall/internal/fixtures"
	"github.com/mayberuk/recall/internal/rank"
	"github.com/mayberuk/recall/internal/schema"
)

func TestRankOrdersTheContractTable(t *testing.T) {
	var hits []schema.Hit
	turns := map[string]int{}
	for _, r := range walletTable {
		hits = append(hits, spread(r.session, r.hits)...)
		turns[r.session] = r.turns
	}

	got := rank.Rank(hits, turns, rank.Concentration)

	if diff := sessionIDs(got.Sessions); !slices.Equal(diff, wantOrder) {
		t.Fatalf("concentration order %v, want %v", diff, wantOrder)
	}
	for _, r := range walletTable {
		s := sessionByID(t, got, r.session)
		if s.HitCount != r.hits {
			t.Errorf("%s hit count %d, want %d", r.session, s.HitCount, r.hits)
		}
		if s.Turns != r.turns {
			t.Errorf("%s denominator %d, want the supplied %d", r.session, s.Turns, r.turns)
		}
	}
}

func TestRankFoldsSubagentHitsIntoTheParentSession(t *testing.T) {
	m := fixtures.Materialize(t).Manifest
	parent := needleFor(t, m, fixtures.NeedleConversation)
	sub := needleFor(t, m, fixtures.NeedleSubagent)
	if sub.Session != parent.Session {
		t.Fatalf("fixture subagent needle sits in %s, not the parent session %s", sub.Session, parent.Session)
	}

	got := rank.Rank([]schema.Hit{
		hit(parent.Session, parent.UUID, text(parent.Token)),
		hit(sub.Session, sub.UUID, text(sub.Token), author(schema.AuthorAgent)),
	}, map[string]int{parent.Session: 40}, rank.Concentration)

	if len(got.Sessions) != 1 {
		t.Fatalf("got %d results, want 1 — a subagent hit is not a separate result", len(got.Sessions))
	}
	s := got.Sessions[0]
	if s.HitCount != 2 || s.AgentHits != 1 {
		t.Fatalf("session %s has %d hits / %d agent-authored, want 2 / 1", s.ID, s.HitCount, s.AgentHits)
	}
}

func TestRankCountsARecordCarriedIntoASecondFileOnce(t *testing.T) {
	m := fixtures.Materialize(t).Manifest
	if len(m.DupUUIDs) == 0 {
		t.Fatal("fixtures manifest names no duplicated uuid")
	}
	dup := m.DupUUIDs[0]
	if len(dup.Files) < 2 {
		t.Fatalf("manifest duplicate %s lives in %v, want two files", dup.UUID, dup.Files)
	}

	copied := hit(dup.Session, dup.UUID, text(fixtures.NeedleDuplicated))
	got := rank.Rank([]schema.Hit{copied, copied}, map[string]int{dup.Session: 30}, rank.Concentration)

	if got.HitCount != 1 || got.Redundant != 1 {
		t.Fatalf("got %d hits / %d redundant, want 1 / 1", got.HitCount, got.Redundant)
	}
	s := sessionByID(t, got, dup.Session)
	if s.HitCount != 1 || s.HitTurns != 1 {
		t.Fatalf("session %s: %d hits over %d turns, want 1 / 1", s.ID, s.HitCount, s.HitTurns)
	}
	if repos := got.Facets.Repo; len(repos) != 1 || repos[0].Hits != 1 {
		t.Fatalf("repo facet %+v double-counts the redundant copy", repos)
	}
}

// TestRankKeepsAUUIDTwoSessionsCarry pins the measured fact that a fork rewrites
// the session on records it carries forward: 3,402 uuids in the real store hold
// two sessionIds, and collapsing them would delete the turn from one session.
func TestRankKeepsAUUIDTwoSessionsCarry(t *testing.T) {
	m := fixtures.Materialize(t).Manifest
	if len(m.MultiSessionIDs) < 2 {
		t.Fatalf("manifest names %v, want two sessions in one file", m.MultiSessionIDs)
	}
	shared := "shared-uuid-0000-4000-8000-000000000001"

	got := rank.Rank([]schema.Hit{
		hit(m.MultiSessionIDs[0], shared),
		hit(m.MultiSessionIDs[1], shared),
	}, map[string]int{m.MultiSessionIDs[0]: 20, m.MultiSessionIDs[1]: 20}, rank.Concentration)

	if got.HitCount != 2 || got.Redundant != 0 {
		t.Fatalf("got %d hits / %d redundant, want 2 / 0", got.HitCount, got.Redundant)
	}
	if len(got.Sessions) != 2 {
		t.Fatalf("got %d sessions, want both forks to keep the turn", len(got.Sessions))
	}
}

func TestRankKeepsSeparateMatchesInOneTurn(t *testing.T) {
	got := rank.Rank([]schema.Hit{
		hit("s", "u", offset(0)),
		hit("s", "u", offset(64)),
	}, map[string]int{"s": 10}, rank.Concentration)

	if got.HitCount != 2 {
		t.Fatalf("got %d hits, want 2 — two matches in one turn are two hits", got.HitCount)
	}
	if s := got.Sessions[0]; s.HitTurns != 1 {
		t.Fatalf("hit turns %d, want 1 — both matches occupy the same turn", s.HitTurns)
	}
}

// TestRankKeepsTextAndThinkingOfOneRecord covers the fixture where one record
// uuid carries a needle in its assistant text and another in its thinking: two
// turns, each with its own offsets, so a uuid-plus-offset key would drop one.
func TestRankKeepsTextAndThinkingOfOneRecord(t *testing.T) {
	m := fixtures.Materialize(t).Manifest
	spoken := needleFor(t, m, fixtures.NeedleConversation)
	thought := needleFor(t, m, fixtures.NeedleThinking)
	if spoken.UUID != thought.UUID {
		t.Fatalf("fixture needles sit on %s and %s, want one record uuid", spoken.UUID, thought.UUID)
	}

	got := rank.Rank([]schema.Hit{
		hit(spoken.Session, spoken.UUID, text(spoken.Token)),
		hit(thought.Session, thought.UUID, text(thought.Token)),
	}, map[string]int{spoken.Session: 10}, rank.Concentration)

	if got.HitCount != 2 {
		t.Fatalf("got %d hits, want 2 — the thinking turn and the text turn are distinct", got.HitCount)
	}
}

func TestRankPutsAMissingTurnCountLastAndReportsIt(t *testing.T) {
	loud := spread("no-count", 500)
	quiet := spread("counted", 3)

	got := rank.Rank(append(loud, quiet...), map[string]int{"counted": 50}, rank.Concentration)

	if ids := sessionIDs(got.Sessions); !slices.Equal(ids, []string{"counted", "no-count"}) {
		t.Fatalf("order %v, want the session with a known denominator first", ids)
	}
	if !slices.Equal(got.UnknownTurns, []string{"no-count"}) {
		t.Fatalf("UnknownTurns %v, want [no-count] — a missing denominator is reported, not silent", got.UnknownTurns)
	}
	s := sessionByID(t, got, "no-count")
	if s.TurnsKnown {
		t.Error("TurnsKnown is true for a session the caller supplied no count for")
	}
	if s.Turns != 500 {
		t.Errorf("denominator %d, want the 500 turns its own hits prove", s.Turns)
	}
}

func TestRankTreatsAZeroTurnCountAsMissing(t *testing.T) {
	got := rank.Rank(spread("s", 4), map[string]int{"s": 0}, rank.Concentration)

	s := got.Sessions[0]
	if s.TurnsKnown {
		t.Error("a zero turn count contradicts a session that has hits; it must not be believed")
	}
	if !slices.Equal(got.UnknownTurns, []string{"s"}) {
		t.Errorf("UnknownTurns %v, want [s]", got.UnknownTurns)
	}
}

func TestRankClampsATurnCountBelowItsOwnHitEvidence(t *testing.T) {
	got := rank.Rank(spread("s", 10), map[string]int{"s": 2}, rank.Concentration)

	s := got.Sessions[0]
	if s.Turns != 10 {
		t.Fatalf("denominator %d, want 10 — ten conversation turns hold the hits", s.Turns)
	}
	if !s.TurnsKnown {
		t.Error("a count present but under-reported is clamped, not discarded")
	}
	if want := rank.Score(signalOf(s.Turnwise), 10); s.Score != want {
		t.Errorf("score %v, want %v", s.Score, want)
	}
}

// TestRankCountsOnlyConversationTurnsInTheDenominator keeps tool use from
// penalising a session: result-tier hits raise the numerator but never the floor
// under the denominator.
func TestRankCountsOnlyConversationTurnsInTheDenominator(t *testing.T) {
	got := rank.Rank(spread("s", 5, tier(schema.TierResult)), map[string]int{"s": 3}, rank.Concentration)

	s := got.Sessions[0]
	if s.HitTurns != 0 {
		t.Fatalf("conversation hit turns %d, want 0 — every hit is tool output", s.HitTurns)
	}
	if s.Turns != 3 {
		t.Fatalf("denominator %d, want the supplied 3 conversation turns", s.Turns)
	}
}

func TestRankChronologicalOrdersOldestFirst(t *testing.T) {
	hits := []schema.Hit{
		hit("late", "l1", at(300)),
		hit("early", "e1", at(0)),
		hit("middle", "m1", at(100)),
	}
	turns := map[string]int{"late": 5, "early": 900, "middle": 5}

	got := rank.Rank(hits, turns, rank.Chronological)

	if ids := sessionIDs(got.Sessions); !slices.Equal(ids, []string{"early", "middle", "late"}) {
		t.Fatalf("chronological order %v, want [early middle late]", ids)
	}
	if got.Mode != rank.Chronological {
		t.Errorf("Mode %q, want %q", got.Mode, rank.Chronological)
	}
}

func TestRankRecentOrdersNewestFirst(t *testing.T) {
	hits := []schema.Hit{
		hit("early", "e1", at(0)),
		hit("late", "l1", at(300)),
		hit("middle", "m1", at(100)),
	}
	turns := map[string]int{"late": 900, "early": 5, "middle": 5}

	got := rank.Rank(hits, turns, rank.Recent)

	if ids := sessionIDs(got.Sessions); !slices.Equal(ids, []string{"late", "middle", "early"}) {
		t.Fatalf("recent order %v, want [late middle early]", ids)
	}
}

func TestRankSortsUndatedSessionsLast(t *testing.T) {
	hits := []schema.Hit{
		{Session: "undated", UUID: "u1", Tier: schema.TierConversation, Author: schema.AuthorHuman},
		hit("dated", "d1", at(10)),
	}

	got := rank.Rank(hits, map[string]int{"undated": 5, "dated": 5}, rank.Chronological)

	if ids := sessionIDs(got.Sessions); !slices.Equal(ids, []string{"dated", "undated"}) {
		t.Fatalf("order %v, want an unparseable timestamp last rather than claiming to be oldest", ids)
	}
}

func TestRankFallsBackToConcentrationForAnUnknownMode(t *testing.T) {
	hits := append(spread("dense", 8), spread("sparse", 1)...)
	turns := map[string]int{"dense": 20, "sparse": 20}

	got := rank.Rank(hits, turns, rank.Mode("by-vibes"))

	if got.Mode != rank.Concentration {
		t.Fatalf("Mode %q, want %q reported for an unrecognised mode", got.Mode, rank.Concentration)
	}
	if ids := sessionIDs(got.Sessions); !slices.Equal(ids, []string{"dense", "sparse"}) {
		t.Fatalf("order %v, want concentration order", ids)
	}
}

func TestRankIsDeterministicUnderInputOrder(t *testing.T) {
	var hits []schema.Hit
	for _, id := range []string{"bbb", "aaa", "ccc"} {
		hits = append(hits, spread(id, 4)...)
	}
	turns := map[string]int{"aaa": 40, "bbb": 40, "ccc": 40}

	want := sessionIDs(rank.Rank(hits, turns, rank.Concentration).Sessions)
	if !slices.Equal(want, []string{"aaa", "bbb", "ccc"}) {
		t.Fatalf("tied sessions ordered %v, want the session id as the total-order tiebreak", want)
	}

	rng := rand.New(rand.NewSource(7))
	for range 20 {
		shuffled := slices.Clone(hits)
		rng.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
		if got := sessionIDs(rank.Rank(shuffled, turns, rank.Concentration).Sessions); !slices.Equal(got, want) {
			t.Fatalf("order %v after shuffling the input, want %v", got, want)
		}
	}
}

func TestRankReportsTheDominantRepoAndBranchOfASession(t *testing.T) {
	hits := []schema.Hit{
		hit("s", "u1", branch("main")),
		hit("s", "u2", branch("feature")),
		hit("s", "u3", branch("main")),
	}

	got := rank.Rank(hits, map[string]int{"s": 10}, rank.Concentration)

	if s := got.Sessions[0]; s.Branch != "main" {
		t.Fatalf("branch %q, want main — the dominant value among the session's hits", s.Branch)
	}
}

func TestRankOrdersHitsWithinASessionOldestFirst(t *testing.T) {
	got := rank.Rank([]schema.Hit{
		hit("s", "u3", at(30)),
		hit("s", "u1", at(10)),
		hit("s", "u2", at(20)),
	}, map[string]int{"s": 10}, rank.Concentration)

	var order []string
	for _, h := range got.Sessions[0].Hits {
		order = append(order, h.UUID)
	}
	if !slices.Equal(order, []string{"u1", "u2", "u3"}) {
		t.Fatalf("hit order %v, want [u1 u2 u3]", order)
	}
}

func TestRankHandlesNoHits(t *testing.T) {
	got := rank.Rank(nil, nil, rank.Concentration)

	if len(got.Sessions) != 0 || got.HitCount != 0 || got.Redundant != 0 {
		t.Fatalf("empty input produced %+v", got)
	}
	if got.Mode != rank.Concentration {
		t.Errorf("Mode %q, want %q", got.Mode, rank.Concentration)
	}
}

func TestRankHandlesANilTurnMap(t *testing.T) {
	got := rank.Rank(spread("s", 3), nil, rank.Concentration)

	if !slices.Equal(got.UnknownTurns, []string{"s"}) {
		t.Fatalf("UnknownTurns %v, want [s]", got.UnknownTurns)
	}
	if s := got.Sessions[0]; s.Score != rank.Score(signalOf(s.Turnwise), 3) {
		t.Fatalf("score %v, want %v", s.Score, rank.Score(signalOf(s.Turnwise), 3))
	}
}

// signalOf re-derives a session's numerator from the matched turns themselves,
// so a score assertion states the rule rather than a copied constant.
func signalOf(matched []rank.Matched) float64 {
	total := 0.0
	for _, m := range matched {
		total += rank.Signal(m.Hit)
	}
	return total
}
