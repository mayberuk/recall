package main

import (
	"flag"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/mayberuk/recall/internal/fperr"
	"github.com/mayberuk/recall/internal/render"
	"github.com/mayberuk/recall/internal/scan"
	"github.com/mayberuk/recall/internal/schema"
	"github.com/mayberuk/recall/internal/style"
)

// DefaultAround is how many turns of context each side of a match `show`
// returns. A window rather than the session, because the mean session's
// conversation is ~67K tokens and the largest is ~550K — a whole-session fetch
// is the multi-megabyte lookup the requirements rule out.
const DefaultAround = 3

type showCmd struct {
	fs       *flag.FlagSet
	g        *Globals
	results  bool
	tools    bool
	full     bool
	noUpdate bool
	turnID   string
	around   int
	chars    int
	words    bool
}

func newShowCmd() *showCmd {
	c := &showCmd{fs: newFlags("show"), g: NewGlobals(), around: DefaultAround}
	c.g.Bind(c.fs)
	c.fs.BoolVar(&c.results, "results", false, "also show tool output")
	c.fs.BoolVar(&c.tools, "tools", false, "also show tool invocation lines")
	c.fs.BoolVar(&c.full, "full", false, "the whole session, still refused when it breaches --max-bytes")
	c.fs.BoolVar(&c.noUpdate, "no-update", false, "read the archive as it stands, skipping the refresh from disk")
	c.fs.StringVar(&c.turnID, "turn", "", "anchor the window on this record uuid")
	c.fs.IntVar(&c.around, "around", c.around, "turns of context each side of a match")
	c.fs.IntVar(&c.chars, "chars", c.chars, "most characters of each turn to quote; 0 for the whole turn")
	c.fs.BoolVar(&c.words, "words", false, "also count the words scanned, and the lines with them — a second pass over the scanned bytes")
	return c
}

func init() {
	Register("show", func(args []string) error { return show(args, os.Stdout) })
	Describe("show", "<session> [query]", "recover what was concluded, with the turns around it",
		newShowCmd().fs,
		"recall show 5fd86b00",
		`recall show 5fd86b00 "build number"`,
		"recall show 5fd86b00 --turn a1db2039",
		"recall show 5fd86b00 bitrise --results --around 5",
		"recall show 5fd86b00 --turn a1db2039 --chars 2000")
}

func show(args []string, out io.Writer) error {
	cmd := newShowCmd()
	fs, g := cmd.fs, cmd.g
	words, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if err := g.Check(); err != nil {
		return err
	}
	results, tools, full := cmd.results, cmd.tools, cmd.full
	turnID, around := cmd.turnID, cmd.around
	if around < 0 {
		return fperr.New(fperr.ArgError, "--around cannot be negative, got %d", around)
	}
	if cmd.chars < 0 {
		return fperr.New(fperr.ArgError, "--chars cannot be negative, got %d", cmd.chars)
	}
	if len(words) == 0 {
		return fperr.New(fperr.ArgError, "show needs a session id, e.g. `recall show b5ddc1af`")
	}
	query := strings.TrimSpace(strings.Join(words[1:], " "))

	tiers := scan.Tiers(results, tools)
	c, err := openCorpus(cmd.noUpdate, tiers)
	if err != nil {
		return err
	}
	id, err := resolveSession(c.turns, words[0])
	if err != nil {
		return err
	}

	session := sessionTurns(c.turns, id)
	res := scan.Search(session, scan.Query{Text: query, Tiers: tiers, NearbyMax: -1, CountWords: cmd.words})
	c.record(res)

	ordered := onTiers(session, tiers)
	anchors, anchor, err := anchorsOf(ordered, res.Hits, turnID, query, full, around)
	if err != nil {
		return err
	}

	view := render.Show{
		Verb:    "show",
		Session: id,
		Query:   query,
		Anchor:  anchor,
		Turns:   len(ordered),
		Tiers:   tiers,
		Matches: len(anchors),
		Full:    full,
	}
	if len(ordered) > 0 {
		view.Repo = displayRepo(ordered[0].Repo)
		view.Branch = ordered[0].Branch
	}

	body, err := c.fitShow(&view, windowViews(ordered, anchors, around, cmd.chars, full), res, g, g.Palette(out), full)
	if err != nil {
		return err
	}
	warnIfLarge(body, g, "--chars, --around or --budget")
	if err := render.Emit(out, body, g.MaxBytes); err != nil {
		return err
	}
	if len(view.Windows) == 0 {
		return fperr.New(fperr.NoHits, "nothing to show")
	}
	return nil
}

// fitShow renders the answer at the most match windows that fit the byte cap,
// and declares the cut like every other cap here does.
//
// Refusing outright is right for --full, which is the explicit give-me-
// everything path. It is wrong for a plain `show <session> <query>`: a 119-turn
// session at the default --around 3 renders 80 KB against a 64 KiB cap, so the
// question shape this verb exists to serve — recover a conclusion and its
// reasoning — would refuse more often than it answered.
func (c *corpus) fitShow(view *render.Show, windows []render.Window, res scan.Result, g *Globals, pal style.Palette, full bool) ([]byte, error) {
	attempt := func(n int) ([]byte, error) {
		view.Windows = windows[:n]
		view.Shown, view.Fitted = turnsIn(view.Windows), matchesIn(view.Windows)
		view.Coverage = c.coverageOf(res, nil, drops{}, showLimits(*view, n < len(windows)))
		return renderShow(*view, g, pal)
	}
	if full || len(windows) <= 1 {
		return attempt(len(windows))
	}
	ceiling := g.Cap()

	// Binary search over a size that only grows with n. The winning attempt's
	// body and coverage are the ones captured when the search confirms they
	// fit, not re-rendered afterward: the stats footer's elapsed figure only
	// grows with wall-clock time, so re-rendering the same n later could come
	// back a byte or two larger than what the search just verified fit under
	// ceiling, and the cap check downstream would then reject a window this
	// function itself just confirmed.
	best := 1
	bestBody, err := attempt(1)
	if err != nil {
		return nil, err
	}
	bestCoverage := view.Coverage
	for lo, hi := 1, len(windows); lo <= hi; {
		mid := (lo + hi) / 2
		body, err := attempt(mid)
		if err != nil {
			return nil, err
		}
		if int64(len(body)) <= ceiling {
			best, bestBody, bestCoverage = mid, body, view.Coverage
			lo = mid + 1
			continue
		}
		hi = mid - 1
	}
	view.Windows = windows[:best]
	view.Shown, view.Fitted = turnsIn(view.Windows), matchesIn(view.Windows)
	view.Coverage = bestCoverage
	return bestBody, nil
}

// renderShow produces whichever format was asked for, so the cap is measured
// against the bytes that would actually be written.
func renderShow(view render.Show, g *Globals, pal style.Palette) ([]byte, error) {
	switch g.Format {
	case FormatJSON:
		return render.JSON(view)
	case FormatJSONL:
		return view.JSONL()
	}
	// The size footer is added here rather than at the end so the fitting search
	// measures the bytes that will actually be written, footer included.
	return render.WithSize(view.WithPalette(pal).Text()), nil
}

func showLimits(view render.Show, cut bool) []render.Limit {
	var limits []render.Limit
	if view.Shown < view.Turns {
		limits = append(limits, render.Limit{Flag: "--around", What: "turns", Shown: view.Shown, Total: view.Turns})
	}
	if cut {
		limits = append(limits, render.Limit{Flag: "--max-bytes", What: "matches", Shown: view.Fitted, Total: view.Matches})
	}
	return limits
}

func turnsIn(windows []render.Window) int {
	n := 0
	for _, w := range windows {
		n += len(w.Turns)
	}
	return n
}

func matchesIn(windows []render.Window) int {
	n := 0
	for _, w := range windows {
		for _, t := range w.Turns {
			if t.Match {
				n++
			}
		}
	}
	return n
}

// resolveSession accepts a unique prefix, because a session id is 36 characters
// and every one of them comes off another command's output.
func resolveSession(turns []schema.Turn, prefix string) (string, error) {
	if prefix == "" {
		return "", fperr.New(fperr.ArgError, "show needs a session id")
	}
	matches := map[string]struct{}{}
	for i := range turns {
		id := turns[i].Session
		if id == prefix {
			return id, nil
		}
		if strings.HasPrefix(id, prefix) {
			matches[id] = struct{}{}
		}
	}
	ids := make([]string, 0, len(matches))
	for id := range matches {
		ids = append(ids, id)
	}
	slices.Sort(ids)

	switch len(ids) {
	case 0:
		return "", fperr.New(fperr.NotFound, "no archived session id starts with %q", prefix)
	case 1:
		return ids[0], nil
	default:
		return "", fperr.New(fperr.ArgError, "%q matches %d sessions: %s", prefix, len(ids), strings.Join(head(ids, 5), " "))
	}
}

func head(ids []string, n int) []string {
	if len(ids) <= n {
		return ids
	}
	return append(ids[:n:n], "…")
}

// sessionTurns is one session's turns in the order they were written. The
// archive keeps a file per tier and returns them file by file, so a multi-tier
// read arrives grouped by tier; without re-interleaving, show would print every
// reply before any of the tool output that produced it.
func sessionTurns(turns []schema.Turn, id string) []schema.Turn {
	out := make([]schema.Turn, 0, 64)
	for i := range turns {
		if turns[i].Session == id {
			out = append(out, turns[i])
		}
	}
	slices.SortStableFunc(out, func(a, b schema.Turn) int {
		if c := strings.Compare(a.TS, b.TS); c != 0 {
			return c
		}
		if c := strings.Compare(a.UUID, b.UUID); c != 0 {
			return c
		}
		return tierRank(a.Tier) - tierRank(b.Tier)
	})
	return out
}

// tierRank is the order internal/strip emits one record's turns in, which is
// the order the archive's own sort recorded and the only way to put a record's
// prose back in front of the call it made.
func tierRank(t schema.Tier) int {
	switch t {
	case schema.TierInvocation:
		return 1
	case schema.TierResult:
		return 2
	default:
		return 0
	}
}

// onTiers keeps what show will print. A tier this build does not recognise is
// kept alongside the conversation tier, which is where internal/archive files
// it: showing an unrecognised turn costs a few lines, dropping it is the silent
// false negative the tool exists to prevent.
func onTiers(turns []schema.Turn, tiers []schema.Tier) []schema.Turn {
	want := make(map[schema.Tier]bool, len(tiers))
	for _, t := range tiers {
		want[t] = true
	}
	out := make([]schema.Turn, 0, len(turns))
	for i := range turns {
		tier := turns[i].Tier
		if want[tier] || (want[schema.TierConversation] && !knownTier(tier)) {
			out = append(out, turns[i])
		}
	}
	return out
}

// anchorsOf picks the turns a window is built around: the named record, every
// match, or — when the caller named neither — the end of the session, which is
// where a conclusion is.
func anchorsOf(ordered []schema.Turn, hits []schema.Hit, turnID, query string, full bool, around int) ([]int, string, error) {
	if full {
		return nil, render.AnchorFull, nil
	}
	at := map[[2]string]int{}
	for i, t := range ordered {
		at[[2]string{t.UUID, string(t.Tier)}] = i
	}

	if turnID != "" {
		var out []int
		for i, t := range ordered {
			if t.UUID == turnID {
				out = append(out, i)
			}
		}
		if len(out) == 0 {
			return nil, "", fperr.New(fperr.NotFound, "no turn with uuid %q in this session's shown tiers", turnID)
		}
		return out, render.AnchorTurn, nil
	}

	if query != "" {
		seen := map[int]bool{}
		var out []int
		for _, h := range hits {
			i, ok := at[[2]string{h.UUID, string(h.Tier)}]
			if !ok || seen[i] {
				continue
			}
			seen[i] = true
			out = append(out, i)
		}
		slices.Sort(out)
		return out, render.AnchorQuery, nil
	}

	if len(ordered) == 0 {
		return nil, render.AnchorTail, nil
	}
	return []int{len(ordered) - 1 - around}, render.AnchorTail, nil
}

// windowViews expands each anchor by around and merges the runs that touch, so
// two matches three turns apart print as one passage rather than twice.
func windowViews(ordered []schema.Turn, anchors []int, around, chars int, full bool) []render.Window {
	if len(ordered) == 0 {
		return nil
	}
	match := make(map[int]bool, len(anchors))
	var spans [][2]int
	if full {
		spans = [][2]int{{0, len(ordered) - 1}}
	} else {
		for _, a := range anchors {
			match[a] = true
			spans = append(spans, [2]int{clamp(a-around, 0, len(ordered)-1), clamp(a+around, 0, len(ordered)-1)})
		}
		slices.SortFunc(spans, func(x, y [2]int) int { return x[0] - y[0] })
	}

	var merged [][2]int
	for _, s := range spans {
		if n := len(merged); n > 0 && s[0] <= merged[n-1][1]+1 {
			if s[1] > merged[n-1][1] {
				merged[n-1][1] = s[1]
			}
			continue
		}
		merged = append(merged, s)
	}

	out := make([]render.Window, 0, len(merged))
	for _, s := range merged {
		w := render.Window{From: s[0], To: s[1]}
		for i := s[0]; i <= s[1]; i++ {
			t := ordered[i]
			text, cut := t.Text, false
			if chars > 0 && len(text) > chars {
				text, cut = render.Excerpt(text, 0, 0, chars), true
			}
			w.Turns = append(w.Turns, render.Turn{
				Index:     i,
				UUID:      t.UUID,
				TS:        t.TS,
				Tier:      t.Tier,
				Author:    t.Author,
				Agent:     t.Agent,
				Match:     match[i],
				Text:      text,
				Truncated: cut,
				Length:    len(t.Text),
			})
		}
		out = append(out, w)
	}
	return out
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
