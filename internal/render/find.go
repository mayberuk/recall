package render

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/mayberuk/recall/internal/schema"
	"github.com/mayberuk/recall/internal/style"
)

// Hit is one match as it will be shown: the snippet is already cut, because the
// turn it came from can be megabytes and the byte cap is checked against what is
// about to be printed.
type Hit struct {
	UUID   string        `json:"uuid"`
	TS     string        `json:"ts"`
	Tier   schema.Tier   `json:"tier"`
	Author schema.Author `json:"author"`
	Agent  string        `json:"agent,omitempty"`
	Offset int           `json:"offset"`
	Length int           `json:"length"`

	// Occurrences is how many times the query matched inside this one turn. The
	// line is printed once whatever that number is.
	Occurrences int              `json:"occurrences"`
	Match       schema.MatchKind `json:"match"`
	Terms       int              `json:"terms"`
	Snippet     string           `json:"snippet"`
}

// Session is one ranked session. Hits is the whole count; Shown is the subset
// printed, and the difference is declared in the coverage line.
type Session struct {
	ID         string  `json:"id"`
	Repo       string  `json:"repo"`
	Branch     string  `json:"branch,omitempty"`
	Hits       int     `json:"hits"`
	HitTurns   int     `json:"hit_turns"`
	Turns      int     `json:"turns"`
	TurnsKnown bool    `json:"turns_known"`
	AgentHits  int     `json:"agent_hits"`
	Score      float64 `json:"score"`
	First      string  `json:"first,omitempty"`
	Last       string  `json:"last,omitempty"`
	Shown      []Hit   `json:"shown"`
}

// Elsewhere is a repo outside the searched scope that does carry the query. A
// repo-scoped miss reports these instead of a bare zero: the dealbreaker is
// reporting nothing found when the thing is present somewhere on the machine.
type Elsewhere struct {
	Repo     string `json:"repo"`
	Hits     int    `json:"hits"`
	Sessions int    `json:"sessions"`
}

// Term is one query term's fate on a search that found nothing, which converts
// a dead end into the next query.
type Term struct {
	Term   string   `json:"term"`
	Turns  int      `json:"turns"`
	Nearby []string `json:"nearby,omitempty"`
}

// Find is the whole result of `recall find`, and the JSON form a caller reads
// instead of parsing the text.
type Find struct {
	Verb      string      `json:"verb"`
	Query     string      `json:"query"`
	Scope     Scope       `json:"scope"`
	Sort      string      `json:"sort"`
	Hits      int         `json:"hits"`
	Redundant int         `json:"redundant"`
	Sessions  []Session   `json:"sessions"`
	Elsewhere []Elsewhere `json:"elsewhere,omitempty"`
	Terms     []Term      `json:"terms,omitempty"`
	Repos     []Facet     `json:"repos,omitempty"`
	Authors   []Facet     `json:"authors,omitempty"`
	Tiers     []Facet     `json:"tiers,omitempty"`
	Coverage  Coverage    `json:"coverage"`

	// pal is unexported on purpose. encoding/json cannot reach an unexported
	// field, so it is structurally impossible for an escape byte to arrive in
	// --json or --format jsonl no matter what a future caller does here.
	pal style.Palette
}

// WithPalette returns a copy that renders its text form in colour. The zero
// palette is the default, so a caller that never asks gets the plain bytes this
// package emitted before colour existed.
func (f Find) WithPalette(p style.Palette) Find { f.pal = p; return f }

// Facet is one value of a facet field with its deduplicated hit and session
// counts.
type Facet struct {
	Value    string `json:"value"`
	Hits     int    `json:"hits"`
	Sessions int    `json:"sessions"`
}

// Text is the human form of a find.
func (f Find) Text() []byte { return f.text(false) }

// Brief is the same answer with the snippets left out: one line per session,
// for triage at roughly a tenth of the context.
func (f Find) Brief() []byte { return f.text(true) }

func (f Find) text(brief bool) []byte {
	var b strings.Builder
	if len(f.Sessions) == 0 {
		f.writeMiss(&b)
	} else {
		fmt.Fprintf(&b, "%d %s · %d %s for %s\n",
			len(f.Sessions), plural(len(f.Sessions), "session", "sessions"),
			f.Hits, plural(f.Hits, "hit", "hits"), quote(f.Query))
		for _, s := range f.Sessions {
			s.writeAs(&b, brief, f.pal)
		}
	}
	writeLines(&b, f.Coverage.Lines(), f.pal)
	return []byte(b.String())
}

// IDs is the session ids alone, one per line, so `recall find X --ids | head -1
// | xargs recall show` composes without a JSON parser in the middle.
func (f Find) IDs() []byte {
	var b strings.Builder
	for _, s := range f.Sessions {
		b.WriteString(s.ID)
		b.WriteByte('\n')
	}
	return []byte(b.String())
}

// JSONL is one object per line: every shown match, then the coverage record. A
// caller streams or pipes it without holding the whole answer, and the type
// field says which kind of line it is looking at.
func (f Find) JSONL() ([]byte, error) {
	var b bytes.Buffer
	for _, s := range f.Sessions {
		for _, h := range s.Shown {
			line, err := JSON(matchLine{
				Type: "match", Session: s.ID, Repo: s.Repo, Branch: s.Branch,
				Query: f.Query, Hit: h,
			})
			if err != nil {
				return nil, err
			}
			b.Write(line)
		}
	}
	line, err := JSON(coverageLine{Type: "coverage", Query: f.Query, Sessions: len(f.Sessions), Hits: f.Hits, Coverage: f.Coverage})
	if err != nil {
		return nil, err
	}
	b.Write(line)
	return b.Bytes(), nil
}

type matchLine struct {
	Type    string `json:"type"`
	Session string `json:"session"`
	Repo    string `json:"repo,omitempty"`
	Branch  string `json:"branch,omitempty"`
	Query   string `json:"query"`
	Hit
}

type coverageLine struct {
	Type     string   `json:"type"`
	Query    string   `json:"query"`
	Sessions int      `json:"sessions"`
	Hits     int      `json:"hits"`
	Coverage Coverage `json:"coverage"`
}

func (f Find) writeMiss(b *strings.Builder) {
	where := "on this machine"
	if !f.Scope.All && f.Scope.Repo != "" {
		where = "in " + f.Scope.Repo
	}
	fmt.Fprintf(b, "no hits for %s %s\n", quote(f.Query), where)
	writeElsewhere(b, f.Query, f.Elsewhere, f.pal)
	writeTerms(b, f.Terms, f.pal)
}

// writeElsewhere is acceptance case a5: a repo-scoped query that found nothing
// locally reports what the rest of the machine holds, and the command that
// reaches it.
func writeElsewhere(b *strings.Builder, query string, elsewhere []Elsewhere, p style.Palette) {
	if len(elsewhere) == 0 {
		return
	}
	hits, repos := 0, len(elsewhere)
	for _, e := range elsewhere {
		hits += e.Hits
	}
	fmt.Fprintf(b, "found elsewhere: %d %s in %d other %s\n",
		hits, plural(hits, "hit", "hits"), repos, plural(repos, "repo", "repos"))
	for _, e := range elsewhere {
		fmt.Fprintf(b, "  %-40s %d %s · %d %s\n",
			e.Repo, e.Hits, plural(e.Hits, "hit", "hits"), e.Sessions, plural(e.Sessions, "session", "sessions"))
	}
	// a line the reader is meant to retype, so it carries the handle attribute
	fmt.Fprintf(b, "run: %s\n", p.Handle("recall find "+shellArg(query)+" --all"))
}

func writeTerms(b *strings.Builder, terms []Term, p style.Palette) {
	for _, t := range terms {
		// pad first, then style: escape bytes counted by %-20s would collapse
		// the column to a couple of visible characters
		term := p.Key(fmt.Sprintf("%-20s", t.Term))
		switch {
		case t.Turns > 0:
			fmt.Fprintf(b, "  %s %d %s carry it, but not together with the rest\n",
				term, t.Turns, plural(t.Turns, "turn", "turns"))
		case len(t.Nearby) > 0:
			fmt.Fprintf(b, "  %s no turn carries it; nearby: %s\n", term, p.Handle(strings.Join(t.Nearby, ", ")))
		default:
			fmt.Fprintf(b, "  %s no turn carries it, and nothing in the corpus is close\n", term)
		}
	}
}

// Block is one session as the human form prints it: the heading line and its
// hit lines. The interactive record stream embeds it verbatim, so the two
// surfaces cannot drift into showing different things.
func (s Session) Block() string {
	var b strings.Builder
	s.write(&b)
	return b.String()
}

func (s Session) write(b *strings.Builder) { s.writeAs(b, false, style.Palette{}) }

func (s Session) writeAs(b *strings.Builder, brief bool, p style.Palette) {
	fmt.Fprintf(b, "%s  %s\n", p.Handle(s.ID), strings.Join(nonEmpty(
		dateRange(s.First, s.Last), s.Repo, s.Branch, s.tally(), agentNote(s.AgentHits),
	), "  "))
	if brief {
		return
	}
	for _, h := range s.Shown {
		h.write(b, p)
	}
}

// tally states the three numbers separately, because they answer different
// questions and conflating them was confusing: how often the query matched, how
// many turns those matches sit in, and how long the session is.
func (s Session) tally() string {
	turns := fmt.Sprintf("%d turns", s.Turns)
	if !s.TurnsKnown {
		turns = "turn count unknown"
	}
	if s.HitTurns > 0 && s.HitTurns != s.Hits {
		return fmt.Sprintf("%d %s in %d %s of %s",
			s.Hits, plural(s.Hits, "hit", "hits"),
			s.HitTurns, plural(s.HitTurns, "turn", "turns"), turns)
	}
	return fmt.Sprintf("%d %s of %s", s.Hits, plural(s.Hits, "hit", "hits"), turns)
}

func dateRange(first, last string) string {
	switch {
	case first == "":
		return last
	case last == "" || first == last:
		return first
	default:
		return first + ".." + last
	}
}

func agentNote(n int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("%d from subagents", n)
}

func (h Hit) write(b *strings.Builder, p style.Palette) {
	tag := string(h.Author)
	if h.Tier != schema.TierConversation {
		tag = string(h.Tier)
	}
	repeat := ""
	if h.Occurrences > 1 {
		repeat = p.Quiet(fmt.Sprintf(" ×%d", h.Occurrences))
	}
	// padded before styling, for the same column reason as writeTerms
	fmt.Fprintf(b, "  %s %s%s\n", p.Key(fmt.Sprintf("%-10s", tag)), styleSnippet(p, h.Snippet), repeat)
}

// styleSnippet inverts the matched words, guillemets included, so the marker
// reads as one solid block rather than as punctuation with a highlight behind
// it.
//
// The brackets stay. Dropping them would read a shade cleaner and would make
// colour subtract content, and every size this tool reports is measured by
// stripping the attributes back off: a terminal and a pipe would then be told
// two different numbers for the same answer. A tool that prices its own output
// does not get to price it differently depending on who is looking.
func styleSnippet(p style.Palette, s string) string {
	if !p.Enabled() {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	rest := s
	for {
		open := strings.Index(rest, MarkOpen)
		if open < 0 {
			break
		}
		close := strings.Index(rest[open+len(MarkOpen):], MarkClose)
		if close < 0 {
			break
		}
		hit := rest[open : open+len(MarkOpen)+close+len(MarkClose)]
		b.WriteString(rest[:open])
		b.WriteString(p.Match(hit))
		rest = rest[open+len(MarkOpen)+close+len(MarkClose):]
	}
	b.WriteString(rest)
	out := b.String()

	// the edge elisions say "there was more here", which is context, not content
	if strings.HasPrefix(out, "…") {
		out = p.Quiet("…") + strings.TrimPrefix(out, "…")
	}
	if strings.HasSuffix(out, "…") {
		out = strings.TrimSuffix(out, "…") + p.Quiet("…")
	}
	return out
}

// writeLines emits the coverage footer. Every line of it recedes: it is there
// for the reader who wants to know what was not searched, and out of the way of
// the one who already has their answer.
func writeLines(b *strings.Builder, lines []string, p style.Palette) {
	for _, l := range lines {
		b.WriteString(p.Quiet(l))
		b.WriteByte('\n')
	}
}

func nonEmpty(vals ...string) []string {
	out := make([]string, 0, len(vals))
	for _, v := range vals {
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

func quote(s string) string { return `"` + s + `"` }

// shellArg quotes a query for the copy-pasteable next command, so a suggestion
// containing spaces is one argument when it is pasted back.
func shellArg(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t'\"\\$`") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Separators for the interactive record stream. fzf resolves {1} in --preview
// and in every key binding by splitting on the field separator, and --read0
// needs records terminated rather than separated, so the last record carries a
// trailing NUL too.
const (
	fzfFieldSep  = "\x1f"
	fzfRecordEnd = "\x00"
)

// FZF is the record stream the interactive front end reads: one
// NUL-terminated `<session id>\x1f<block>` per ranked session, in recall's own
// order. Field 1 is the bare session id and nothing else, so --with-nth can
// hide it while --preview and --track still address it.
//
// The second return is what could not become a record. A record whose field 1
// is not a session id would break every binding, so a search with no sessions
// puts its report — coverage line included — there instead, for stderr.
func (f Find) FZF() (records, note []byte) {
	if len(f.Sessions) == 0 {
		var b strings.Builder
		f.writeMiss(&b)
		// the fzf surfaces stay plain: field 1 is parsed as a session id by the
		// binding, and an attribute there breaks every one of them
		writeLines(&b, f.Coverage.Lines(), style.Palette{})
		return nil, []byte(b.String())
	}

	var b strings.Builder
	for i, s := range f.Sessions {
		b.WriteString(s.ID)
		b.WriteString(fzfFieldSep)
		b.WriteString(s.Block())
		// The coverage line is a contract, so it travels with the results
		// rather than being dropped; the last block is the only place it can
		// go without becoming a record fzf would treat as a session.
		if i == len(f.Sessions)-1 {
			writeLines(&b, f.Coverage.Lines(), style.Palette{})
		}
		b.WriteString(fzfRecordEnd)
	}
	return []byte(b.String()), nil
}
