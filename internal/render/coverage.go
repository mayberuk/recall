package render

import (
	"fmt"
	"strings"
	"time"

	"github.com/mayberuk/recall/internal/schema"
)

// NoStatsEnv, when set, tells cmd/recall to leave Coverage.Stats nil. This
// package never reads the environment itself — only the flag it names.
const NoStatsEnv = "RECALL_NO_STATS"

// Limit is a cap the caller chose that changed what was shown. Every one of
// these appears in the coverage line: a result list cut down silently is
// indistinguishable from a corpus that held nothing more.
type Limit struct {
	Flag  string `json:"flag"`
	What  string `json:"what"`
	Shown int    `json:"shown"`
	Total int    `json:"total"`
}

// Query is how a query was read: what was searched for after phrase grouping
// and common-word removal, what was excluded, and — when no turn in the corpus
// carried every term — how much of it the returned turns do carry.
//
// A relaxed search is the one place recall answers a question the caller did
// not quite ask, so it says so in the same footer that declares every other
// narrowing.
type Query struct {
	Terms    []string `json:"terms,omitempty"`
	Dropped  []string `json:"dropped,omitempty"`
	Excluded []string `json:"excluded,omitempty"`
	Required int      `json:"required"`
	Total    int      `json:"total"`
	Carried  []string `json:"carried,omitempty"`

	// Expanded is the terms answered under a spelling the caller did not type,
	// because no turn carried the one they did.
	Expanded []Expansion `json:"expanded,omitempty"`
}

// Expansion is one term searched under another spelling: what was typed, the
// corpus words put in its place, and how far from the typed term they are.
type Expansion struct {
	Term     string   `json:"term"`
	Variants []string `json:"variants"`
	Distance int      `json:"distance"`
}

// missing is the query terms no returned turn carries.
func (q Query) missing() []string {
	held := make(map[string]bool, len(q.Carried))
	for _, c := range q.Carried {
		held[c] = true
	}
	var out []string
	for _, t := range q.Terms {
		if !held[t] {
			out = append(out, t)
		}
	}
	return out
}

func (q Query) lines() []string {
	var out []string
	// Ahead of everything else, because it says what was searched rather than
	// what the search then did: every line below describes the result of a query
	// this one is still correcting.
	for _, e := range q.Expanded {
		out = append(out, fmt.Sprintf(
			"── no turn carries %s; searched %s instead (%d %s away) — --exact to search only what you typed",
			e.Term, strings.Join(e.Variants, ", "), e.Distance, plural(e.Distance, "edit", "edits")))
	}
	if q.Required > 0 && q.Required < q.Total {
		line := fmt.Sprintf("── no turn carried all %d terms; showing turns carrying %d of %s",
			q.Total, q.Required, strings.Join(q.Terms, ", "))
		// The claim that a term is carried by nothing is only sound at the
		// bottom level, where a single occurrence anywhere would have surfaced.
		if q.Required == 1 {
			if missing := q.missing(); len(missing) > 0 {
				line += " · no turn carries " + strings.Join(missing, ", ")
			}
		}
		out = append(out, line+" — --all-terms to require every term")
	}
	if len(q.Dropped) > 0 {
		out = append(out, "── ignored as too common to narrow anything: "+strings.Join(q.Dropped, ", "))
	}
	if len(q.Excluded) > 0 {
		out = append(out, "── turns carrying "+strings.Join(q.Excluded, ", ")+" were skipped (--not)")
	}
	return out
}

// Scope is which repos a command looked at.
type Scope struct {
	Repo string `json:"repo,omitempty"`
	All  bool   `json:"all"`
	CWD  string `json:"cwd,omitempty"`
}

// Coverage is what a command searched and how current that was. It is a
// contract rather than a summary: the requirements dealbreaker is a silent
// false negative, so the tiers left out are stated on every searching command.
//
// It is also, now that Stats hangs off it, the footer's whole data model —
// archive/session coverage plus the per-query performance telemetry — not
// something narrowly about coverage in the archival sense alone.
//
// LiveFrom and ContentFrom are different numbers and are never conflated.
// LiveFrom is the oldest mtime still on disk, which is what Claude Code's
// cleanup deletes next; ContentFrom is the oldest date the archived words reach.
type Coverage struct {
	Sessions         int           `json:"sessions"`
	SessionsSearched int           `json:"sessions_searched"`
	Turns            int           `json:"turns"`
	TurnsSearched    int           `json:"turns_searched"`
	Searched         []schema.Tier `json:"searched"`
	Unsearched       []schema.Tier `json:"unsearched"`
	LiveFrom         time.Time     `json:"-"`
	ContentFrom      time.Time     `json:"-"`
	ContentTo        time.Time     `json:"-"`
	ArchiveReaches   bool          `json:"archive_reaches_before_live"`
	Refreshed        bool          `json:"refreshed"`
	RefreshedAgo     string        `json:"refreshed_ago,omitempty"`
	Query            Query         `json:"query"`
	Limits           []Limit       `json:"limits,omitempty"`

	// Notes are narrowings that are not counts — something read but not
	// matched, for instance. They print as coverage lines because anything
	// that makes a search less complete than it looks belongs in the footer.
	Notes []string `json:"notes,omitempty"`

	// Stats is nil when the section is off (RECALL_NO_STATS), which with
	// omitempty leaves every byte of the JSON and JSONL forms this package
	// emitted before Stats existed untouched.
	Stats *Stats `json:"stats,omitempty"`

	// LiveFromAt, ContentFromAt and ContentToAt are LiveFrom, ContentFrom and
	// ContentTo as RFC3339, so a caller reads the boundaries without
	// re-deriving them from the human line. They are plain tagged fields
	// rather than a custom MarshalJSON so the struct and the wire agree and a
	// consumer can unmarshal a Coverage back.
	LiveFromAt    string `json:"live_from"`
	ContentFromAt string `json:"content_from"`
	ContentToAt   string `json:"content_to"`
}

// Stats is what a search cost: how much of the corpus it read, how long that
// took, and how many passes it took to answer. It is the machine-readable
// surface a later MCP caller reads instead of recomputing these numbers from
// the rendered text.
//
// Lines and Words are only as good as LinesKnown and WordsKnown say: counting
// either costs a second pass over the scanned text, so a search that was not
// asked to count reports zero and says so rather than implying an empty
// corpus. Passes counts the readings this footer explains — not corpus walks
// — so a reader who divides Bytes by Passes does not get corpus size.
type Stats struct {
	Bytes      int64   `json:"bytes"`
	Lines      int64   `json:"lines"`
	LinesKnown bool    `json:"lines_known"`
	Words      int64   `json:"words"`
	WordsKnown bool    `json:"words_known"`
	Turns      int     `json:"turns"`
	Passes     int     `json:"passes"`
	ElapsedMS  float64 `json:"elapsed_ms"`
}

// line renders the stats footer, the coverage line Lines appends last.
func (s Stats) line() string {
	segs := []string{"scanned " + byteSize(int(s.Bytes))}
	if s.LinesKnown {
		segs = append(segs, fmt.Sprintf("%d %s", s.Lines, plural(int(s.Lines), "line", "lines")))
	}
	if s.WordsKnown {
		segs = append(segs, fmt.Sprintf("%d %s", s.Words, plural(int(s.Words), "word", "words")))
	}
	segs = append(segs, fmt.Sprintf("%d %s", s.Turns, plural(s.Turns, "turn", "turns")))
	if s.Passes > 1 {
		segs = append(segs, fmt.Sprintf("%d %s", s.Passes, plural(s.Passes, "pass", "passes")))
	}
	segs = append(segs, duration(s.ElapsedMS))
	return "── " + strings.Join(segs, " · ")
}

// Lines is the coverage footer, one string per line, without the trailing
// newlines. The first two are the pinned format from the build contract.
func (c Coverage) Lines() []string {
	out := []string{
		fmt.Sprintf("── %d %s · %d searched · %s",
			c.Sessions, plural(c.Sessions, "session", "sessions"), c.SessionsSearched, TierClause(c.Unsearched)),
		"── " + c.freshness(),
	}
	out = append(out, c.Query.lines()...)
	for _, l := range c.Limits {
		out = append(out, fmt.Sprintf("── showing %d of %d %s (%s)", l.Shown, l.Total, l.What, l.Flag))
	}
	for _, n := range c.Notes {
		out = append(out, "── "+n)
	}
	if c.Stats != nil {
		out = append(out, c.Stats.line())
	}
	return out
}

// freshness states both boundaries without implying one from the other. They
// are minima over different sets, so the only honest claim relating them is
// whether the archive reaches further back at all.
func (c Coverage) freshness() string {
	if c.LiveFrom.IsZero() {
		return "no live transcripts on disk · archive only"
	}
	reach := "nothing archived before that"
	if c.ArchiveReaches {
		reach = "archived before that"
	}
	line := fmt.Sprintf("live to %s · %s", Day(c.LiveFrom), reach)
	switch {
	case !c.Refreshed:
		line += " · not refreshed (--no-update)"
	case c.RefreshedAgo != "":
		line += " · refreshed " + c.RefreshedAgo
	}
	return line
}

// TierClause names what was not searched. The two-tier wording is pinned by the
// build contract and is the one a default `find` prints.
func TierClause(unsearched []schema.Tier) string {
	switch strings.Join(tierNames(unsearched), ",") {
	case "":
		return "every tier searched"
	case "invocation,result":
		return "conversation only — tool output NOT searched (--results)"
	case "result":
		return "conversation and tool calls — tool output NOT searched (--results)"
	case "invocation":
		return "conversation and tool output — tool calls NOT searched (--tools)"
	default:
		return "NOT searched: " + strings.Join(tierNames(unsearched), ", ")
	}
}

func tierNames(tiers []schema.Tier) []string {
	out := make([]string, len(tiers))
	for i, t := range tiers {
		out[i] = string(t)
	}
	return out
}
