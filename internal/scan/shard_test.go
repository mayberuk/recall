package scan

import (
	"fmt"
	"math/rand"
	"reflect"
	"runtime"
	"testing"

	"github.com/mayberuk/recall/internal/schema"
)

// withShardSize sets the sharding threshold so a corpus is cut into ranges of
// the given size. One range is the original single-pass algorithm; more is the
// concurrent path.
//
// It raises GOMAXPROCS as well, because shardCount caps the range count at it: a
// two-core CI runner would otherwise exercise two ranges where a sixteen-core
// developer machine exercises sixteen, and more ranges is precisely where the
// merge rule gets harder. Pinning it makes the shape of the test the same
// everywhere.
func withShardSize(t *testing.T, turnsPerRange int) {
	t.Helper()
	wasMin := minShardTurns
	wasProcs := runtime.GOMAXPROCS(32)
	t.Cleanup(func() {
		minShardTurns = wasMin
		runtime.GOMAXPROCS(wasProcs)
	})
	minShardTurns = turnsPerRange
}

// allocsPerSearch is testing.AllocsPerRun with the one thing that makes it
// unusable here removed: that function pins GOMAXPROCS to 1 for its duration,
// which drives the range count to one and measures the single-pass path under
// the name of the sharded one.
func allocsPerSearch(runs int, fn func()) float64 {
	fn()
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for range runs {
		fn()
	}
	runtime.ReadMemStats(&after)
	return float64(after.Mallocs-before.Mallocs) / float64(runs)
}

// TestTheRangeFloorTakesOnlyAUsableOverride pins the env knob the differential
// harness drives the built binary with. A value that silently failed to parse
// would leave that harness comparing two single-pass scans and reporting a pass
// for a path it never ran.
func TestTheRangeFloorTakesOnlyAUsableOverride(t *testing.T) {
	const dflt = 2048
	for _, c := range []struct {
		env  string
		want int
	}{
		{"64", 64},
		{"1", 1},
		{"", dflt},
		{"0", dflt},
		{"-8", dflt},
		{"sixty-four", dflt},
		{"64 ", dflt},
	} {
		if got := rangeFloor(c.env, dflt); got != c.want {
			t.Errorf("rangeFloor(%q) = %d, want %d", c.env, got, c.want)
		}
	}
}

// TestShardingDoesNotChangeAnyAnswer is the correctness argument for scanning
// concurrently. The claim is not that the parallel path looks right, it is that
// it is indistinguishable from the single pass — every hit, every offset, every
// coverage count, every reported term.
//
// The queries are drawn at random from a seeded generator against a generated
// corpus, because the case that breaks a merge rule is a distribution of term
// counts across shard boundaries, and that is not something to enumerate by
// hand. The named cases below cover the specific merge paths.
func TestShardingDoesNotChangeAnyAnswer(t *testing.T) {
	corpus := shardCorpus(4000)
	rnd := rand.New(rand.NewSource(7))

	vocab := []string{"alpha", "bravo", "charlie", "delta", "echo", "zulu", "quebec", "absent"}
	for round := range 300 {
		var q Query
		n := 1 + rnd.Intn(3)
		text := ""
		for range n {
			if text != "" {
				text += " "
			}
			text += vocab[rnd.Intn(len(vocab))]
		}
		q.Text = text
		q.AllTerms = rnd.Intn(4) == 0
		q.Exact = rnd.Intn(4) == 0
		if rnd.Intn(5) == 0 {
			q.Not = []string{vocab[rnd.Intn(len(vocab))]}
		}
		if rnd.Intn(6) == 0 {
			q.Tiers = []schema.Tier{schema.TierConversation, schema.TierResult}
		}
		if rnd.Intn(8) == 0 {
			q.Keep = func(tn *schema.Turn) bool { return tn.Author == schema.AuthorAssistant }
		}

		sequential := searchWith(t, corpus, q, len(corpus)+1)
		parallel := searchWith(t, corpus, q, 64)
		if !reflect.DeepEqual(sequential, parallel) {
			t.Fatalf("round %d, query %+v: sharding changed the answer\n%s",
				round, q, describe(sequential, parallel))
		}
	}
}

// TestShardingHoldsAtEveryShardCount widens the same claim across cut points,
// because a merge rule can be right for two ranges and wrong for nine — a term
// count that appears in exactly one shard is the case the rule turns on.
func TestShardingHoldsAtEveryShardCount(t *testing.T) {
	corpus := shardCorpus(1800)
	queries := []Query{
		{Text: "alpha"},
		{Text: "alpha bravo"},
		{Text: "alpha bravo charlie"},
		{Text: "alpha bravo charlie", AllTerms: true},
		{Text: "zulu quebec"},
		{Text: "absent"},
		{Text: "alpha absent"},
		{Text: `"alpha bravo"`},
		{Text: "alpha", Not: []string{"bravo"}},
	}
	for _, q := range queries {
		t.Run(q.Text+fmt.Sprintf("/all-terms=%v/not=%v", q.AllTerms, q.Not), func(t *testing.T) {
			want := searchWith(t, corpus, q, len(corpus)+1)
			for _, per := range []int{1, 2, 3, 7, 16, 100, 601} {
				if got := searchWith(t, corpus, q, per); !reflect.DeepEqual(want, got) {
					t.Errorf("%d turns per shard changed the answer\n%s", per, describe(want, got))
				}
			}
		})
	}
}

// TestAShardThatFoundLessContributesToTheRelaxedResult pins the merge path that
// a uniform corpus never reaches: the best term count exists in one shard only,
// and another shard's best is exactly one below it. Those turns belong in the
// relaxed result, not discarded, because a single pass would have kept them.
func TestAShardThatFoundLessContributesToTheRelaxedResult(t *testing.T) {
	// Three turns, each its own shard. Only the first carries both terms.
	corpus := []schema.Turn{
		turnOf("s1", "u1", schema.AuthorAssistant, "alpha and bravo together here"),
		turnOf("s2", "u2", schema.AuthorAssistant, "alpha on its own here"),
		turnOf("s3", "u3", schema.AuthorAssistant, "nothing relevant in this one"),
	}
	q := Query{Text: "alpha bravo"}

	want := searchWith(t, corpus, q, len(corpus)+1)
	if want.Match.Required != 2 || want.Match.Total != 2 {
		t.Fatalf("the single pass carried %d of %d terms, want 2 of 2 — the fixture is wrong",
			want.Match.Required, want.Match.Total)
	}
	// The two-term turn is satisfiable, so the one-term turn must NOT appear.
	for _, h := range want.Hits {
		if h.Session == "s2" {
			t.Fatalf("the single pass returned the one-term turn alongside a satisfiable match")
		}
	}
	if got := searchWith(t, corpus, q, 1); !reflect.DeepEqual(want, got) {
		t.Errorf("one turn per shard changed the answer\n%s", describe(want, got))
	}
}

// TestARelaxedQueryKeepsOneLevelOfSlackAcrossShards is the same path with no
// satisfiable turn anywhere, which is when the slack level is actually returned.
func TestARelaxedQueryKeepsOneLevelOfSlackAcrossShards(t *testing.T) {
	corpus := []schema.Turn{
		turnOf("s1", "u1", schema.AuthorAssistant, "alpha and bravo but not the third"),
		turnOf("s2", "u2", schema.AuthorAssistant, "alpha alone over here"),
		turnOf("s3", "u3", schema.AuthorAssistant, "bravo alone over here"),
		turnOf("s4", "u4", schema.AuthorAssistant, "unrelated words entirely"),
	}
	q := Query{Text: "alpha bravo charlie"}

	want := searchWith(t, corpus, q, len(corpus)+1)
	if !want.Match.Relaxed() {
		t.Fatalf("the single pass carried %d of %d terms, want a relaxed result — the fixture is wrong",
			want.Match.Required, want.Match.Total)
	}
	if want.Match.Required != 1 {
		t.Fatalf("the single pass required %d terms, want 1 (two carried, one level of slack)",
			want.Match.Required)
	}
	for _, per := range []int{1, 2, 3} {
		if got := searchWith(t, corpus, q, per); !reflect.DeepEqual(want, got) {
			t.Errorf("%d turns per shard changed the answer\n%s", per, describe(want, got))
		}
	}
}

// TestASessionSplitAcrossShardsIsCountedOnce guards the coverage figures, which
// come from a per-session map each shard keeps separately. A session whose turns
// straddle a cut must not become two sessions or lose half its turn count — the
// concentration denominator ranking uses is that count.
func TestASessionSplitAcrossShardsIsCountedOnce(t *testing.T) {
	var corpus []schema.Turn
	for i := range 40 {
		corpus = append(corpus, turnOf("one-session", fmt.Sprintf("u%d", i),
			schema.AuthorAssistant, fmt.Sprintf("turn %d carries alpha", i)))
	}
	q := Query{Text: "alpha"}

	want := searchWith(t, corpus, q, len(corpus)+1)
	if want.Sessions != 1 || want.TurnsBySession["one-session"] != 40 {
		t.Fatalf("the single pass saw %d sessions and %d turns, want 1 and 40 — the fixture is wrong",
			want.Sessions, want.TurnsBySession["one-session"])
	}
	for _, per := range []int{1, 3, 8, 13} {
		got := searchWith(t, corpus, q, per)
		if got.Sessions != 1 {
			t.Errorf("%d turns per shard reported %d sessions, want 1", per, got.Sessions)
		}
		if got.TurnsBySession["one-session"] != 40 {
			t.Errorf("%d turns per shard counted %d turns for the session, want 40",
				per, got.TurnsBySession["one-session"])
		}
		if !reflect.DeepEqual(want, got) {
			t.Errorf("%d turns per shard changed the answer\n%s", per, describe(want, got))
		}
	}
}

// TestShardingCostsPerShardNotPerTurn holds the price of scanning concurrently
// to what it is meant to be. Every range sets up its own matcher scratch, session
// map and hit slice, so allocations rise with the number of ranges — that is the
// deal. What must not happen is a per-turn cost sneaking in behind it, because
// that scales with the corpus and the concurrency would be paying for itself.
//
// Stated as a ratio between two corpus sizes at a fixed range count rather than
// as a ceiling, so it measures the thing it names and not this implementation's
// current output.
func TestShardingCostsPerShardNotPerTurn(t *testing.T) {
	q := Query{Text: "alpha bravo"}

	// The same number of sessions in both corpora. One sessionState per session
	// is a cost the single pass has always had and it is proportional to sessions,
	// not to turns, so letting it vary here would make a pre-existing per-session
	// allocation look like a per-turn one this change introduced.
	const sessions = 32
	small := shardCorpusOf(2000, sessions)
	large := shardCorpusOf(20000, sessions)

	// Both corpora are cut into the same number of ranges, so the only variable
	// left is how many turns each range holds.
	const ranges = 8
	measure := func(corpus []schema.Turn) (float64, int) {
		withShardSize(t, len(corpus)/ranges)
		return allocsPerSearch(5, func() { Search(corpus, q) }), shardCount(len(corpus))
	}

	few, fewRanges := measure(small)
	many, manyRanges := measure(large)
	if fewRanges != manyRanges || fewRanges != ranges {
		t.Fatalf("the corpora were cut into %d and %d ranges, want %d each",
			fewRanges, manyRanges, ranges)
	}
	if many > few*2 {
		t.Errorf("ten times the turns over the same %d ranges took %.0f allocations against %.0f — "+
			"the sharded scan is allocating per turn, not per range", ranges, many, few)
	}
	t.Logf("2,000 turns: %.0f allocations · 20,000 turns: %.0f allocations, both over %d ranges",
		few, many, ranges)
}

func searchWith(t *testing.T, corpus []schema.Turn, q Query, perShard int) Result {
	t.Helper()
	withShardSize(t, perShard)
	return Search(corpus, q)
}

// shardCorpus builds n turns in sessions of 17, which is small enough that most
// shard cut points split one.
func shardCorpus(n int) []schema.Turn { return shardCorpusOf(n, (n+16)/17) }

// shardCorpusOf builds n turns spread over the given number of sessions. Term
// counts vary so that different shards reach different bests, and every term is
// drawn from a seeded generator: the same corpus on every run and every machine.
func shardCorpusOf(n, sessions int) []schema.Turn {
	rnd := rand.New(rand.NewSource(3))
	filler := []string{"walk", "value", "return", "path", "offset", "reader", "window"}
	terms := []string{"alpha", "bravo", "charlie", "delta", "echo", "zulu", "quebec"}

	authors := []schema.Author{schema.AuthorHuman, schema.AuthorAssistant, schema.AuthorAgent}
	tiers := []schema.Tier{schema.TierConversation, schema.TierConversation, schema.TierResult}

	out := make([]schema.Turn, 0, n)
	for i := range n {
		text := ""
		// A handful of turns carry many query terms and most carry none, which is
		// the shape that makes one shard's best differ from another's.
		for range 4 + rnd.Intn(12) {
			text += filler[rnd.Intn(len(filler))] + " "
		}
		for range rnd.Intn(4) {
			text += terms[rnd.Intn(len(terms))] + " "
		}
		out = append(out, schema.Turn{
			// Contiguous blocks, as a real store has them, so that a cut point
			// lands inside a session rather than between two.
			Session: fmt.Sprintf("session-%02d", i/max(n/max(sessions, 1), 1)),
			UUID:    fmt.Sprintf("uuid-%05d", i),
			TS:      fmt.Sprintf("2026-01-01T%02d:%02d:%02dZ", i/3600%24, i/60%60, i%60),
			Tier:    tiers[i%len(tiers)],
			Author:  authors[i%len(authors)],
			Text:    text,
		})
	}
	return out
}

func turnOf(session, uuid string, author schema.Author, text string) schema.Turn {
	return schema.Turn{
		Session: session,
		UUID:    uuid,
		TS:      "2026-01-01T00:00:00Z",
		Tier:    schema.TierConversation,
		Author:  author,
		Text:    text,
	}
}

// describe names the first field that differs, so a failure says what moved
// rather than printing two whole results.
func describe(want, got Result) string {
	switch {
	case want.Match.Required != got.Match.Required:
		return fmt.Sprintf("  required terms: %d, sharded %d", want.Match.Required, got.Match.Required)
	case len(want.Hits) != len(got.Hits):
		return fmt.Sprintf("  hits: %d, sharded %d", len(want.Hits), len(got.Hits))
	case want.Turns != got.Turns:
		return fmt.Sprintf("  turns seen: %d, sharded %d", want.Turns, got.Turns)
	case want.TurnsScanned != got.TurnsScanned:
		return fmt.Sprintf("  turns scanned: %d, sharded %d", want.TurnsScanned, got.TurnsScanned)
	case want.Sessions != got.Sessions:
		return fmt.Sprintf("  sessions: %d, sharded %d", want.Sessions, got.Sessions)
	case want.SessionsScanned != got.SessionsScanned:
		return fmt.Sprintf("  sessions scanned: %d, sharded %d", want.SessionsScanned, got.SessionsScanned)
	case !reflect.DeepEqual(want.Match.Carried, got.Match.Carried):
		return fmt.Sprintf("  carried: %v, sharded %v", want.Match.Carried, got.Match.Carried)
	case !reflect.DeepEqual(want.TurnsBySession, got.TurnsBySession):
		return "  turns-by-session differs"
	case !reflect.DeepEqual(want.Terms, got.Terms):
		return "  the reported per-term survey differs"
	}
	for i := range want.Hits {
		if want.Hits[i] != got.Hits[i] {
			return fmt.Sprintf("  hit %d: %+v\n  sharded: %+v", i, want.Hits[i], got.Hits[i])
		}
	}
	return "  results differ in a field describe() does not name"
}
