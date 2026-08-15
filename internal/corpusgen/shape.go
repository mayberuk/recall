package corpusgen

import "github.com/mayberuk/recall/internal/schema"

// TierShape is one tier's share of a session store and the mean size of a turn
// in it. The shares are of stripped turns and stripped text, which is what a
// search reads — not of the JSONL on disk, five sixths of which never becomes a
// turn at all.
type TierShape struct {
	Tier          schema.Tier
	TurnShare     float64
	ByteShare     float64
	MeanTurnBytes int
}

// measured is a working session store as internal/strip read it on 2026-08-15:
// 317,643 records over 1,402 MB of JSONL, stripped to the turns below. Doctor
// reports fewer turns from the same store because it collapses the records
// written to two files; these figures are pre-dedup, which is the basis the
// benchmark report measures a generated corpus on.
var measured = []struct {
	tier  schema.Tier
	turns int
	mib   float64
}{
	{schema.TierConversation, 38709, 39.2},
	{schema.TierInvocation, 78375, 20.6},
	{schema.TierResult, 76522, 184.3},
}

// RealStore is the distribution the generator aims at. Tool output is four
// fifths of a real store's turns and three quarters of its bytes, and none of
// it is searched by default: a corpus without that asymmetry makes an all-tier
// search cost what a conversation-tier one does, which is the one thing a
// benchmark of this tool must not claim.
func RealStore() []TierShape {
	var turns int
	var mib float64
	for _, m := range measured {
		turns += m.turns
		mib += m.mib
	}
	out := make([]TierShape, 0, len(measured))
	for _, m := range measured {
		out = append(out, TierShape{
			Tier:          m.tier,
			TurnShare:     float64(m.turns) / float64(turns),
			ByteShare:     m.mib / mib,
			MeanTurnBytes: int(m.mib * (1 << 20) / float64(m.turns)),
		})
	}
	return out
}

// What the filler sizes itself against, resolved once.
var (
	convTarget = tierTarget(schema.TierConversation)
	invTarget  = tierTarget(schema.TierInvocation)
	resTarget  = tierTarget(schema.TierResult)
)

func tierTarget(tier schema.Tier) TierShape {
	for _, s := range RealStore() {
		if s.Tier == tier {
			return s
		}
	}
	panic("corpusgen: no measurement for the " + string(tier) + " tier")
}
