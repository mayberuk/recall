package scan

// SynonymsVersion names the table's build in the coverage footer, so an
// answer that used a substitution is traceable to the table that produced it
// even when it was read from a different build than the one that ran it.
const SynonymsVersion = 1

// synonymPairs is the curated, shipped synonym table. A per-corpus or learned
// table is rejected outright: it would make one machine's answer
// unreproducible on another, and computing it needs a counting pass over the
// corpus — an index by another name.
//
// Every pair earns its place by what substring matching cannot already do:
// "auth" needs no entry against "authentication" because it is already a
// prefix of it, but the reverse does, and a handful of pairs here — "msg",
// "pkg", "docs", "cfg", "tmp", "utils" — are not even a prefix of their long
// form, so neither direction is free. The table stays curated software
// shorthand, the kind a coding-agent transcript actually uses, not a general
// thesaurus.
var synonymPairs = [][2]string{
	{"authentication", "auth"},
	{"database", "db"},
	{"repository", "repo"},
	{"configuration", "config"},
	{"configuration", "cfg"},
	{"environment", "env"},
	{"directory", "dir"},
	{"dependency", "dep"},
	{"parameter", "param"},
	{"argument", "arg"},
	{"message", "msg"},
	{"package", "pkg"},
	{"function", "func"},
	{"reference", "ref"},
	{"identifier", "id"},
	{"application", "app"},
	{"temporary", "tmp"},
	{"initialize", "init"},
	{"utility", "util"},
	{"utilities", "utils"},
	{"specification", "spec"},
	{"implementation", "impl"},
	{"documentation", "docs"},
	{"development", "dev"},
	{"production", "prod"},
	{"request", "req"},
	{"response", "resp"},
}

// synonyms indexes synonymPairs both ways: each key maps to every counterpart
// declared for it, built once at package init so a lookup is the one map hit
// the hit-path budget allows.
var synonyms = func() map[string][]string {
	m := make(map[string][]string, len(synonymPairs)*2)
	for _, p := range synonymPairs {
		m[p[0]] = append(m[p[0]], p[1])
		m[p[1]] = append(m[p[1]], p[0])
	}
	return m
}()

// synonymsFor returns the shipped table's counterparts for term, or nil if
// term is not an exact key. term must already be folded, the same spelling
// newTerm compares it under.
func synonymsFor(term string) []string {
	return synonyms[term]
}

// synonymExpansions is the coverage-footer view of every term whose table
// counterparts — added in newTerm, at compile time, not the miss path —
// reached this search's returned turns.
//
// m.terms is read rather than re-derived: alt is set only by newTerm's table
// lookup here, because widen builds a clone to add its own and never writes
// through to the matcher a completed search settled against.
func synonymExpansions(m *matcher, carried []string) []Expansion {
	if len(carried) == 0 {
		return nil
	}
	held := make(map[string]bool, len(carried))
	for _, c := range carried {
		held[c] = true
	}
	var out []Expansion
	for i := range m.terms {
		t := &m.terms[i]
		if len(t.alt) == 0 || !held[t.text] {
			continue
		}
		vars := make([]string, len(t.alt))
		for j := range t.alt {
			vars[j] = string(t.alt[j].needle)
		}
		out = append(out, Expansion{Term: t.text, Variants: vars, Synonym: true})
	}
	return out
}
