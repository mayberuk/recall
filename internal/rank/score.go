package rank

import "github.com/mayberuk/recall/internal/schema"

// Shrinkage is the k in hits/(turns+k). It is not a tuning knob: it is the
// number of empty turns a session is charged before its rate is believed, and
// it caps what one hit can earn at 1/k. Raw density fails on the contract's own
// wallet query, where one passing mention in 13 turns (0.0769) outranks the
// 554-hit session (0.0697), so k must satisfy 1/(1+k) < 554/7944 — k > 13.34.
const Shrinkage = 20

// Weights that separate what a session is about from what was injected into it.
// The measured pathology: 479 of 629 hits on one build-related query were the
// skill preamble "Base directory for this skill: …/testbuild", machine text
// pasted into every session that ever used that skill, which therefore matches
// most build queries in most sessions at once. Nothing is filtered — every hit
// is still returned and still counted — the injected copies just stop
// outranking the prose.
const (
	systemWeight = 0.25 // machine text injected into the session, not said in it
	insideWeight = 0.30 // the term sits inside a longer word: "no" in "know"
	prefixWeight = 0.85 // the term starts a word: "wallet" in "wallets"
)

// Score is a query's concentration in a session: matched turns over
// turns+Shrinkage, each turn weighed by what it is worth.
//
// The numerator counts turns rather than occurrences because one long turn
// carrying six query terms is one place the topic was discussed, not six.
// A minimum-hits floor was the alternative and is rejected as query-scale
// dependent — a rare token whose best session holds two hits puts every result
// below any fixed floor, leaving the rule nothing to say. Shrinkage subsumes a
// floor, since signal/(turns+k) can never exceed signal/k, and it has no cliff.
func Score(signal float64, turns int) float64 {
	if signal <= 0 {
		return 0
	}
	if turns < 0 {
		turns = 0
	}
	return signal / float64(turns+Shrinkage)
}

// TurnScale is the turn length at which a match counts half. It is the same
// shrinkage idea as the session denominator, applied inside a turn: a 20 KB
// compact-continuation summary carries most query terms because it carries most
// words, and a 900-byte paragraph that carries them is about them.
const TurnScale = 4000

// Signal is what one matched turn is worth: how much of the query it carries,
// discounted for machine-injected text, for matches that landed inside a longer
// word, and for turns long enough to carry anything.
func Signal(h schema.Hit) float64 {
	terms := h.Terms
	if terms < 1 {
		terms = 1
	}
	return float64(terms) * authorWeight(h.Author) * kindWeight(h.Match) * lengthWeight(len(h.Text))
}

func lengthWeight(n int) float64 {
	if n <= 0 {
		return 1
	}
	return TurnScale / float64(TurnScale+n)
}

func authorWeight(a schema.Author) float64 {
	if a == schema.AuthorSystem {
		return systemWeight
	}
	return 1
}

func kindWeight(k schema.MatchKind) float64 {
	switch k {
	case schema.MatchInside:
		return insideWeight
	case schema.MatchPrefix:
		return prefixWeight
	default:
		return 1
	}
}
