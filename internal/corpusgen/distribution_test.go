package corpusgen

import (
	"testing"

	"github.com/mayberuk/recall/internal/schema"
)

// tolerance is how far a generated corpus may sit from the store RealStore
// measured, as a fraction of each share. A share cannot be hit exactly: the
// generator writes whole records, a tool exchange carries two turns at once,
// and the planted needles are deliberately short turns. A twentieth covers all
// three and is still narrow enough that the generator this replaced — 99.5%
// conversation turns against a real 20% — misses it fivefold.
const tolerance = 0.05

// TestGeneratedTiersMatchTheRealStore is what the all-tier benchmark rests on.
// Every expected figure here comes from RealStore, measured by stripping a
// working session store, and none of it from a previous run of the generator:
// a corpus checked against its own output would prove determinism, which
// another test already proves, and nothing about shape.
func TestGeneratedTiersMatchTheRealStore(t *testing.T) {
	_, c := generate(t, Small())

	turns := map[schema.Tier]int{}
	bytes := map[schema.Tier]int64{}
	var totalTurns int
	var totalBytes int64
	for _, f := range readCorpus(t, c.Root) {
		for _, turn := range f.turns {
			turns[turn.Tier]++
			bytes[turn.Tier] += int64(len(turn.Text))
			totalTurns++
			totalBytes += int64(len(turn.Text))
		}
	}
	if totalTurns == 0 {
		t.Fatal("the generated corpus stripped to no turns")
	}

	for _, want := range RealStore() {
		t.Logf("%-12s %6d turns · %5.2f%% of turns (real %5.2f%%) · %5.2f%% of bytes (real %5.2f%%)",
			want.Tier, turns[want.Tier],
			100*float64(turns[want.Tier])/float64(totalTurns), 100*want.TurnShare,
			100*float64(bytes[want.Tier])/float64(totalBytes), 100*want.ByteShare)
	}
	for _, want := range RealStore() {
		t.Run(string(want.Tier), func(t *testing.T) {
			near(t, "share of turns", float64(turns[want.Tier])/float64(totalTurns), want.TurnShare)
			near(t, "share of bytes", float64(bytes[want.Tier])/float64(totalBytes), want.ByteShare)
		})
	}

	// The property the all-tier gate exists to measure: searching every tier
	// reads several times what searching the default tier reads, so the two
	// numbers in the report cannot come out the same.
	conv := bytes[schema.TierConversation]
	if conv == 0 {
		t.Fatal("the generated corpus carries no conversation turns")
	}
	near(t, "bytes an all-tier search reads per conversation-tier byte",
		float64(totalBytes)/float64(conv), 1/conversationByteShare(t))
}

func conversationByteShare(t *testing.T) float64 {
	t.Helper()
	for _, s := range RealStore() {
		if s.Tier == schema.TierConversation {
			return s.ByteShare
		}
	}
	t.Fatal("RealStore describes no conversation tier")
	return 0
}

func near(t *testing.T, what string, got, want float64) {
	t.Helper()
	slack := want * tolerance
	if got < want-slack || got > want+slack {
		t.Errorf("%s is %.4f, want %.4f ±%.0f%% (%.4f to %.4f) as measured over a real store",
			what, got, want, tolerance*100, want-slack, want+slack)
	}
}
