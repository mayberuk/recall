package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/mayberuk/recall/internal/archive"
	"github.com/mayberuk/recall/internal/fperr"
	"github.com/mayberuk/recall/internal/rank"
	"github.com/mayberuk/recall/internal/render"
	"github.com/mayberuk/recall/internal/repo"
	"github.com/mayberuk/recall/internal/scan"
	"github.com/mayberuk/recall/internal/schema"
	"github.com/mayberuk/recall/internal/strip"
)

// Defaults for the declared caps. They are declared rather than silent: scan
// emits one hit per occurrence and puts no ceiling on it, so a query like "the"
// yields hundreds of thousands, and a list quietly cut to ten is
// indistinguishable from a corpus that held ten.
const (
	DefaultLimit          = 10
	DefaultHitsPerSession = 3
)

// searchFlags are the flags a searching verb accepts on top of the globals.
type searchFlags struct {
	SearchFlags
	Mine     bool
	Exact    bool
	AllTerms bool
	Not      stringList
	Limit    int
	Hits     int
	Sort     string
	NoUpdate bool

	Author  string
	Branch  string
	Agent   string
	Session string
	Since   string
	Until   string

	IncludeSelf   bool
	IncludeRecall bool

	Brief bool
	IDs   bool

	filter filter
}

func newSearchFlags() *searchFlags {
	return &searchFlags{Limit: DefaultLimit, Hits: DefaultHitsPerSession}
}

// bind attaches the search flags. unit is what this verb ranks — sessions for
// find and when, passages for turns — because a flag's help text is read before
// the first invocation and a wrong noun there costs the attempt it saves.
func (s *searchFlags) bind(fs *flag.FlagSet) { s.bindFor(fs, "sessions", "session ids") }

func (s *searchFlags) bindFor(fs *flag.FlagSet, unit, ids string) {
	s.SearchFlags.Bind(fs)
	fs.BoolVar(&s.Mine, "mine", s.Mine, "only turns you typed (the same as --author human)")
	fs.BoolVar(&s.Exact, "exact", s.Exact, "match terms literally, without stem expansion")
	fs.BoolVar(&s.AllTerms, "all-terms", s.AllTerms, "require every term, returning nothing rather than the best partial match")
	fs.Var(&s.Not, "not", "skip turns carrying this term; repeatable")
	fs.IntVar(&s.Limit, "limit", s.Limit, "most "+unit+" to show")
	fs.IntVar(&s.Hits, "hits", s.Hits, "most matched turns to show per session")
	fs.StringVar(&s.Sort, "sort", s.Sort, "override the verb's order: recent")
	fs.BoolVar(&s.NoUpdate, "no-update", s.NoUpdate, "search the archive as it stands, skipping the refresh from disk")
	fs.StringVar(&s.Author, "author", s.Author, "only turns by human, assistant, agent or system")
	fs.StringVar(&s.Branch, "branch", s.Branch, "only turns recorded on this git branch")
	fs.StringVar(&s.Agent, "agent", s.Agent, "only turns from a subagent whose name contains this")
	fs.StringVar(&s.Session, "session", s.Session, "only this session, by id or unique prefix")
	fs.StringVar(&s.Since, "since", s.Since, "only turns at or after this: 2w, 3d, 12h or a date")
	fs.StringVar(&s.Until, "until", s.Until, "only turns at or before this: 2w, 3d, 12h or a date")
	fs.BoolVar(&s.IncludeSelf, "include-self", s.IncludeSelf, "include the session asking the question")
	fs.BoolVar(&s.IncludeRecall, "include-recall", s.IncludeRecall, "include recall's own recorded commands and output")
	fs.BoolVar(&s.Brief, "brief", s.Brief, "one line per session, no snippets")
	fs.BoolVar(&s.IDs, "ids", s.IDs, "print "+ids+" only, one per line")
}

func (s *searchFlags) check() error {
	if s.Limit <= 0 {
		return fperr.New(fperr.ArgError, "--limit must be positive, got %d", s.Limit)
	}
	if s.Hits < 0 {
		return fperr.New(fperr.ArgError, "--hits cannot be negative, got %d", s.Hits)
	}
	switch s.Sort {
	case "", "recent", "concentration", "chronological":
	default:
		return fperr.New(fperr.ArgError, "--sort takes recent, concentration or chronological, got %q", s.Sort)
	}
	return s.buildFilter(time.Now())
}

// buildFilter resolves the flags into the one predicate the scanner applies.
// --mine is kept as the spelling for author == human that the contract pins.
func (s *searchFlags) buildFilter(now time.Time) error {
	author, err := parseAuthor(s.Author)
	if err != nil {
		return err
	}
	authorFlag := "--author " + string(author)
	if s.Mine {
		if author != "" && author != schema.AuthorHuman {
			return fperr.New(fperr.ArgError, "--mine and --author %s ask for different turns", author)
		}
		author, authorFlag = schema.AuthorHuman, "--mine"
	}
	since, err := parseWhen("--since", s.Since, now)
	if err != nil {
		return err
	}
	until, err := parseWhen("--until", s.Until, now)
	if err != nil {
		return err
	}
	if !since.IsZero() && !until.IsZero() && until.Before(since) {
		return fperr.New(fperr.ArgError, "--until %s is before --since %s", s.Until, s.Since)
	}
	// Naming a session outranks the default exclusion of it: asking for your own
	// session by id is unambiguous, and silently returning nothing for it would
	// be the worst kind of narrowing.
	self := callingSession(s.IncludeSelf)
	if s.Session != "" && strings.HasPrefix(self, s.Session) {
		self = ""
	}
	s.filter = filter{
		author:     author,
		authorFlag: authorFlag,
		branch:     s.Branch,
		agent:      s.Agent,
		session:    s.Session,
		since:      since,
		until:      until,
		self:       self,
		dropRecall: !s.IncludeRecall,
	}
	return nil
}

// callingSession is the session running the command, which Claude Code puts in
// the environment. Excluding it is exact rather than heuristic because of that;
// outside Claude Code the variable is unset and nothing is excluded.
func callingSession(include bool) string {
	if include {
		return ""
	}
	return os.Getenv("CLAUDE_CODE_SESSION_ID")
}

// parseArgs parses flags that appear after the query as well as before it.
// stdlib flag stops at the first non-flag argument, so `recall find agvtool
// --all` would otherwise search for the words "agvtool --all" — which is what
// an agent writes, and it fails silently by finding nothing.
func parseArgs(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := parseFlags(fs, args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return positional, nil
		}
		positional = append(positional, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

func queryOf(words []string, verb string) (string, error) {
	q := strings.TrimSpace(strings.Join(words, " "))
	if q == "" {
		return "", fperr.New(fperr.ArgError, "%s needs something to look for, e.g. `recall %s agvtool`", verb, verb)
	}
	return q, nil
}

// corpus is the archive as one command sees it: the turns of the tiers it
// asked for, both coverage boundaries, and whether the refresh from disk ran.
type corpus struct {
	store     *archive.Store
	turns     []schema.Turn
	tiers     []schema.Tier
	unknown   []string
	coverage  archive.Coverage
	refreshed bool

	// refreshedAgo is how stale the archive is in words. With --no-update in the
	// mix a caller otherwise cannot tell whether it is reading current data.
	refreshedAgo string
}

// openCorpus wires the real strip and repo implementations into the archive
// and loads only the tiers about to be searched — the conversation tier alone
// is 51 MB of the archive's 261 MB, and reading the rest is the whole
// difference between a 14 ms query and a 2.3 s one.
//
// The pairing of the injected functions is load-bearing: Strip is called from
// the archive's worker pool and strip.Stripper is safe for that, while Resolve
// runs single-threaded because resolving an identity reads git state.
func openCorpus(noUpdate bool, tiers []schema.Tier) (*corpus, error) {
	store, err := archive.Open(archive.Options{Strip: strip.New().Strip, Resolve: repo.New().Repo})
	if err != nil {
		return nil, err
	}
	c := &corpus{store: store, tiers: tiers}
	// The cold build takes a second or two and used to be silent, which reads
	// as a hang to a caller that has never run the tool before. It is announced
	// before the work, not after it.
	if !noUpdate && store.WrittenAt().IsZero() {
		fmt.Fprintln(os.Stderr, "recall: building the archive from the whole session store — this happens once and takes a second or two")
	}
	if noUpdate {
		cov, err := store.Coverage()
		if err != nil {
			return nil, fperr.New(fperr.BadArchive,
				"there is no archive to search yet; run the same command without --no-update to build one")
		}
		c.coverage = cov
		c.refreshedAgo = ago(time.Since(store.WrittenAt()))
	} else {
		res, err := store.Update()
		if err != nil {
			return nil, err
		}
		c.coverage, c.refreshed = res.Coverage, true
		c.refreshedAgo = "just now"
	}
	turns, err := store.Turns(tiers...)
	if err != nil {
		return nil, err
	}
	c.turns = turns
	c.unknown = unknownTiers(turns)
	return c, nil
}

// unknownTiers names the tiers in the loaded turns that this build does not
// recognise, sorted. internal/archive files an unrecognised tier in the
// conversation file, so every search loads it — but internal/scan matches only
// the three it knows, so those turns are read and not searched. That gap is
// exactly the shape of a silent false negative, so the coverage line says so.
func unknownTiers(turns []schema.Turn) []string {
	var seen map[schema.Tier]bool
	for i := range turns {
		if knownTier(turns[i].Tier) {
			continue
		}
		if seen == nil {
			seen = map[schema.Tier]bool{}
		}
		seen[turns[i].Tier] = true
	}
	out := make([]string, 0, len(seen))
	for t := range seen {
		out = append(out, string(t))
	}
	sort.Strings(out)
	return out
}

func knownTier(t schema.Tier) bool {
	switch t {
	case schema.TierConversation, schema.TierInvocation, schema.TierResult:
		return true
	}
	return false
}

// scopeOf is the repo the query runs against. The default is the repo the
// caller is standing in, because that is the common case; a cwd that belongs to
// no repo falls back to machine-wide rather than to a scope that would match
// only the sessions run outside a repo.
func scopeOf(f *searchFlags) render.Scope {
	cwd, _ := os.Getwd()
	sc := render.Scope{CWD: cwd, All: f.All}
	switch {
	case f.All:
		return sc
	case f.Repo != "":
		sc.Repo = f.Repo
		return sc
	}
	id := repo.New().Resolve(cwd)
	if id.Kind == repo.KindNone {
		sc.All = true
		return sc
	}
	sc.Repo = id.ID
	return sc
}

// inScope keeps the turns of one repo. The match is a substring fallback so a
// caller can pass `--repo api-server` rather than the whole normalized
// remote, which is what the identity actually is.
func inScope(turns []schema.Turn, sc render.Scope) []schema.Turn {
	if sc.All || sc.Repo == "" {
		return turns
	}
	needle := strings.ToLower(sc.Repo)
	out := make([]schema.Turn, 0, len(turns))
	for _, t := range turns {
		if t.Repo == sc.Repo || strings.Contains(strings.ToLower(t.Repo), needle) {
			out = append(out, t)
		}
	}
	return out
}

// searched is one query's outcome, plus the raw scan the coverage line needs
// for the counts it states.
type searched struct {
	scan   scan.Result
	ranked rank.Result
	scope  render.Scope

	// skipped is what the default exclusions removed from this search alone.
	// The wider probe and the terms-nearby survey run the same predicate again,
	// so a live counter reports the same turns two and three times over.
	skipped drops

	// notes are the coverage lines this search alone can state — chiefly that
	// another checkout answers the query better than this one does. They live
	// here so every verb that searches gets them from one place.
	notes []string
}

// search runs one query and returns the ranked sessions plus the raw scan,
// which the coverage line needs for the counts it states.
func (c *corpus) search(q string, f *searchFlags, mode rank.Mode) searched {
	sc := scopeOf(f)
	res := scan.Search(inScope(c.turns, sc), scan.Query{
		Text:     q,
		Tiers:    c.tiers,
		Exact:    f.Exact,
		AllTerms: f.AllTerms,
		Not:      f.Not,
		Keep:     f.filter.keep(),
		// A repo-scoped miss probes the whole machine next, and that pass
		// surveys the corpus for nearby terms itself. Surveying the scoped
		// slice first costs a second tokenizing pass for an answer strictly
		// worse than the one about to replace it.
		NearbyMax: nearbySurvey(sc),
	})
	hits := res.Hits
	if f.Sort == "recent" {
		mode = rank.Recent
	}
	out := searched{
		scan:    res,
		ranked:  rank.Rank(hits, res.TurnsBySession, mode),
		scope:   sc,
		skipped: f.filter.snapshot(),
	}
	out.notes = c.betterElsewhere(q, f, out)
	return out
}

// elsewhere probes the whole machine after a repo-scoped miss. Reporting a bare
// zero when the answer sits in another checkout is the failure this tool exists
// to invert, and it is acceptance case a5.
func (c *corpus) elsewhere(q string, f *searchFlags, sc render.Scope) ([]render.Elsewhere, []render.Term) {
	if sc.All || sc.Repo == "" {
		return nil, nil
	}
	wide := scan.Search(c.turns, scan.Query{
		Text:     q,
		Tiers:    c.tiers,
		Exact:    f.Exact,
		AllTerms: f.AllTerms,
		Not:      f.Not,
		Keep:     f.filter.keep(),
	})
	hits := wide.Hits
	if len(hits) == 0 {
		return nil, termViews(wide.Terms)
	}

	deduped, _ := rank.Dedup(hits)
	byRepo := map[string]*render.Elsewhere{}
	sessions := map[string]map[string]struct{}{}
	needle := strings.ToLower(sc.Repo)
	for _, h := range deduped {
		if h.Repo == sc.Repo || strings.Contains(strings.ToLower(h.Repo), needle) {
			continue
		}
		e := byRepo[h.Repo]
		if e == nil {
			e = &render.Elsewhere{Repo: displayRepo(h.Repo)}
			byRepo[h.Repo] = e
			sessions[h.Repo] = map[string]struct{}{}
		}
		e.Hits++
		sessions[h.Repo][h.Session] = struct{}{}
	}

	out := make([]render.Elsewhere, 0, len(byRepo))
	for id, e := range byRepo {
		e.Sessions = len(sessions[id])
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Hits != out[j].Hits {
			return out[i].Hits > out[j].Hits
		}
		return out[i].Repo < out[j].Repo
	})
	return out, nil
}

// betterElsewhere is the repo-scoped half of the failure this tool exists to
// invert, re-armed for relaxed matching. The wider probe fires only on a bare
// zero, and a query that could not be met in full no longer returns zero — it
// returns weak local partial matches, which look answered. So when the machine
// carries more of the query than this repo does, the footer says so.
func (c *corpus) betterElsewhere(q string, f *searchFlags, s searched) []string {
	if s.scope.All || s.scope.Repo == "" || !s.scan.Match.Relaxed() || len(s.ranked.Sessions) == 0 {
		return nil
	}
	wide := scan.Search(c.turns, scan.Query{
		Text:      q,
		Tiers:     c.tiers,
		Exact:     f.Exact,
		AllTerms:  f.AllTerms,
		Not:       f.Not,
		Keep:      f.filter.keep(),
		NearbyMax: -1,
	})
	if wide.Match.Required <= s.scan.Match.Required {
		return nil
	}
	return []string{fmt.Sprintf(
		"turns elsewhere on this machine carry %d of the %d terms, against %d here — run: recall find %s --all",
		wide.Match.Required, wide.Match.Total, s.scan.Match.Required, shellArg(q))}
}

// shellArg quotes a suggested command's query so pasting it back is one
// argument.
func shellArg(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t'\"\\$`") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// nearbySurvey suppresses the terms-present-nearby pass where a wider one is
// already coming. Negative skips it; zero takes scan's default.
func nearbySurvey(sc render.Scope) int {
	if sc.All || sc.Repo == "" {
		return 0
	}
	return -1
}

func displayRepo(id string) string {
	if id == "" {
		return "(no repo recorded)"
	}
	return id
}

// coverageOf assembles the line every searching command emits. Both boundaries
// come from the archive and neither is derived from the other.
func (c *corpus) coverageOf(res scan.Result, f *searchFlags, skipped drops, limits []render.Limit, notes ...string) render.Coverage {
	return render.Coverage{
		Sessions:         res.Sessions,
		SessionsSearched: res.SessionsScanned,
		Turns:            res.Turns,
		TurnsSearched:    res.TurnsScanned,
		Searched:         res.Tiers,
		Unsearched:       res.Unsearched(),
		LiveFrom:         c.coverage.LiveFrom,
		ContentFrom:      c.coverage.ContentFrom,
		ContentTo:        c.coverage.ContentTo,
		ArchiveReaches:   c.coverage.ReachesBeforeLive(),
		Refreshed:        c.refreshed,
		RefreshedAgo:     c.refreshedAgo,
		Query: render.Query{
			Terms:    res.Match.Terms,
			Dropped:  res.Match.Dropped,
			Excluded: res.Match.Excluded,
			Required: res.Match.Required,
			Total:    res.Match.Total,
			Carried:  res.Match.Carried,
		},
		Limits: append(limits, filterLimits(f, res)...),
		Notes:  append(notes, c.notes(f, skipped)...),
	}
}

func (c *corpus) notes(f *searchFlags, skipped drops) []string {
	var out []string
	if f != nil {
		out = append(out, skipped.narrowings()...)
	}
	if len(c.unknown) > 0 {
		out = append(out, fmt.Sprintf(
			"a tier this build does not know is present (%s) and was read but not matched — run `recall doctor`",
			strings.Join(c.unknown, ", ")))
	}
	return out
}

// ago puts a duration in the words a reader skims, coarsening as it grows: the
// question is whether the archive is current, not how many seconds old it is.
func ago(d time.Duration) string {
	switch {
	case d < 2*time.Second:
		return "just now"
	case d < time.Minute:
		return fmt.Sprintf("%d s ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%d days ago", int(d.Hours()/24))
	}
}

// sessionViews cuts the ranked sessions down to what will be printed and
// reports the caps it applied, which the coverage line then declares.
func sessionViews(sessions []rank.Session, limit, perSession int) ([]render.Session, []render.Limit) {
	var limits []render.Limit
	shown := sessions
	if len(shown) > limit {
		shown = shown[:limit]
		limits = append(limits, render.Limit{Flag: "--limit", What: "sessions", Shown: limit, Total: len(sessions)})
	}

	out := make([]render.Session, 0, len(shown))
	cutHits, allHits := 0, 0
	for _, s := range shown {
		matched := rank.Best(s.Turnwise, perSession)
		allHits += len(s.Turnwise)
		cutHits += len(matched)
		view := render.Session{
			ID:         s.ID,
			Repo:       displayRepo(s.Repo),
			Branch:     s.Branch,
			Hits:       s.HitCount,
			HitTurns:   len(s.Turnwise),
			Turns:      s.Turns,
			TurnsKnown: s.TurnsKnown,
			AgentHits:  s.AgentHits,
			Score:      s.Score,
			First:      render.Day(s.First),
			Last:       render.Day(s.Last),
			Shown:      make([]render.Hit, 0, len(matched)),
		}
		for _, m := range matched {
			view.Shown = append(view.Shown, hitView(m))
		}
		out = append(out, view)
	}
	if cutHits < allHits {
		limits = append(limits, render.Limit{Flag: "--hits", What: "matched turns", Shown: cutHits, Total: allHits})
	}
	return out, limits
}

func hitView(m rank.Matched) render.Hit {
	return render.Hit{
		UUID:        m.UUID,
		TS:          m.TS,
		Tier:        m.Tier,
		Author:      m.Author,
		Agent:       m.Agent,
		Offset:      m.Offset,
		Length:      m.Length,
		Occurrences: m.Occurrences,
		Match:       m.Match,
		Terms:       m.Terms,
		Snippet:     render.Snippet(m.Text, m.Offset, m.Length, render.DefaultSnippet),
	}
}

func facetViews(facets []rank.Facet) []render.Facet {
	out := make([]render.Facet, 0, len(facets))
	for _, f := range facets {
		out = append(out, render.Facet{Value: f.Value, Hits: f.Hits, Sessions: f.Sessions})
	}
	return out
}

func termViews(reports []scan.TermReport) []render.Term {
	out := make([]render.Term, 0, len(reports))
	for _, r := range reports {
		t := render.Term{Term: r.Term, Turns: r.Turns}
		for _, n := range r.Nearby {
			t.Nearby = append(t.Nearby, n.Text)
		}
		out = append(out, t)
	}
	return out
}

// lined is a view that can emit one object per line. Everything that accepts
// --format jsonl implements it, so accepting the flag and printing prose is a
// compile error rather than a silent disappointment.
type lined interface{ JSONL() ([]byte, error) }

func emit(out io.Writer, g *Globals, human []byte, machine lined) error {
	body := human
	switch g.Format {
	case FormatJSON:
		b, err := render.JSON(machine)
		if err != nil {
			return err
		}
		body = b
	case FormatJSONL:
		b, err := machine.JSONL()
		if err != nil {
			return err
		}
		body = b
	}
	return render.Emit(out, body, g.MaxBytes)
}

// filterLimits is the narrowing the caller asked for, stated in the same footer
// as every other one.
func filterLimits(f *searchFlags, res scan.Result) []render.Limit {
	if f == nil {
		return nil
	}
	return f.filter.limits(res.TurnsScanned, res.Turns)
}

// LargeAnswer is the size past which a text answer is worth warning about. It
// is a fraction of --max-bytes on purpose: the cap is the point of refusal, and
// a caller that has to page an answer out to a file has already lost, at a size
// well under it.
const LargeAnswer = 16 << 10

// warnIfLarge tells a caller how to ask for less, before it has to work out for
// itself why its context just filled up. It goes to stderr so the answer on
// stdout is unchanged, and it stays quiet for a caller that already set a
// budget or asked for a machine format.
func warnIfLarge(body []byte, g *Globals, levers string) {
	if g.Format != FormatText || g.Budget > 0 || len(body) < LargeAnswer {
		return
	}
	fmt.Fprintf(os.Stderr, "recall: this answer is %.1f KB (~%d tokens) — narrow it with %s\n",
		float64(len(body))/1024, len(body)/render.BytesPerToken, levers)
}
