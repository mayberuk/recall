package scan

// SynonymsVersion identifies the table build reported in the coverage footer.
const SynonymsVersion = 1

// synonymPairs is the shipped, curated synonym table — never learned from a
// corpus, so a match stays reproducible across machines without a counting
// pass over the corpus. A pair earns its place only where substring matching
// cannot already bridge it: "auth" needs no entry against "authentication"
// because it is already a prefix, but "msg", "pkg", "docs", "cfg", "tmp" and
// "utils" are not prefixes of their long forms in either direction.
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

var synonyms = func() map[string][]string {
	m := make(map[string][]string, len(synonymPairs)*2)
	for _, p := range synonymPairs {
		m[p[0]] = append(m[p[0]], p[1])
		m[p[1]] = append(m[p[1]], p[0])
	}
	return m
}()

func synonymsFor(term string) []string {
	return synonyms[term]
}

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
