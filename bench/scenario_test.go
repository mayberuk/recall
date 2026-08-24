package bench_test

import (
	"strings"
	"testing"

	"github.com/mayberuk/recall/bench"
)

// registeredVerbs are the verbs cmd/recall.Register wires up. bench cannot
// import cmd/recall without pulling the built binary's init side effects into
// the test, so the set is pinned here — the same trade TestEveryQueryMeasuresWhatItNames
// makes for query shapes.
var registeredVerbs = map[string]bool{
	"find": true, "turns": true, "show": true, "when": true, "doctor": true, "guide": true,
}

// TestScenariosNameEveryVerb is Scenarios' half of the coverage
// TestEveryQueryMeasuresWhatItNames carries for Queries: nothing else asserted
// the scenario set names every verb the report claims to measure.
func TestScenariosNameEveryVerb(t *testing.T) {
	g, err := bench.Corpus(bench.SizeSmall)
	if err != nil {
		t.Fatalf("Corpus: %v", err)
	}
	scenarios, err := bench.Scenarios(g)
	if err != nil {
		t.Fatalf("Scenarios: %v", err)
	}

	seen := map[string]bool{}
	for _, s := range scenarios {
		if len(s.Args) == 0 {
			t.Fatalf("scenario %q has no args, so it names no verb", s.Name)
		}
		seen[s.Args[0]] = true
	}
	for verb := range registeredVerbs {
		if !seen[verb] {
			t.Errorf("Scenarios named nothing for %q, so the report would not measure it", verb)
		}
	}
	for verb := range seen {
		if !registeredVerbs[verb] {
			t.Errorf("Scenarios named %q, which is not a registered verb", verb)
		}
	}
}

// TestScenarioArgsAreWellFormed checks the shape Scenario.Run needs to time
// the invocation it claims to: a name, a known verb, a working directory, and
// no empty argument that would reach exec.Command as a bare "".
func TestScenarioArgsAreWellFormed(t *testing.T) {
	g, err := bench.Corpus(bench.SizeSmall)
	if err != nil {
		t.Fatalf("Corpus: %v", err)
	}
	scenarios, err := bench.Scenarios(g)
	if err != nil {
		t.Fatalf("Scenarios: %v", err)
	}

	for _, s := range scenarios {
		if s.Name == "" {
			t.Errorf("scenario with args %v has no name", s.Args)
			continue
		}
		if len(s.Args) == 0 {
			t.Errorf("scenario %q has no args", s.Name)
			continue
		}
		if !registeredVerbs[s.Args[0]] {
			t.Errorf("scenario %q runs verb %q, which is not registered", s.Name, s.Args[0])
		}
		if !strings.HasPrefix(s.Name, s.Args[0]) {
			t.Errorf("scenario %q does not start with its own verb %q, so the report would not sort it there", s.Name, s.Args[0])
		}
		if s.Dir == "" {
			t.Errorf("scenario %q has no working directory, so it would search the operator's own session store", s.Name)
		}
		for _, a := range s.Args {
			if a == "" {
				t.Errorf("scenario %q carries an empty argument", s.Name)
			}
		}
	}
}

// TestWordsScenariosMeasureTheOptInPass pins the two scenarios this part
// added: the opt-in word-counting pass, alone and combined with --all, has to
// actually be in the set or RESULTS.md's "cheap enough" claim rests on
// nothing measured.
func TestWordsScenariosMeasureTheOptInPass(t *testing.T) {
	g, err := bench.Corpus(bench.SizeSmall)
	if err != nil {
		t.Fatalf("Corpus: %v", err)
	}
	scenarios, err := bench.Scenarios(g)
	if err != nil {
		t.Fatalf("Scenarios: %v", err)
	}

	want := map[string][]string{
		"find --words":       {"--words"},
		"find --all --words": {"--all", "--words"},
	}
	for _, s := range scenarios {
		flags, ok := want[s.Name]
		if !ok {
			continue
		}
		delete(want, s.Name)
		if len(s.Args) != 2+len(flags) {
			t.Errorf("scenario %q has args %v, want find, a term and %v", s.Name, s.Args, flags)
			continue
		}
		if got := s.Args[2:]; !equal(got, flags) {
			t.Errorf("scenario %q passes flags %v, want %v", s.Name, got, flags)
		}
	}
	for name := range want {
		t.Errorf("Scenarios has no %q scenario, so the opt-in word pass goes unmeasured", name)
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
