package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mayberuk/recall/internal/fixtures"
	"github.com/mayberuk/recall/internal/fperr"
	"github.com/mayberuk/recall/internal/rank"
	"github.com/mayberuk/recall/internal/render"
	"github.com/mayberuk/recall/internal/scan"
	"github.com/mayberuk/recall/internal/schema"
)

func callTurns(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := notFoundIsNotAFailure(turns(args, &out))
	return out.String(), err
}

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

// TestSearchAccumulatesBytesAndTurnsFromItsOwnScan is the base case: one
// command, one scan.Search call, so the corpus's running total is exactly
// what that one scan reported — nothing summed, nothing dropped.
func TestSearchAccumulatesBytesAndTurnsFromItsOwnScan(t *testing.T) {
	c := &corpus{
		turns: []schema.Turn{
			{Session: "s1", UUID: "u1", Repo: "acme/mobile", Tier: schema.TierConversation, Text: "agvtool ran here"},
		},
		tiers: []schema.Tier{schema.TierConversation},
	}
	f := newSearchFlags()
	f.All = true // bypass this test process's own real git scope, which would filter every synthetic turn out
	if err := f.check(); err != nil {
		t.Fatalf("check: %v", err)
	}
	s := c.search("agvtool", f, rank.Concentration)

	if s.scan.BytesScanned == 0 || s.scan.TurnsScanned == 0 {
		t.Fatalf("scan = %+v, want a real scan over the one turn, not a vacuous zero", s.scan)
	}
	if c.work.bytes != s.scan.BytesScanned {
		t.Errorf("work.bytes = %d, want %d (the scan's own BytesScanned)", c.work.bytes, s.scan.BytesScanned)
	}
	if c.work.turns != s.scan.TurnsScanned {
		t.Errorf("work.turns = %d, want %d (the scan's own TurnsScanned)", c.work.turns, s.scan.TurnsScanned)
	}
	if c.work.passes != s.scan.Passes {
		t.Errorf("work.passes = %d, want %d", c.work.passes, s.scan.Passes)
	}
}

// TestRelaxedRepoScopedSearchSumsBytesAndPassesAcrossTheWiderProbe covers a
// command that runs scan.Search twice: the scoped search finds only a partial
// match, so betterElsewhere re-probes the whole corpus, and the footer has to
// report both passes' bytes rather than the scoped pass alone.
func TestRelaxedRepoScopedSearchSumsBytesAndPassesAcrossTheWiderProbe(t *testing.T) {
	c := &corpus{
		turns: []schema.Turn{
			{Session: "s-scoped", UUID: "u1", Repo: "acme/scoped", Tier: schema.TierConversation,
				Text: "alpha shows up here on its own"},
			{Session: "s-wider", UUID: "u2", Repo: "acme/wider", Tier: schema.TierConversation,
				Text: "alpha and beta both show up here"},
		},
		tiers: []schema.Tier{schema.TierConversation},
	}
	f := newSearchFlags()
	f.Repo = "acme/scoped"
	if err := f.check(); err != nil {
		t.Fatalf("check: %v", err)
	}
	s := c.search("alpha beta", f, rank.Concentration)

	if !s.scan.Match.Relaxed() {
		t.Fatalf("Match = %+v, want a relaxed partial match to exercise betterElsewhere", s.scan.Match)
	}
	if len(s.notes) == 0 {
		t.Fatal("betterElsewhere did not fire, so this test proves nothing about the wider probe's bytes")
	}

	// betterElsewhere's own probe result never reaches the caller, so observe it
	// independently with the same query betterElsewhere issues (see search.go), rather
	// than trusting c.work — the accumulator under test — to report its own inputs back.
	wide := scan.Search(c.turns, scan.Query{
		Text:       "alpha beta",
		Tiers:      c.tiers,
		Exact:      f.Exact,
		AllTerms:   f.AllTerms,
		Not:        f.Not,
		Keep:       f.filter.keep(),
		NearbyMax:  -1,
		CountWords: f.Words,
	})

	wantPasses := s.scan.Passes + wide.Passes
	if c.work.passes != wantPasses {
		t.Errorf("work.passes = %d, want %d (scoped pass's %d plus the wider probe's %d)",
			c.work.passes, wantPasses, s.scan.Passes, wide.Passes)
	}
	wantBytes := s.scan.BytesScanned + wide.BytesScanned
	if c.work.bytes != wantBytes {
		t.Errorf("work.bytes = %d, want %d (scoped pass's %d bytes plus the wider probe's %d) — an implementation "+
			"that drops the scoped pass and counts only the wide probe would report %d here",
			c.work.bytes, wantBytes, s.scan.BytesScanned, wide.BytesScanned, wide.BytesScanned)
	}
}

// TestElsewhereMemoisesTheWholeMachineProbe is the --budget retry loop's
// invariant: fitToBudget can call elsewhere for the same query several times
// in one command, and only the first has to pay for the scan.
func TestElsewhereMemoisesTheWholeMachineProbe(t *testing.T) {
	c := &corpus{turns: []schema.Turn{
		{Session: "s1", UUID: "u1", Repo: "acme/wider", Tier: schema.TierConversation, Text: "gamma appears only over here"},
	}}
	f := newSearchFlags()
	sc := render.Scope{Repo: "acme/scoped"}

	first, firstTerms := c.elsewhere("gamma", f, sc)
	passesAfterFirst, bytesAfterFirst := c.work.passes, c.work.bytes
	if passesAfterFirst == 0 {
		t.Fatal("the first call did not record a scan")
	}

	second, secondTerms := c.elsewhere("gamma", f, sc)
	if c.work.passes != passesAfterFirst || c.work.bytes != bytesAfterFirst {
		t.Errorf("a second call for the same query re-ran the scan: passes %d -> %d, bytes %d -> %d",
			passesAfterFirst, c.work.passes, bytesAfterFirst, c.work.bytes)
	}
	if len(first) != len(second) || len(firstTerms) != len(secondTerms) {
		t.Errorf("the memoised call returned a different answer: %v/%v vs %v/%v", first, firstTerms, second, secondTerms)
	}
}

// TestWordsFlagDrivesBothLineAndWordCountsInStats holds the mapping this part
// is responsible for: scan.Result carries one WordsCounted bool for both
// counters, and coverageOf has to fan it out to render.Stats's separate
// LinesKnown and WordsKnown.
func TestWordsFlagDrivesBothLineAndWordCountsInStats(t *testing.T) {
	// One newline, hand-counted here rather than taken from a run of the scanner —
	// see internal/scan/stats_test.go's statsCorpus for the same convention.
	const turnText = "several words on this line\nand one more line without any"
	const wantLines = 1

	c := &corpus{
		turns: []schema.Turn{
			{Session: "s1", UUID: "u1", Repo: "acme/mobile", Tier: schema.TierConversation, Text: turnText},
		},
		tiers: []schema.Tier{schema.TierConversation},
	}
	f := newSearchFlags()
	f.All = true // bypass this test process's own real git scope, which would filter every synthetic turn out
	f.Words = true
	if err := f.check(); err != nil {
		t.Fatalf("check: %v", err)
	}
	s := c.search("words", f, rank.Concentration)
	if s.scan.TurnsScanned == 0 {
		t.Fatalf("scan = %+v, want a real scan over the one turn, not a vacuous zero", s.scan)
	}
	cov := c.coverageOf(s.scan, f, s.skipped, nil, s.notes...)
	if cov.Stats == nil {
		t.Fatal("Stats is nil")
	}
	if !cov.Stats.LinesKnown || !cov.Stats.WordsKnown {
		t.Errorf("Stats = %+v, want both LinesKnown and WordsKnown with --words", cov.Stats)
	}
	if cov.Stats.Words == 0 {
		t.Error("Words = 0 with --words set on a turn that has words in it")
	}
	if cov.Stats.Lines != wantLines {
		t.Errorf("Stats.Lines = %d, want %d (the one newline in the fixture turn) — LinesKnown true with Lines "+
			"left at zero would pass a test that only checks the flag", cov.Stats.Lines, wantLines)
	}

	c2 := &corpus{turns: c.turns, tiers: c.tiers}
	f2 := newSearchFlags()
	f2.All = true
	if err := f2.check(); err != nil {
		t.Fatalf("check: %v", err)
	}
	s2 := c2.search("words", f2, rank.Concentration)
	if s2.scan.TurnsScanned == 0 {
		t.Fatalf("scan = %+v, want a real scan over the one turn, not a vacuous zero", s2.scan)
	}
	cov2 := c2.coverageOf(s2.scan, f2, s2.skipped, nil, s2.notes...)
	if cov2.Stats == nil {
		t.Fatal("Stats is nil")
	}
	if cov2.Stats.LinesKnown || cov2.Stats.WordsKnown {
		t.Errorf("Stats = %+v, want neither Known flag without --words", cov2.Stats)
	}
	if cov2.Stats.Words != 0 || cov2.Stats.Lines != 0 {
		t.Errorf("Stats = %+v, want zero lines and words without --words", cov2.Stats)
	}
}

// TestElapsedIsMeasuredSinceStartedAt proves ElapsedMS is a real
// time.Since(startedAt), not a zero value that happens to be omitted.
func TestElapsedIsMeasuredSinceStartedAt(t *testing.T) {
	c := &corpus{startedAt: time.Now().Add(-5 * time.Millisecond)}
	cov := c.coverageOf(scan.Result{}, nil, drops{}, nil)
	if cov.Stats == nil {
		t.Fatal("Stats is nil")
	}
	if cov.Stats.ElapsedMS <= 0 {
		t.Errorf("ElapsedMS = %v, want greater than zero", cov.Stats.ElapsedMS)
	}
}

// TestStatsSuppressionCoversEveryOutputSurface is the RECALL_NO_STATS off
// switch, checked at every surface Coverage.Stats reaches rather than only
// at render.Coverage.Lines: --brief and --fzf build their own body
// independently of Text and JSON, so a suppression bug specific to either
// would still leak the non-deterministic elapsed figure into a comparison
// that expects byte-identical output.
//
// statsSuppressed is set directly rather than through RECALL_NO_STATS,
// because it is read from the environment once at process start — the same
// reason internal/scan's shard tests reassign minShardTurns directly instead
// of setting its environment variable.
func TestStatsSuppressionCoversEveryOutputSurface(t *testing.T) {
	c := harness(t)
	t.Chdir(c.Scratch)
	was := statsSuppressed
	t.Cleanup(func() { statsSuppressed = was })

	const statsShape = "── scanned "

	cases := []struct {
		name string
		run  func() (string, string)
	}{
		{"text", func() (string, string) {
			out, errOut, err := callFind(t, fixtures.NeedleConversation, "--all")
			if err != nil {
				t.Fatalf("find: %v", err)
			}
			return out, errOut
		}},
		{"brief", func() (string, string) {
			out, errOut, err := callFind(t, fixtures.NeedleConversation, "--all", "--brief")
			if err != nil {
				t.Fatalf("find --brief: %v", err)
			}
			return out, errOut
		}},
		{"fzf", func() (string, string) {
			out, errOut, err := callFind(t, fixtures.NeedleConversation, "--all", "--fzf")
			if err != nil {
				t.Fatalf("find --fzf: %v", err)
			}
			return out, errOut
		}},
		{"json", func() (string, string) {
			out, errOut, err := callFind(t, fixtures.NeedleConversation, "--all", "--json")
			if err != nil {
				t.Fatalf("find --json: %v", err)
			}
			return out, errOut
		}},
		{"jsonl", func() (string, string) {
			out, errOut, err := callFind(t, fixtures.NeedleConversation, "--all", "--format", "jsonl")
			if err != nil {
				t.Fatalf("find --format jsonl: %v", err)
			}
			return out, errOut
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			statsSuppressed = false
			unsuppressedOut, unsuppressedErr := tc.run()
			if !strings.Contains(unsuppressedOut+unsuppressedErr, statsShape) && !strings.Contains(unsuppressedOut+unsuppressedErr, `"stats"`) {
				t.Errorf("no stats section with statsSuppressed=false:\nstdout: %s\nstderr: %s", unsuppressedOut, unsuppressedErr)
			}

			statsSuppressed = true
			suppressedOut, suppressedErr := tc.run()
			if strings.Contains(suppressedOut, statsShape) || strings.Contains(suppressedErr, statsShape) {
				t.Errorf("statsSuppressed=true still printed a stats line:\nstdout: %s\nstderr: %s", suppressedOut, suppressedErr)
			}
			if strings.Contains(suppressedOut, `"stats"`) || strings.Contains(suppressedErr, `"stats"`) {
				t.Errorf("statsSuppressed=true still emitted a stats JSON key:\nstdout: %s\nstderr: %s", suppressedOut, suppressedErr)
			}
		})
	}
}

// TestIDsOmitsTheStatsLine holds --ids to its existing contract: session ids
// alone, with no coverage line of any kind — --ids never reaches
// Coverage.Lines, so the stats section cannot leak into it either.
func TestIDsOmitsTheStatsLine(t *testing.T) {
	c := harness(t)
	t.Chdir(c.Scratch)

	out, _, err := callFind(t, fixtures.NeedleConversation, "--all", "--ids")
	if err != nil {
		t.Fatalf("find --ids: %v", err)
	}
	if strings.Contains(out, "── scanned ") {
		t.Errorf("--ids printed a stats line:\n%s", out)
	}
	if strings.Contains(out, "── ") {
		t.Errorf("--ids printed a coverage line of any kind:\n%s", out)
	}
}

// TestStatsLineIsLastAboveTheSizeFooterOnEveryVerb is the EARS acceptance
// case for find, turns, when and show with a query: each ends its coverage
// footer with the stats line, and the byte-size footer comes after it.
func TestStatsLineIsLastAboveTheSizeFooterOnEveryVerb(t *testing.T) {
	c := harness(t)
	t.Chdir(c.Scratch)

	cases := []struct {
		verb string
		run  func() (string, error)
	}{
		{"find", func() (string, error) { s, _, e := callFind(t, fixtures.NeedleConversation, "--all"); return s, e }},
		{"turns", func() (string, error) { return callTurns(t, fixtures.NeedleConversation, "--all") }},
		{"when", func() (string, error) { return callWhen(t, fixtures.NeedleConversation, "--all") }},
		{"show", func() (string, error) { return callShow(t, fixtures.SessNeedle, fixtures.NeedleConversation) }},
	}
	for _, tc := range cases {
		t.Run(tc.verb, func(t *testing.T) {
			out, err := tc.run()
			if err != nil {
				t.Fatalf("%s: %v", tc.verb, err)
			}
			lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
			if len(lines) < 2 {
				t.Fatalf("%s: too few lines to hold a stats line and a size footer:\n%s", tc.verb, out)
			}
			sizeFooter := lines[len(lines)-1]
			statsLine := lines[len(lines)-2]
			if !strings.Contains(sizeFooter, " tokens") {
				t.Errorf("%s: last line is not the size footer: %q", tc.verb, sizeFooter)
			}
			if !strings.HasPrefix(statsLine, "── scanned ") {
				t.Errorf("%s: line above the size footer is not the stats line: %q", tc.verb, statsLine)
			}
		})
	}
}
