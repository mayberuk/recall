package rank

import "github.com/mayberuk/recall/internal/schema"

// One record yields several turns that share its uuid — an assistant text block
// and its thinking block — and each turn's offsets are relative to its own text,
// so uuid plus offset alone collides between them and drops a real hit.
type hitKey struct {
	session string
	uuid    string
	tier    schema.Tier
	offset  int
	length  int
	text    string
}

// Dedup returns the hits with redundant copies of a record removed, in input
// order, plus the number dropped. The key is deliberately not the uuid alone:
// 3,402 uuids on this machine carry two different sessionIds, because a fork
// rewrites the session on the records it carries forward, and collapsing those
// would delete the turn from one of the two sessions it genuinely belongs to.
func Dedup(hits []schema.Hit) ([]schema.Hit, int) {
	seen := make(map[hitKey]struct{}, len(hits))
	kept := make([]schema.Hit, 0, len(hits))
	for _, h := range hits {
		k := hitKey{h.Session, h.UUID, h.Tier, h.Offset, h.Length, h.Text}
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		kept = append(kept, h)
	}
	return kept, len(hits) - len(kept)
}
