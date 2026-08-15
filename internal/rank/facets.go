package rank

import (
	"cmp"
	"slices"

	"github.com/mayberuk/recall/internal/schema"
)

// Facet is one value of a facet field: how many deduped hits carry it and how
// many sessions those hits fall in. Value is the field as it appears, empty
// string included — rank does not invent a label for a field a record omitted.
type Facet struct {
	Value    string
	Hits     int
	Sessions int
}

// Facets summarises the ranked hits over the fields a hit carries. The richer
// facets the raw records hold — pr-link, attributionSkill/attributionPlugin,
// custom-title/ai-title, relocated — are absent from the pinned Turn schema and
// so cannot reach here.
type Facets struct {
	Repo   []Facet
	Author []Facet
	Tier   []Facet
	Branch []Facet
}

func facetsOf(hits []schema.Hit) Facets {
	return Facets{
		Repo:   facet(hits, func(h schema.Hit) string { return h.Repo }),
		Author: facet(hits, func(h schema.Hit) string { return string(h.Author) }),
		Tier:   facet(hits, func(h schema.Hit) string { return string(h.Tier) }),
		Branch: facet(hits, func(h schema.Hit) string { return h.Branch }),
	}
}

func facet(hits []schema.Hit, value func(schema.Hit) string) []Facet {
	type tally struct {
		hits     int
		sessions map[string]struct{}
	}
	counts := make(map[string]*tally)
	for _, h := range hits {
		v := value(h)
		t := counts[v]
		if t == nil {
			t = &tally{sessions: make(map[string]struct{})}
			counts[v] = t
		}
		t.hits++
		t.sessions[h.Session] = struct{}{}
	}

	out := make([]Facet, 0, len(counts))
	for v, t := range counts {
		out = append(out, Facet{Value: v, Hits: t.hits, Sessions: len(t.sessions)})
	}
	slices.SortFunc(out, func(a, b Facet) int {
		if d := cmp.Compare(b.Hits, a.Hits); d != 0 {
			return d
		}
		return cmp.Compare(a.Value, b.Value)
	})
	return out
}
