package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/mayberuk/recall/internal/archive"
	"github.com/mayberuk/recall/internal/fperr"
	"github.com/mayberuk/recall/internal/schema"
)

// A verb binds its own flags onto this same FlagSet later, without editing this file.
func TestGlobalsBindToAVerbFlagSet(t *testing.T) {
	g := NewGlobals()
	fs := newFlags("find")
	g.Bind(fs)

	if g.MaxBytes != DefaultMaxBytes {
		t.Errorf("default MaxBytes = %d, want %d", g.MaxBytes, DefaultMaxBytes)
	}
	if err := parseFlags(fs, []string{"--max-bytes", "4096", "--json", "wallet"}); err != nil {
		t.Fatal(err)
	}
	if g.MaxBytes != 4096 {
		t.Errorf("MaxBytes = %d, want 4096", g.MaxBytes)
	}
	if !g.JSON {
		t.Error("--json did not bind")
	}
	if got := fs.Args(); len(got) != 1 || got[0] != "wallet" {
		t.Errorf("positional args = %v, want [wallet]", got)
	}
}

func TestSearchFlagsBind(t *testing.T) {
	var s SearchFlags
	fs := newFlags("find")
	s.Bind(fs)
	if err := parseFlags(fs, []string{"--all", "--results", "--tools", "--repo", "api-server"}); err != nil {
		t.Fatal(err)
	}
	if !s.All || !s.Results || !s.Tools {
		t.Errorf("flags = %+v, want all three set", s)
	}
	if s.Repo != "api-server" {
		t.Errorf("Repo = %q, want api-server", s.Repo)
	}
}

func TestGlobalsAndSearchFlagsShareOneFlagSet(t *testing.T) {
	g := NewGlobals()
	var s SearchFlags
	fs := newFlags("find")
	g.Bind(fs)
	s.Bind(fs)
	if err := parseFlags(fs, []string{"--all", "--max-bytes", "1024"}); err != nil {
		t.Fatal(err)
	}
	if !s.All || g.MaxBytes != 1024 {
		t.Errorf("globals=%+v search=%+v", g, s)
	}
}

func TestCheckRejectsANonBoundingCap(t *testing.T) {
	for _, n := range []int64{0, -1} {
		g := &Globals{MaxBytes: n, Format: FormatText}
		err := g.Check()
		if err == nil {
			t.Fatalf("--max-bytes %d accepted; it bounds nothing", n)
		}
		var coded *fperr.Error
		if !errors.As(err, &coded) || coded.Code != fperr.ArgError {
			t.Errorf("error = %v, want code %s", err, fperr.ArgError)
		}
	}
	if err := NewGlobals().Check(); err != nil {
		t.Errorf("the default cap was rejected: %v", err)
	}
}

func TestBadFlagBecomesAnArgError(t *testing.T) {
	fs := newFlags("find")
	NewGlobals().Bind(fs)
	err := parseFlags(fs, []string{"--no-such-flag"})
	if err == nil {
		t.Fatal("an unknown flag was accepted")
	}
	var coded *fperr.Error
	if !errors.As(err, &coded) || coded.Code != fperr.ArgError {
		t.Errorf("error = %v, want code %s", err, fperr.ArgError)
	}
	if !strings.Contains(err.Error(), "run `recall find --help`") {
		t.Errorf("error does not point at the registered verb's flags: %v", err)
	}
}

// TestBadFlagOnAnUnregisteredNameStillReportsAnArgError is the branch
// flagNames takes when the FlagSet's name is not a registered verb: there is
// no flag list to append, but the error must still be reported rather than
// panicking on a nil list.
func TestBadFlagOnAnUnregisteredNameStillReportsAnArgError(t *testing.T) {
	fs := newFlags("not-a-registered-verb")
	err := parseFlags(fs, []string{"--no-such-flag"})
	if err == nil {
		t.Fatal("an unknown flag was accepted")
	}
	var coded *fperr.Error
	if !errors.As(err, &coded) || coded.Code != fperr.ArgError {
		t.Errorf("error = %v, want code %s", err, fperr.ArgError)
	}
	if strings.Contains(err.Error(), "takes:") {
		t.Errorf("an unregistered verb has no flag list to print: %v", err)
	}
}

func TestCheckRejectsANegativeBudget(t *testing.T) {
	g := &Globals{MaxBytes: DefaultMaxBytes, Format: FormatText, Budget: -1}
	err := g.Check()
	if err == nil {
		t.Fatal("a negative --budget was accepted")
	}
	var coded *fperr.Error
	if !errors.As(err, &coded) || coded.Code != fperr.ArgError {
		t.Errorf("error = %v, want code %s", err, fperr.ArgError)
	}
}

func TestCheckRejectsAnUnknownFormat(t *testing.T) {
	g := &Globals{MaxBytes: DefaultMaxBytes, Format: "yaml"}
	err := g.Check()
	if err == nil {
		t.Fatal(`--format yaml was accepted; only text, json and jsonl are declared`)
	}
	var coded *fperr.Error
	if !errors.As(err, &coded) || coded.Code != fperr.ArgError {
		t.Errorf("error = %v, want code %s", err, fperr.ArgError)
	}
}

// TestCheckRejectsJSONWithFormatJSONLBecauseTheyAreTwoAnswers holds --json
// and --format jsonl apart: JSON is one object, jsonl is one per line, and
// accepting both would silently pick one without saying which.
func TestCheckRejectsJSONWithFormatJSONLBecauseTheyAreTwoAnswers(t *testing.T) {
	g := &Globals{MaxBytes: DefaultMaxBytes, Format: FormatJSONL, JSON: true}
	if err := g.Check(); err == nil {
		t.Fatal("--json and --format jsonl were both accepted")
	}
}

func TestCheckResolvesJSONToTheJSONFormat(t *testing.T) {
	g := &Globals{MaxBytes: DefaultMaxBytes, Format: FormatText, JSON: true}
	if err := g.Check(); err != nil {
		t.Fatalf("Check: %v", err)
	}
	if g.Format != FormatJSON {
		t.Errorf("Format = %q, want %q — --json is the same narrowing as --format json", g.Format, FormatJSON)
	}
}

// TestCapPrefersTheTighterOfBudgetAndMaxBytes covers all three shapes: no
// budget at all, a budget tighter than the hard cap, and a budget looser
// than it — the hard cap always wins when the budget would loosen it.
func TestCapPrefersTheTighterOfBudgetAndMaxBytes(t *testing.T) {
	cases := []struct {
		name     string
		maxBytes int64
		budget   int
		want     int64
	}{
		{"no budget set", 1000, 0, 1000},
		{"budget tighter than the cap", 100000, 10, 40},
		{"budget looser than the cap", 100, 1000, 100},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := &Globals{MaxBytes: tc.maxBytes, Budget: tc.budget}
			if got := g.Cap(); got != tc.want {
				t.Errorf("Cap() = %d, want %d", got, tc.want)
			}
		})
	}
}

// --provider governs every searching verb now, not just doctor, and its help
// text has to name all of them rather than singling doctor out as the one
// verb that honours the flag.
func TestProviderHelpNamesEveryVerbItGoverns(t *testing.T) {
	fs := newFlags("find")
	NewGlobals().Bind(fs)
	f := fs.Lookup("provider")
	if f == nil {
		t.Fatal("--provider is not bound")
	}
	usage := f.Usage
	if strings.Contains(usage, "still read claude-code only") {
		t.Errorf("--provider help still claims find, turns, when and show ignore it: %q", usage)
	}
	for _, verb := range []string{"find", "turns", "when", "show", "doctor"} {
		if !strings.Contains(usage, verb) {
			t.Errorf("--provider help does not name %s among the verbs it governs: %q", verb, usage)
		}
	}
}

func TestNewGlobalsDefaultsProviderToAuto(t *testing.T) {
	if got := NewGlobals().Provider; got != ProviderAuto {
		t.Errorf("Provider = %q, want %q", got, ProviderAuto)
	}
}

func TestProviderBindsOntoAVerbFlagSet(t *testing.T) {
	g := NewGlobals()
	fs := newFlags("doctor")
	g.Bind(fs)
	if err := parseFlags(fs, []string{"--provider", "codex"}); err != nil {
		t.Fatal(err)
	}
	if g.Provider != "codex" {
		t.Errorf("Provider = %q, want codex", g.Provider)
	}
}

// TestCheckAcceptsAZeroValueProviderWithoutCallingSetSelection guards fact
// that archive.SetSelection("") is itself an argument error: NewGlobals's
// "auto" default and a bare zero-value Globals both have to reach Check
// without ever making that call, or every default invocation of every verb
// would fail.
func TestCheckAcceptsAZeroValueProviderWithoutCallingSetSelection(t *testing.T) {
	g := &Globals{MaxBytes: DefaultMaxBytes, Format: FormatText}
	if err := g.Check(); err != nil {
		t.Errorf("a zero-value --provider was rejected: %v", err)
	}
	if err := NewGlobals().Check(); err != nil {
		t.Errorf("the auto default --provider was rejected: %v", err)
	}
}

// TestCheckWiresAnExplicitProviderIntoTheSelection proves --provider is not
// just stored on Globals: Check hands it to archive.SetSelection, the one
// path RECALL_AGENT also resolves through, so a later archive.Select sees it
// and rejects a name with no registered provider.
func TestCheckWiresAnExplicitProviderIntoTheSelection(t *testing.T) {
	t.Cleanup(func() { _ = archive.SetSelection(string(schema.AgentClaudeCode)) })

	g := &Globals{MaxBytes: DefaultMaxBytes, Format: FormatText, Provider: "no-such-agent"}
	if err := g.Check(); err != nil {
		t.Fatalf("Check: %v", err)
	}
	_, err := archive.Select()
	if err == nil {
		t.Fatal("Select resolved an agent with no registered provider")
	}
	var coded *fperr.Error
	if !errors.As(err, &coded) || coded.Code != fperr.ArgError {
		t.Errorf("error = %v, want code %s", err, fperr.ArgError)
	}
	if !strings.Contains(err.Error(), "registered agents are") {
		t.Errorf("error does not name the registered agents: %v", err)
	}
}
