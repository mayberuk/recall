package main

import (
	"flag"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// docsPage is the site's documentation page. It is hand-written rather than
// generated, which buys prose that reads properly and costs the guarantee that
// it stays current. This test buys the guarantee back: the registry already
// holds every verb's flag set, so a flag that ships without a mention here
// fails the build instead of going quietly undocumented.
const docsPage = "../../site/src/content/docs.md"

// undocumented names flags the page deliberately leaves out, each with the
// reason. A flag belongs here only when documenting it would mislead, never
// because writing the line was inconvenient.
var undocumented = map[string]string{
	// The record separator for the fzf front end, not something a reader types.
	// The page documents recall-fzf, which is the thing you actually run.
	"--fzf": "an internal record format; the page documents recall-fzf instead",
	// Both exist for the test harnesses and for recovering from a half-written
	// archive. Documenting them invites reaching for them first, and a stale
	// answer is the one failure this tool exists to prevent.
	"--no-update": "a harness and recovery escape hatch, not a normal read",
	"--words":     "a second scan pass for benchmarking, priced in the footer already",
}

func TestEveryVerbAndFlagIsOnTheDocumentationPage(t *testing.T) {
	page, err := os.ReadFile(filepath.FromSlash(docsPage))
	if err != nil {
		t.Fatalf("cannot read the documentation page at %s: %v", docsPage, err)
	}
	text := string(page)

	var missing []string
	for _, verb := range verbNames() {
		if !strings.Contains(text, "recall "+verb) {
			missing = append(missing, "verb `recall "+verb+"`")
		}
		e := registry[verb]
		if e.flags == nil {
			continue
		}
		e.flags.VisitAll(func(f *flag.Flag) {
			name := "--" + f.Name
			if _, ok := undocumented[name]; ok {
				return
			}
			if !strings.Contains(text, name) {
				missing = append(missing, name+" (recall "+verb+")")
			}
		})
	}

	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("%s does not mention %d part(s) of the shipped surface:\n  %s\n\n"+
			"Document them there, or add the flag to `undocumented` in this file with the reason it is left out.",
			docsPage, len(missing), strings.Join(dedupe(missing), "\n  "))
	}
}

// TestTheUndocumentedListNamesOnlyRealFlags keeps the exemption list honest: an
// entry for a flag that no longer exists would silently widen the gate.
func TestTheUndocumentedListNamesOnlyRealFlags(t *testing.T) {
	real := map[string]bool{}
	for _, verb := range verbNames() {
		for _, name := range flagNames(verb) {
			real[name] = true
		}
	}
	for name, why := range undocumented {
		if !real[name] {
			t.Errorf("undocumented names %s (%q) but no verb defines it any more; delete the entry", name, why)
		}
		if why == "" {
			t.Errorf("undocumented[%s] carries no reason; an exemption without one is a gap", name)
		}
	}
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := in[:0]
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
