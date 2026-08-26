package main

import (
	"flag"
	"io"
	"os"

	"github.com/mayberuk/recall/internal/fperr"
	"github.com/mayberuk/recall/internal/rank"
	"github.com/mayberuk/recall/internal/render"
	"github.com/mayberuk/recall/internal/scan"
)

// findCmd holds one run's flag set and the values it parses into. The same
// constructor builds the set for a real run and for `recall find --help`, so
// the help can never list a flag the verb does not accept.
type findCmd struct {
	fs  *flag.FlagSet
	g   *Globals
	f   *searchFlags
	fzf bool
}

func newFindCmd() *findCmd {
	c := &findCmd{fs: newFlags("find"), g: NewGlobals(), f: newSearchFlags()}
	c.g.Bind(c.fs)
	c.f.bind(c.fs)
	c.fs.BoolVar(&c.fzf, "fzf", false, "emit NUL-terminated `<session id>\\x1f<block>` records for the interactive front end")
	return c
}

func init() {
	Register("find", func(args []string) error { return find(args, os.Stdout, os.Stderr) })
	Describe("find", "<query>", "which sessions talked about something, and how much",
		newFindCmd().fs,
		"recall find agvtool",
		`recall find "build number" --since 2w`,
		"recall find bitrise --results --author assistant",
		"recall find wallet --brief --all",
		"recall find wallet --ids | head -1 | xargs recall show")
}

func find(args []string, out, errOut io.Writer) error {
	c := newFindCmd()
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
	if c.fzf && g.Format != FormatText {
		return fperr.New(fperr.ArgError, "--fzf and --format %s are two output formats; pick one", g.Format)
	}
	q, err := queryOf(words, "find")
	if err != nil {
		return err
	}

	corp, err := openCorpus(f.NoUpdate, scan.Tiers(f.Results, f.Tools))
	if err != nil {
		return err
	}

	s := corp.search(q, f, rank.Concentration)
	view := render.Find{
		Verb:      "find",
		Query:     q,
		Scope:     s.scope,
		Sort:      string(s.ranked.Mode),
		Hits:      s.ranked.HitCount,
		Redundant: s.ranked.Redundant,
		Repos:     facetViews(s.ranked.Facets.Repo),
		Authors:   facetViews(s.ranked.Facets.Author),
		Tiers:     facetViews(s.ranked.Facets.Tier),
	}

	pal := g.Palette(out)
	build := func(sz size) ([]byte, error) {
		hits := sz.hits
		if sz.brief {
			hits = 0
		}
		sessions, limits := sessionViews(s.ranked.Sessions, sz.limit, hits)
		if sz.brief {
			// Brief prints no hit lines, so a cap on them is noise. The cap on
			// sessions is not: a list cut to ten of seventy-seven with nothing
			// saying so is the silent narrowing the footer exists to prevent.
			limits = withoutFlag(limits, "--hits")
		}
		if sz.shaped {
			limits = mergeBudgetLimit(limits, "sessions", len(sessions), len(s.ranked.Sessions))
		}
		view.Sessions = sessions
		if len(sessions) == 0 {
			view.Elsewhere, view.Terms = corp.elsewhere(q, f, s.scope)
			if len(view.Terms) == 0 && len(view.Elsewhere) == 0 {
				view.Terms = termViews(s.scan.Terms)
			}
		}
		view.Coverage = corp.coverageOf(s.scan, f, s.skipped, limits, s.notes...)
		switch {
		case c.fzf:
			return nil, nil
		case g.Format == FormatJSON:
			return render.JSON(view)
		case g.Format == FormatJSONL:
			return view.JSONL()
		case f.IDs:
			return view.IDs(), nil
		case sz.brief:
			return render.WithSize(view.WithPalette(pal).Brief()), nil
		default:
			return render.WithSize(view.WithPalette(pal).Text()), nil
		}
	}

	body, err := fitToBudget(g, f, build)
	if err != nil {
		return err
	}

	if c.fzf {
		records, note := view.FZF()
		if err := render.Emit(errOut, note, g.MaxBytes-int64(plainLen(records))); err != nil {
			return err
		}
		if err := render.Emit(out, records, g.MaxBytes); err != nil {
			return err
		}
		return foundSomething(view.Sessions)
	}
	warnIfLarge(body, g, "--brief, --hits, --limit or --budget")
	if err := render.Emit(out, body, g.MaxBytes); err != nil {
		return err
	}
	return foundSomething(view.Sessions)
}

// foundSomething turns an empty result into exit 1. The report is already on
// stdout; this is only the status a caller branches on.
func foundSomething(sessions []render.Session) error {
	if len(sessions) == 0 {
		return fperr.New(fperr.NoHits, "no hits")
	}
	return nil
}

// size is one attempt at an answer: how many sessions, how many matched turns
// each, and whether snippets are printed at all.
type size struct {
	limit, hits int
	brief       bool

	// shaped marks a retry attempt run only because the first was too big for
	// --budget, so the footer can credit --budget rather than the size flags.
	shaped bool
}

// fitToBudget renders the largest answer that fits the caller's token budget,
// giving up detail in the order it costs least: fewer matched turns per
// session, then no snippets, then fewer sessions. Without --budget nothing is
// given up and an oversized answer is refused, which is the existing contract.
func fitToBudget(g *Globals, f *searchFlags, build func(size) ([]byte, error)) ([]byte, error) {
	attempts := []size{{limit: f.Limit, hits: f.Hits, brief: f.Brief}}
	if g.Budget > 0 && !f.IDs {
		attempts = append(attempts,
			size{limit: f.Limit, hits: 1, brief: f.Brief, shaped: true},
			size{limit: f.Limit, hits: 0, brief: true, shaped: true},
			size{limit: max(f.Limit/2, 1), hits: 0, brief: true, shaped: true},
			size{limit: 1, hits: 0, brief: true, shaped: true})
	}

	ceiling := g.Cap()
	var body []byte
	for i, a := range attempts {
		b, err := build(a)
		if err != nil {
			return nil, err
		}
		body = b
		if int64(plainLen(body)) <= ceiling || i == len(attempts)-1 {
			break
		}
	}
	return body, nil
}

func withoutFlag(limits []render.Limit, flag string) []render.Limit {
	out := limits[:0]
	for _, l := range limits {
		if l.Flag != flag {
			out = append(out, l)
		}
	}
	return out
}

// mergeBudgetLimit folds a --budget cap into an existing entry reporting the
// identical shown/total rather than restating it on its own line; a
// genuinely different cut still gets its own line.
func mergeBudgetLimit(limits []render.Limit, what string, shown, total int) []render.Limit {
	for i, l := range limits {
		if l.What == what && l.Shown == shown && l.Total == total {
			limits[i].Flag = l.Flag + ", --budget"
			return limits
		}
	}
	return append(limits, render.Limit{Flag: "--budget", What: what, Shown: shown, Total: total})
}
