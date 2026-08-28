package main

import (
	"cmp"
	"flag"
	"io"
	"os"
	"slices"

	"github.com/mayberuk/recall/internal/fperr"
	"github.com/mayberuk/recall/internal/rank"
	"github.com/mayberuk/recall/internal/render"
	"github.com/mayberuk/recall/internal/scan"
)

// Defaults for `turns`. Five passages is what fits a reply without crowding
// out the caller's own work; 700 bytes is a long paragraph, which is the unit a
// conclusion is usually stated in.
const (
	DefaultPassages = 5
	DefaultChars    = 700
)

type turnsCmd struct {
	fs    *flag.FlagSet
	g     *Globals
	f     *searchFlags
	chars int
}

func newTurnsCmd() *turnsCmd {
	c := &turnsCmd{fs: newFlags("turns"), g: NewGlobals(), f: newSearchFlags(), chars: DefaultChars}
	c.g.Bind(c.fs)
	// Set before binding: flag captures the default it will print in --help at
	// bind time, so a later assignment leaves the help text quoting 10.
	c.f.Limit = DefaultPassages
	c.f.bindFor(c.fs, "passages", "session:uuid citations")
	c.fs.IntVar(&c.chars, "chars", c.chars, "most characters of each turn to quote; 0 for the whole turn")
	return c
}

func init() {
	Register("turns", func(args []string) error { return turns(args, os.Stdout) })
	Describe("turns", "<query>", "the passages themselves, ranked across every session at once",
		newTurnsCmd().fs,
		`recall turns "why did we pick bitrise"`,
		"recall turns agvtool --all --limit 3",
		"recall turns codepush --author human --since 1m")
}

// turns answers with the passage instead of the session. find, then choosing,
// then show is three round trips for one question, and an agent that pays
// context per attempt abandons after about two.
func turns(args []string, out io.Writer) error {
	c := newTurnsCmd()
	g, f := c.g, c.f
	words, err := parseArgs(c.fs, args)
	if err != nil {
		return err
	}
	if err := g.Check(); err != nil {
		return err
	}
	if err := f.check(); err != nil {
		return err
	}
	if c.chars < 0 {
		return fperr.New(fperr.ArgError, "--chars cannot be negative, got %d", c.chars)
	}
	q, err := queryOf(words, "turns")
	if err != nil {
		return err
	}

	corp, err := openCorpus(f.NoUpdate, scan.Tiers(f.Results, f.Tools))
	if err != nil {
		return err
	}

	s := corp.search(q, f, rank.Concentration)
	ranked := bestTurns(s.ranked.Sessions)

	view := render.Turns{
		Verb:    "turns",
		Query:   q,
		Scope:   s.scope,
		Hits:    s.ranked.HitCount,
		Matched: len(ranked),
	}
	pal := g.Palette(out)
	// shaped marks a retry run only because the first attempt was too big for
	// --budget.
	build := func(limit, chars int, shaped bool) ([]byte, error) {
		var limits []render.Limit
		shown := ranked
		if len(shown) > limit {
			shown = shown[:limit]
			limits = append(limits, render.Limit{Flag: "--limit", What: "matched turns", Shown: len(shown), Total: len(ranked)})
		}
		if shaped {
			limits = mergeBudgetLimit(limits, "matched turns", len(shown), len(ranked))
		}
		view.Passages = view.Passages[:0]
		for _, m := range shown {
			view.Passages = append(view.Passages, passageOf(m, chars))
		}
		if len(view.Passages) == 0 {
			view.Elsewhere, view.Terms = corp.elsewhere(q, f, s.scope)
			if len(view.Terms) == 0 && len(view.Elsewhere) == 0 {
				view.Terms = termViews(s.scan.Terms)
			}
		}
		view.Coverage = corp.coverageOf(s.scan, f, s.skipped, limits, s.notes...)
		switch {
		case g.Format == FormatJSON:
			return render.JSON(view)
		case g.Format == FormatJSONL:
			return view.JSONL()
		case f.IDs:
			return view.IDs(), nil
		case f.Brief:
			return render.WithSize(view.WithPalette(pal).Brief()), nil
		default:
			return render.WithSize(view.WithPalette(pal).Text()), nil
		}
	}

	// Quoted passages are the bulkiest thing recall prints, so a budget buys
	// shorter quotes first and fewer of them second — a caller that named a
	// budget wants an answer that fits, not a refusal.
	body, err := build(f.Limit, c.chars, false)
	if err != nil {
		return err
	}
	if g.Budget > 0 {
		for _, attempt := range []struct{ limit, chars int }{
			{f.Limit, DefaultChars / 2},
			{max(f.Limit/2, 1), DefaultChars / 2},
			{1, DefaultChars / 2},
		} {
			if int64(len(body)) <= g.Cap() {
				break
			}
			if body, err = build(attempt.limit, attempt.chars, true); err != nil {
				return err
			}
		}
	}
	warnIfLarge(body, g, "--chars, --limit or --budget")
	if err := render.Emit(out, body, g.MaxBytes); err != nil {
		return err
	}
	if len(view.Passages) == 0 {
		return fperr.New(fperr.NoHits, "no turns")
	}
	return nil
}

// scored is one matched turn carrying the standing of the session it came from.
type scored struct {
	rank.Matched
	repo    string
	branch  string
	session float64
}

// bestTurns flattens every session's matches into one ranking. A turn's own
// worth leads, because the question is which passage answers it; the session's
// concentration breaks ties, so a turn from a session that is about the topic
// beats an identical turn from one that mentioned it once.
func bestTurns(sessions []rank.Session) []scored {
	var out []scored
	for _, s := range sessions {
		for _, m := range s.Turnwise {
			out = append(out, scored{Matched: m, repo: s.Repo, branch: s.Branch, session: s.Score})
		}
	}
	slices.SortStableFunc(out, func(a, b scored) int {
		if d := cmp.Compare(b.Signal, a.Signal); d != 0 {
			return d
		}
		if d := cmp.Compare(b.Occurrences, a.Occurrences); d != 0 {
			return d
		}
		if d := cmp.Compare(b.session, a.session); d != 0 {
			return d
		}
		return cmp.Compare(b.TS, a.TS)
	})
	return out
}

func passageOf(m scored, chars int) render.Passage {
	text, truncated := quoteAround(m.Text, m.Offset, m.Length, chars)
	return render.Passage{
		Session:     m.Session,
		UUID:        m.UUID,
		Cite:        m.Session + ":" + m.UUID,
		TS:          m.TS,
		Repo:        displayRepo(m.repo),
		Branch:      m.branch,
		Tier:        m.Tier,
		Author:      m.Author,
		Agent:       m.Agent,
		Occurrences: m.Occurrences,
		Terms:       m.Terms,
		Text:        text,
		Truncated:   truncated,
		Length:      len(m.Text),
	}
}

// quoteAround keeps the passage centred on the match rather than starting at
// the top of a turn that can be tens of kilobytes. A cut says so in the output
// and names the command that returns the rest: nothing here is truncated
// silently.
func quoteAround(text string, offset, length, chars int) (string, bool) {
	if chars <= 0 || len(text) <= chars {
		return text, false
	}
	return render.Excerpt(text, offset, length, chars), true
}
