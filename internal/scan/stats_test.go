package scan

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mayberuk/recall/internal/schema"
)

// statsCorpus is deliberately small enough that its byte, line and word totals
// are written out by hand below, rather than taken from a run of the scanner.
// It covers the whitespace the word rule names: a run of spaces, tabs between
// words, and a carriage return before a newline.
func statsCorpus() []schema.Turn {
	return []schema.Turn{
		turnOf("s1", "u1", schema.AuthorHuman, "alpha bravo\ncharlie"),
		turnOf("s2", "u2", schema.AuthorAssistant, "  alpha\t\tdelta \r\n"),
		turnOf("s3", "u3", schema.AuthorAgent, "echo"),
	}
}

// What statsCorpus holds: 19 + 17 + 4 bytes, one newline in each of the first
// two turns, and the words alpha, bravo, charlie, alpha, delta, echo.
const (
	statsBytes = 40
	statsLines = 2
	statsWords = 6
)

func TestTheScanReportsTheTextItRead(t *testing.T) {
	corpus := statsCorpus()
	res := Search(corpus, Query{Text: "alpha", CountWords: true})

	if len(res.Hits) == 0 {
		t.Fatalf("the query found nothing, so this measures the miss path — the fixture is wrong")
	}
	if res.TurnsScanned != len(corpus) {
		t.Fatalf("scanned %d turns, want all %d — the fixture is wrong", res.TurnsScanned, len(corpus))
	}
	assertWork(t, res, statsBytes, statsLines, statsWords)
	if res.Passes != 1 {
		t.Errorf("reported %d passes over the corpus, want 1 for a search with hits", res.Passes)
	}
	if !res.WordsCounted {
		t.Errorf("reported WordsCounted false for a search that was asked to count words")
	}
}

// TestLinesAndWordsAreOnlyCountedWhenAsked pins the opt-in half of the
// contract. Bytes are a single add and are always reported; lines and words
// each need a pass over the text, which measured too expensive to charge every
// search for, so both wait to be asked. The zero has to be distinguishable from
// text that genuinely holds no lines or words, which is what WordsCounted is
// for.
func TestLinesAndWordsAreOnlyCountedWhenAsked(t *testing.T) {
	res := Search(statsCorpus(), Query{Text: "alpha"})

	if res.WordsCounted {
		t.Errorf("reported WordsCounted true for a search that never asked for words")
	}
	assertWork(t, res, statsBytes, 0, 0)
}

// TestATurnOutsideTheSearchedTiersIsNotCounted holds the counters to exactly the
// turns TurnsScanned covers. A byte figure that quietly included the tool-result
// tier would overstate a default search several-fold, which is the same untruth
// the coverage line exists to prevent.
func TestATurnOutsideTheSearchedTiersIsNotCounted(t *testing.T) {
	corpus := []schema.Turn{
		tierTurn("s1", "u1", schema.TierConversation, "alpha in conversation"),
		tierTurn("s1", "u2", schema.TierResult, "alpha in a tool result\nwith a second line"),
	}
	conversation := int64(len(corpus[0].Text))
	result := int64(len(corpus[1].Text))

	narrow := Search(corpus, Query{Text: "alpha", CountWords: true})
	if narrow.TurnsScanned != 1 {
		t.Fatalf("the default search scanned %d turns, want the conversation turn alone", narrow.TurnsScanned)
	}
	assertWork(t, narrow, conversation, 0, 3)

	wide := Search(corpus, Query{Text: "alpha", Tiers: []schema.Tier{schema.TierConversation, schema.TierResult}, CountWords: true})
	if wide.TurnsScanned != 2 {
		t.Fatalf("the two-tier search scanned %d turns, want both", wide.TurnsScanned)
	}
	assertWork(t, wide, conversation+result, 1, 3+9)
}

// TestATurnTheKeepPredicateRejectedIsNotCounted is the same claim for the other
// filter, which narrows a search without narrowing what Turns reports.
func TestATurnTheKeepPredicateRejectedIsNotCounted(t *testing.T) {
	corpus := statsCorpus()
	assistant := corpus[1]

	res := Search(corpus, Query{
		Text:       "alpha",
		CountWords: true,
		Keep:       func(tn *schema.Turn) bool { return tn.Author == schema.AuthorAssistant },
	})
	if res.TurnsScanned != 1 {
		t.Fatalf("scanned %d turns, want the one assistant turn", res.TurnsScanned)
	}
	if res.Turns != len(corpus) {
		t.Fatalf("saw %d turns, want all %d — Keep narrows the search, not the corpus", res.Turns, len(corpus))
	}
	// "  alpha\t\tdelta \r\n": one newline, and the words alpha and delta.
	assertWork(t, res, int64(len(assistant.Text)), 1, 2)
}

// TestAnExcludedTurnStillCountsAsRead separates the two things a --not term
// does. It removes a turn from the answer; it does not un-read it, and the whole
// cost of reading it was already paid by the time the exclusion was tested.
func TestAnExcludedTurnStillCountsAsRead(t *testing.T) {
	corpus := []schema.Turn{
		turnOf("s1", "u1", schema.AuthorHuman, "alpha on its own"),
		turnOf("s2", "u2", schema.AuthorAssistant, "alpha beside bravo"),
	}
	both := int64(len(corpus[0].Text) + len(corpus[1].Text))

	res := Search(corpus, Query{Text: "alpha", Not: []string{"bravo"}, CountWords: true})
	for _, h := range res.Hits {
		if h.Session == "s2" {
			t.Fatalf("the excluded turn came back as a hit — the fixture is wrong")
		}
	}
	if len(res.Hits) == 0 {
		t.Fatalf("nothing matched, so the exclusion is not what removed the second turn")
	}
	if res.TurnsScanned != 2 {
		t.Fatalf("scanned %d turns, want both — an excluded turn is still a scanned turn", res.TurnsScanned)
	}
	assertWork(t, res, both, 0, 4+3)
}

// TestFoldingDoesNotMoveTheCounts backs the decision to count lines and words
// over the folded buffer rather than the original text. That is only sound
// because fold preserves every byte position, so text that folding actually
// changes — upper case, and runes whose lower case is the same width — has to
// come out with the same figures.
func TestFoldingDoesNotMoveTheCounts(t *testing.T) {
	const text = "ALPHA Ünïcödé\nSECOND LINE"
	corpus := []schema.Turn{turnOf("s1", "u1", schema.AuthorHuman, text)}

	res := Search(corpus, Query{Text: "alpha", CountWords: true})
	if len(res.Hits) == 0 {
		t.Fatalf("the query did not match the upper-case text, so no fold was exercised")
	}
	assertWork(t, res, int64(len(text)), 1, 4)
}

// TestAQueryWithNoTermsCountsTheSameAsOneWithThem covers the path `turns` and
// `when` take, which never folds anything and so counts over the original text.
// Two counting paths that could disagree is the defect worth testing for; the
// figures have to be the same either way.
func TestAQueryWithNoTermsCountsTheSameAsOneWithThem(t *testing.T) {
	corpus := statsCorpus()

	bare := Search(corpus, Query{CountWords: true})
	if len(bare.Match.Terms) != 0 {
		t.Fatalf("the query compiled to %d terms, so it does not exercise the no-fold path", len(bare.Match.Terms))
	}
	assertWork(t, bare, statsBytes, statsLines, statsWords)
	if bare.Passes != 1 {
		t.Errorf("reported %d passes, want 1", bare.Passes)
	}
}

// TestAMissWalksTheCorpusTwiceAndSaysSo pins the pass count to the work it
// stands for. A miss reads the corpus again to explain itself, and reporting one
// pass while charging for two walks of bytes would make the two figures
// contradict each other.
func TestAMissWalksTheCorpusTwiceAndSaysSo(t *testing.T) {
	corpus := statsCorpus()

	res := Search(corpus, Query{Text: "jabberwock", CountWords: true})
	if len(res.Hits) != 0 || len(res.Terms) == 0 {
		t.Fatalf("found %d hits and %d term reports, so the survey did not run",
			len(res.Hits), len(res.Terms))
	}
	if res.Passes != 2 {
		t.Errorf("reported %d passes, want 2 — the hit walk plus the survey", res.Passes)
	}
	assertWork(t, res, 2*statsBytes, 2*statsLines, 2*statsWords)
}

// TestASkippedSurveyIsOnePass is the control for the test above: with the survey
// declined, the same missing term reads the corpus once and says so.
func TestASkippedSurveyIsOnePass(t *testing.T) {
	res := Search(statsCorpus(), Query{Text: "jabberwock", NearbyMax: -1, CountWords: true})

	if len(res.Terms) != 0 {
		t.Fatalf("the survey produced %d reports despite being declined", len(res.Terms))
	}
	if res.Passes != 1 {
		t.Errorf("reported %d passes, want 1", res.Passes)
	}
	assertWork(t, res, statsBytes, statsLines, statsWords)
}

// TestAMultiTermMissChargesForEveryWalkAndStillReportsTwoPasses pins the shape
// where the bytes and the passes deliberately disagree. A miss carrying more
// than one term reads the corpus three times — once for hits, once to count what
// each term carries on its own, once to gather suggestions — and BytesScanned
// charges for all three. Passes stays at two, because it counts the readings the
// coverage line explains: the search, and the survey that went back to explain
// it. Adding or losing a walk moves the bytes without moving the passes, so only
// asserting both together catches it.
func TestAMultiTermMissChargesForEveryWalkAndStillReportsTwoPasses(t *testing.T) {
	const walks = 3
	for _, c := range []struct {
		name string
		q    Query
		// term and turns are a count only the counting walk can produce, which
		// is what tells a skipped counting walk apart from a cheaper one.
		term  string
		turns int
	}{
		// No turn carries either term, so both are owed suggestions.
		{"neither term carried", Query{Text: "jabberwock frumious", CountWords: true}, "jabberwock", 0},
		// One term is carried and the other is not, so the search misses only
		// because every term is required.
		{"one term carried, both required", Query{Text: "alpha jabberwock", AllTerms: true, CountWords: true}, "alpha", 2},
	} {
		t.Run(c.name, func(t *testing.T) {
			res := Search(statsCorpus(), c.q)

			if len(res.Hits) != 0 {
				t.Fatalf("found %d hits, so this does not measure the miss path — the fixture is wrong", len(res.Hits))
			}
			if len(res.Terms) != 2 {
				t.Fatalf("the survey reported on %d terms, want both — the fixture is wrong", len(res.Terms))
			}
			if got := report(t, res, c.term).Turns; got != c.turns {
				t.Fatalf("counted %d turns carrying %q, want %d — the fixture is wrong", got, c.term, c.turns)
			}
			assertWork(t, res, walks*statsBytes, walks*statsLines, walks*statsWords)
			if res.Passes != 2 {
				t.Errorf("reported %d passes, want 2 — the survey walks twice but explains itself once", res.Passes)
			}
		})
	}
}

// TestShardingDoesNotChangeTheWorkCounters is the counters' half of the argument
// that cutting the corpus into ranges is invisible. They merge by addition, so a
// range whose totals were dropped or overwritten would under-report — and the
// hit path and the miss path merge separately, so both shapes are exercised.
func TestShardingDoesNotChangeTheWorkCounters(t *testing.T) {
	corpus := shardCorpus(4000)
	for _, q := range []Query{
		{Text: "alpha", CountWords: true},
		{Text: "alpha bravo", CountWords: true},
		{Text: "jabberwock", CountWords: true},
		{Text: "alpha jabberwock", AllTerms: true, CountWords: true},
		{CountWords: true},
	} {
		t.Run(q.Text+"/all-terms="+boolText(q.AllTerms), func(t *testing.T) {
			want := searchWith(t, corpus, q, len(corpus)+1)
			if want.BytesScanned == 0 {
				t.Fatalf("the single pass read no bytes — the fixture is wrong")
			}
			for _, per := range []int{1, 7, 64, 601} {
				got := searchWith(t, corpus, q, per)
				switch {
				case got.BytesScanned != want.BytesScanned:
					t.Errorf("%d turns per range read %d bytes, want %d", per, got.BytesScanned, want.BytesScanned)
				case got.LinesScanned != want.LinesScanned:
					t.Errorf("%d turns per range read %d lines, want %d", per, got.LinesScanned, want.LinesScanned)
				case got.WordsScanned != want.WordsScanned:
					t.Errorf("%d turns per range read %d words, want %d", per, got.WordsScanned, want.WordsScanned)
				case got.Passes != want.Passes:
					t.Errorf("%d turns per range took %d passes, want %d", per, got.Passes, want.Passes)
				}
			}
		})
	}
}

// budgetCorpus repeats one turn, so the totals over any prefix of it are a
// multiplication rather than a sum anyone has to trust. Its length is set by
// what it has to prove: the byte budget has to run out partway through, with
// enough turns after that point for the hit walk's ranges to straddle it.
const (
	budgetTurnText  = "walk value return\npath offset reader window"
	budgetTurnBytes = 43
	budgetTurnLines = 1
	budgetTurnWords = 7
	budgetTurns     = 200

	// budgetCovered is how many turns the shrunk budget pays for. The budget is
	// tested before a turn is read, so paying for exactly this many turns buys
	// those and stops.
	budgetCovered = 100
)

func budgetCorpus() []schema.Turn {
	out := make([]schema.Turn, 0, budgetTurns)
	for i := range budgetTurns {
		out = append(out, turnOf("s1", fmt.Sprintf("u%03d", i), schema.AuthorAssistant, budgetTurnText))
	}
	return out
}

// TestABudgetTruncatedSurveyCountsTheSameHoweverTheCorpusIsCut carries the
// sharding parity claim into the one walk that does not read the whole corpus.
// The suggestion walk stops where the byte budget runs out, and where it stops
// is settled over the whole corpus before any range is cut — so the bytes it
// charges must not move with the range size either. A budget spent range by
// range would run out at a different turn on a machine with a different core
// count, and these figures would follow it.
//
// The totals are absolute rather than a comparison against the single pass,
// because a truncation that dropped its work entirely would be consistent across
// range sizes and satisfy a parity-only test.
func TestABudgetTruncatedSurveyCountsTheSameHoweverTheCorpusIsCut(t *testing.T) {
	corpus := budgetCorpus()

	restore := nearbyBudget
	nearbyBudget = budgetCovered * budgetTurnBytes
	t.Cleanup(func() { nearbyBudget = restore })
	if nearbyBudget >= budgetTurns*budgetTurnBytes {
		t.Fatalf("the budget pays for the whole corpus, so nothing is truncated — the fixture is wrong")
	}

	for _, c := range []struct {
		name string
		q    Query
		// read is how many turns the query's walks read between them.
		read int64
	}{
		// The hit walk over the whole corpus, then the suggestion walk over what
		// the budget bought. The counting walk is skipped: a single term that
		// found nothing is already known to be carried by no turn.
		{"one term", Query{Text: "jabberwock", CountWords: true}, budgetTurns + budgetCovered},
		// The same plus the counting walk, which is exhaustive because its
		// per-term counts are a claim about the corpus rather than an offer — so
		// the budget truncates one of the survey's two walks and not the other.
		{"two terms", Query{Text: "jabberwock frumious", CountWords: true}, 2*budgetTurns + budgetCovered},
	} {
		for _, per := range []int{len(corpus) + 1, 1, 7, 33, 64} {
			t.Run(fmt.Sprintf("%s/%d-per-range", c.name, per), func(t *testing.T) {
				res := searchWith(t, corpus, c.q, per)
				if len(res.Hits) != 0 || len(res.Terms) == 0 {
					t.Fatalf("found %d hits and %d term reports, so the survey did not run",
						len(res.Hits), len(res.Terms))
				}
				assertWork(t, res, c.read*budgetTurnBytes, c.read*budgetTurnLines, c.read*budgetTurnWords)
				if res.Passes != 2 {
					t.Errorf("reported %d passes, want 2", res.Passes)
				}
			})
		}
	}
}

// TestCountWordsCountsWordStarts holds the word rule to its definition: a word
// begins at a non-whitespace byte whose predecessor is whitespace or the start
// of the text, where whitespace is space, tab, newline and carriage return.
// Punctuation is inside a word, because a word here is what a reader would
// count, not what the tokenizer would offer as a query.
func TestCountWordsCountsWordStarts(t *testing.T) {
	for _, c := range []struct {
		text string
		want int64
	}{
		{"", 0},
		{"alpha", 1},
		{"   ", 0},
		{"\n\n\n", 0},
		{" alpha ", 1},
		{"alpha bravo", 2},
		{"  alpha\t\tbravo\r\n", 2},
		{"a b c d", 4},
		{"alpha\nbravo\ncharlie", 3},
		{"punctuation, is-not a separator", 4},
		{"\ttrailing tab\t", 2},
		{"Ünïcödé counts as one", 4},
	} {
		if got := countWords(c.text); got != c.want {
			t.Errorf("countWords(%q) = %d, want %d", c.text, got, c.want)
		}
		if got := countWords([]byte(c.text)); got != c.want {
			t.Errorf("countWords([]byte(%q)) = %d, want %d", c.text, got, c.want)
		}
	}
}

func assertWork(t *testing.T, res Result, bytes, lines, words int64) {
	t.Helper()
	if res.BytesScanned != bytes {
		t.Errorf("read %d bytes, want %d", res.BytesScanned, bytes)
	}
	if res.LinesScanned != lines {
		t.Errorf("read %d lines, want %d", res.LinesScanned, lines)
	}
	if res.WordsScanned != words {
		t.Errorf("read %d words, want %d", res.WordsScanned, words)
	}
}

func tierTurn(session, uuid string, tier schema.Tier, text string) schema.Turn {
	turn := turnOf(session, uuid, schema.AuthorAssistant, text)
	turn.Tier = tier
	return turn
}

func boolText(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// statsFixtureIsWhatItClaims guards the hand-written totals above against an
// edit to the fixture text that forgets to move them.
func TestStatsFixtureIsWhatItClaims(t *testing.T) {
	var bytes, lines, words int64
	for _, turn := range statsCorpus() {
		bytes += int64(len(turn.Text))
		lines += int64(strings.Count(turn.Text, "\n"))
		words += int64(len(strings.FieldsFunc(turn.Text, wordSeparator)))
	}
	if bytes != statsBytes || lines != statsLines || words != statsWords {
		t.Errorf("the fixture holds %d bytes, %d lines and %d words; the constants say %d, %d and %d",
			bytes, lines, words, statsBytes, statsLines, statsWords)
	}
}

// TestBudgetFixtureIsWhatItClaims guards the per-turn figures the budget test
// multiplies, and the repetition that makes multiplying them valid.
func TestBudgetFixtureIsWhatItClaims(t *testing.T) {
	corpus := budgetCorpus()
	if len(corpus) != budgetTurns {
		t.Fatalf("the fixture holds %d turns; the constant says %d", len(corpus), budgetTurns)
	}
	for _, turn := range corpus {
		bytes := int64(len(turn.Text))
		lines := int64(strings.Count(turn.Text, "\n"))
		words := int64(len(strings.FieldsFunc(turn.Text, wordSeparator)))
		if bytes != budgetTurnBytes || lines != budgetTurnLines || words != budgetTurnWords {
			t.Fatalf("a turn holds %d bytes, %d lines and %d words; the constants say %d, %d and %d",
				bytes, lines, words, budgetTurnBytes, budgetTurnLines, budgetTurnWords)
		}
	}
}

// wordSeparator states the word rule independently of countWords, so the fixture
// checks measure the fixture rather than confirming the implementation against
// itself.
func wordSeparator(r rune) bool { return r == ' ' || r == '\t' || r == '\n' || r == '\r' }
