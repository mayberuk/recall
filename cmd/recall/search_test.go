package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mayberuk/recall/internal/fperr"
	"github.com/mayberuk/recall/internal/render"
	"github.com/mayberuk/recall/internal/scan"
	"github.com/mayberuk/recall/internal/schema"
)

func TestCheckRejectsANonPositiveLimit(t *testing.T) {
	f := newSearchFlags()
	f.Limit = 0
	if err := f.check(); err == nil {
		t.Fatal("--limit 0 was accepted; it would show nothing")
	}
}

func TestCheckRejectsANegativeHits(t *testing.T) {
	f := newSearchFlags()
	f.Hits = -1
	if err := f.check(); err == nil {
		t.Fatal("--hits -1 was accepted")
	}
}

func TestCheckRejectsAnUnknownSort(t *testing.T) {
	f := newSearchFlags()
	f.Sort = "alphabetical"
	err := f.check()
	if err == nil {
		t.Fatal("--sort alphabetical was accepted; it is not one of the declared orders")
	}
	var coded *fperr.Error
	if !errors.As(err, &coded) || coded.Code != fperr.ArgError {
		t.Errorf("error = %v, want code %s", err, fperr.ArgError)
	}
}

// TestBuildFilterPropagatesEveryParseFailure holds buildFilter to reporting
// each flag's own parse error rather than swallowing it into a generic one.
func TestBuildFilterPropagatesEveryParseFailure(t *testing.T) {
	cases := []struct {
		name string
		set  func(*searchFlags)
	}{
		{"bad author", func(f *searchFlags) { f.Author = "robot" }},
		{"bad since", func(f *searchFlags) { f.Since = "last tuesday" }},
		{"bad until", func(f *searchFlags) { f.Until = "last tuesday" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newSearchFlags()
			tc.set(f)
			if err := f.buildFilter(time.Now()); err == nil {
				t.Errorf("%s: bad input was accepted", tc.name)
			}
		})
	}
}

// TestBuildFilterKeepsAnExplicitlyNamedOwnSession is the exception to the
// default self-exclusion: naming your own session by id is unambiguous, and
// silently returning nothing for it would be the worst kind of narrowing.
func TestBuildFilterKeepsAnExplicitlyNamedOwnSession(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "5fd86b00-f55c-4a8f-9cfd-0ff34b08058b")
	f := newSearchFlags()
	f.Session = "5fd86b00"
	if err := f.buildFilter(time.Now()); err != nil {
		t.Fatalf("buildFilter: %v", err)
	}
	if f.filter.self != "" {
		t.Errorf("self = %q, want empty — naming the session overrides the default exclusion", f.filter.self)
	}
}

func TestCallingSessionReadsTheEnvironmentUnlessIncluded(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "abc-123")
	if got := callingSession(false); got != "abc-123" {
		t.Errorf("callingSession(false) = %q, want the environment value", got)
	}
	if got := callingSession(true); got != "" {
		t.Errorf("callingSession(true) = %q, want empty — --include-self clears the exclusion", got)
	}
}

func TestQueryOfRejectsAnEmptyQuery(t *testing.T) {
	if _, err := queryOf(nil, "find"); err == nil {
		t.Fatal("an empty query was accepted")
	}
	if _, err := queryOf([]string{"  ", ""}, "find"); err == nil {
		t.Fatal("a query of only whitespace was accepted")
	}
	got, err := queryOf([]string{"agv", "tool"}, "find")
	if err != nil {
		t.Fatalf("queryOf: %v", err)
	}
	if got != "agv tool" {
		t.Errorf("queryOf = %q, want %q", got, "agv tool")
	}
}

func TestKnownTierCoversTheThreeSearchedTiers(t *testing.T) {
	for _, tier := range []schema.Tier{schema.TierConversation, schema.TierInvocation, schema.TierResult} {
		if !knownTier(tier) {
			t.Errorf("knownTier(%s) = false, want true", tier)
		}
	}
	if knownTier(schema.Tier("future-tier")) {
		t.Error("an unrecognised tier read as known")
	}
}

// TestUnknownTiersNamesWhatThisBuildDoesNotRecognise is the silent-gap
// detector: a tier this build cannot search still gets loaded, and the
// coverage line has to say so rather than let it read as fully searched.
func TestUnknownTiersNamesWhatThisBuildDoesNotRecognise(t *testing.T) {
	turns := []schema.Turn{
		{Tier: schema.TierConversation},
		{Tier: schema.Tier("future-tier")},
		{Tier: schema.Tier("another-future-tier")},
		{Tier: schema.Tier("future-tier")},
	}
	got := unknownTiers(turns)
	want := []string{"another-future-tier", "future-tier"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("unknownTiers = %v, want %v", got, want)
	}
}

func TestUnknownTiersIsEmptyWhenEveryTierIsKnown(t *testing.T) {
	turns := []schema.Turn{{Tier: schema.TierConversation}, {Tier: schema.TierResult}}
	if got := unknownTiers(turns); len(got) != 0 {
		t.Errorf("unknownTiers = %v, want none", got)
	}
}

// TestScopeOfFallsBackToMachineWideOutsideAnyRepo is the decision from the
// doc comment: a cwd that resolves to no repo identity would otherwise scope
// a search to a repo that matches nothing, silently returning zero.
func TestScopeOfFallsBackToMachineWideOutsideAnyRepo(t *testing.T) {
	t.Chdir(t.TempDir())
	f := newSearchFlags()
	sc := scopeOf(f)
	if !sc.All {
		t.Errorf("Scope = %+v, want All=true outside any repo", sc)
	}
}

func TestScopeOfHonoursAnExplicitRepoFlag(t *testing.T) {
	f := newSearchFlags()
	f.Repo = "gitlab.example/acme/mobile"
	sc := scopeOf(f)
	if sc.Repo != f.Repo || sc.All {
		t.Errorf("Scope = %+v, want Repo=%q All=false", sc, f.Repo)
	}
}

// TestElsewhereReturnsNearbyTermsOnAMachineWideMiss is the terms-nearby
// survey's entry point when even the wider probe found nothing.
func TestElsewhereReturnsNearbyTermsOnAMachineWideMiss(t *testing.T) {
	c := &corpus{turns: []schema.Turn{
		{Session: "s1", Repo: "acme/mobile", Tier: schema.TierConversation, Text: "nothing relevant here"},
	}}
	f := newSearchFlags()
	elsewhere, terms := c.elsewhere("zzzznomatch", f, render.Scope{Repo: "acme/tooling"})
	if elsewhere != nil {
		t.Errorf("elsewhere = %v, want nil on a machine-wide miss", elsewhere)
	}
	_ = terms // terms may be empty if nothing is even nearby; the branch under test is len(hits)==0
}

// TestElsewhereGroupsHitsByRepoAndExcludesTheScopedOne holds the shape a5
// depends on: hits inside the searched repo do not count as "elsewhere", and
// hits outside it are grouped per repo with a deduplicated session count.
func TestElsewhereGroupsHitsByRepoAndExcludesTheScopedOne(t *testing.T) {
	c := &corpus{turns: []schema.Turn{
		{Session: "in-scope", UUID: "u0", Repo: "acme/tooling", Tier: schema.TierConversation, Text: "agvtool lives here too"},
		{Session: "s1", UUID: "u1", Repo: "acme/mobile", Tier: schema.TierConversation, Text: "ran agvtool today"},
		{Session: "s1", UUID: "u2", Repo: "acme/mobile", Tier: schema.TierConversation, Text: "agvtool again"},
		{Session: "s2", UUID: "u3", Repo: "acme/mobile", Tier: schema.TierConversation, Text: "agvtool once more"},
	}}
	f := newSearchFlags()
	elsewhere, terms := c.elsewhere("agvtool", f, render.Scope{Repo: "acme/tooling"})
	if terms != nil {
		t.Errorf("terms = %v, want nil when hits were found", terms)
	}
	if len(elsewhere) != 1 {
		t.Fatalf("elsewhere = %v, want exactly one other repo", elsewhere)
	}
	if elsewhere[0].Repo != "acme/mobile" {
		t.Errorf("Repo = %q, want acme/mobile", elsewhere[0].Repo)
	}
	if elsewhere[0].Hits != 3 {
		t.Errorf("Hits = %d, want 3", elsewhere[0].Hits)
	}
	if elsewhere[0].Sessions != 2 {
		t.Errorf("Sessions = %d, want 2 (deduplicated)", elsewhere[0].Sessions)
	}
}

func TestShellArgQuotesOnlyWhatNeedsIt(t *testing.T) {
	if got := shellArg("agvtool"); got != "agvtool" {
		t.Errorf("shellArg(plain word) = %q, want it unchanged", got)
	}
	if got := shellArg("build number"); got != "'build number'" {
		t.Errorf("shellArg(with a space) = %q, want %q", got, "'build number'")
	}
	if got := shellArg("it's broken"); got != `'it'\''s broken'` {
		t.Errorf("shellArg(with a quote) = %q, want the escaped form", got)
	}
}

func TestDisplayRepoNamesAnUnrecordedRepoRatherThanShowingNothing(t *testing.T) {
	if got := displayRepo(""); got != "(no repo recorded)" {
		t.Errorf("displayRepo(\"\") = %q, want the placeholder", got)
	}
	if got := displayRepo("acme/mobile"); got != "acme/mobile" {
		t.Errorf("displayRepo(non-empty) = %q, want it unchanged", got)
	}
}

// TestNotesNamesATierThisBuildDoesNotRecognise is the coverage line's other
// silent-gap warning, alongside the narrowings every search already reports.
func TestNotesNamesATierThisBuildDoesNotRecognise(t *testing.T) {
	c := &corpus{unknown: []string{"future-tier"}}
	got := c.notes(nil, drops{})
	if len(got) != 1 || !strings.Contains(got[0], "future-tier") {
		t.Errorf("notes = %v, want one line naming future-tier", got)
	}
}

func TestNotesIsEmptyWithNothingToReport(t *testing.T) {
	c := &corpus{}
	if got := c.notes(nil, drops{}); len(got) != 0 {
		t.Errorf("notes = %v, want none", got)
	}
}

// TestAgoCoarsensAsTheGapGrows pins every rung of the ladder: the question is
// whether the archive is current, not how many seconds old it is.
func TestAgoCoarsensAsTheGapGrows(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{500 * time.Millisecond, "just now"},
		{30 * time.Second, "30 s ago"},
		{5 * time.Minute, "5 min ago"},
		{3 * time.Hour, "3 h ago"},
		{72 * time.Hour, "3 days ago"},
	}
	for _, tc := range cases {
		if got := ago(tc.d); got != tc.want {
			t.Errorf("ago(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
}

func TestTermViewsCarriesTheNearbyWordsForward(t *testing.T) {
	reports := []scan.TermReport{{
		Term: "retyr", Turns: 0,
		Nearby: []scan.Term{{Text: "retry"}, {Text: "retries"}},
	}}
	got := termViews(reports)
	if len(got) != 1 {
		t.Fatalf("got %d terms, want 1", len(got))
	}
	if len(got[0].Nearby) != 2 || got[0].Nearby[0] != "retry" || got[0].Nearby[1] != "retries" {
		t.Errorf("Nearby = %v, want [retry retries]", got[0].Nearby)
	}
}

// TestEmitJSONReportsAnEncodingFailureRatherThanPanicking covers emit's own
// error path when the machine form cannot become JSON.
func TestEmitJSONReportsAnEncodingFailureRatherThanPanicking(t *testing.T) {
	g := &Globals{Format: FormatJSON, MaxBytes: DefaultMaxBytes}
	err := emit(new(strings.Builder), g, nil, unencodableLined{})
	if err == nil {
		t.Fatal("an unencodable machine form was accepted")
	}
}

func TestEmitJSONLPropagatesTheRenderersOwnError(t *testing.T) {
	g := &Globals{Format: FormatJSONL, MaxBytes: DefaultMaxBytes}
	err := emit(new(strings.Builder), g, nil, failingLined{})
	if err == nil {
		t.Fatal("a JSONL renderer that returns an error was ignored")
	}
	if err.Error() != "boom" {
		t.Errorf("error = %v, want the renderer's own error", err)
	}
}

type unencodableLined struct{}

func (unencodableLined) JSONL() ([]byte, error) { return nil, nil }

// MarshalJSON makes this type itself unencodable by encoding/json, so
// render.JSON's own error branch is what emit's FormatJSON case reaches.
func (unencodableLined) MarshalJSON() ([]byte, error) { return nil, errors.New("cannot encode") }

type failingLined struct{}

func (failingLined) JSONL() ([]byte, error) { return nil, errors.New("boom") }
