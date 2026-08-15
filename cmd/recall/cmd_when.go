package main

import (
	"flag"
	"io"
	"os"
	"sort"
	"time"

	"github.com/mayberuk/recall/internal/rank"
	"github.com/mayberuk/recall/internal/render"
	"github.com/mayberuk/recall/internal/scan"
)

type whenCmd struct {
	fs *flag.FlagSet
	g  *Globals
	f  *searchFlags
}

func newWhenCmd() *whenCmd {
	c := &whenCmd{fs: newFlags("when"), g: NewGlobals(), f: newSearchFlags()}
	c.g.Bind(c.fs)
	c.f.bind(c.fs)
	return c
}

func init() {
	Register("when", func(args []string) error { return when(args, os.Stdout) })
	Describe("when", "<query>", "place a topic in time: first said, last said, and the months between",
		newWhenCmd().fs,
		"recall when codepush",
		"recall when wallet --all --brief")
}

func when(args []string, out io.Writer) error {
	cmd := newWhenCmd()
	fs, g, f := cmd.fs, cmd.g, cmd.f
	words, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if err := g.Check(); err != nil {
		return err
	}
	if err := f.check(); err != nil {
		return err
	}
	q, err := queryOf(words, "when")
	if err != nil {
		return err
	}

	c, err := openCorpus(f.NoUpdate, scan.Tiers(f.Results, f.Tools))
	if err != nil {
		return err
	}

	s := c.search(q, f, rank.Chronological)
	sessions, limits := sessionViews(s.ranked.Sessions, f.Limit, f.Hits)

	first, last := span(s.ranked.Sessions)
	view := render.When{
		Verb:     "when",
		Query:    q,
		Scope:    s.scope,
		Hits:     s.ranked.HitCount,
		First:    render.Day(first),
		Last:     render.Day(last),
		Buckets:  buckets(s.ranked),
		Sessions: sessions,
	}
	if len(sessions) == 0 {
		view.Elsewhere, view.Terms = c.elsewhere(q, f, s.scope)
		if len(view.Terms) == 0 && len(view.Elsewhere) == 0 {
			view.Terms = termViews(s.scan.Terms)
		}
	}
	view.Coverage = c.coverageOf(s.scan, f, s.skipped, limits, s.notes...)

	body := render.WithSize(view.Text())
	if f.Brief {
		body = render.WithSize(view.Brief())
	}
	if f.IDs {
		body = view.IDs()
	}
	if err := emit(out, g, body, view); err != nil {
		return err
	}
	return foundSomething(view.Sessions)
}

// span is the earliest and latest dated hit across every ranked session, not
// only the ones the --limit cut kept: the question is when the topic ran, and a
// display cap must not shorten the answer.
func span(sessions []rank.Session) (first, last time.Time) {
	for _, s := range sessions {
		if !s.Dated {
			continue
		}
		if first.IsZero() || s.First.Before(first) {
			first = s.First
		}
		if s.Last.After(last) {
			last = s.Last
		}
	}
	return first, last
}

// buckets is the timeline, by month. Months rather than days because the corpus
// spans weeks and a daily histogram is mostly empty rows.
func buckets(r rank.Result) []render.Bucket {
	hits := map[string]int{}
	sessions := map[string]map[string]struct{}{}
	for _, s := range r.Sessions {
		for _, h := range s.Hits {
			t, err := time.Parse(time.RFC3339, h.TS)
			if err != nil {
				continue
			}
			key := t.UTC().Format("2006-01")
			hits[key]++
			if sessions[key] == nil {
				sessions[key] = map[string]struct{}{}
			}
			sessions[key][s.ID] = struct{}{}
		}
	}
	out := make([]render.Bucket, 0, len(hits))
	for month, n := range hits {
		out = append(out, render.Bucket{Month: month, Hits: n, Sessions: len(sessions[month])})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Month < out[j].Month })
	return out
}
