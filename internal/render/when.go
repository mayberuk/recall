package render

import (
	"fmt"
	"strings"
	"time"

	"github.com/mayberuk/recall/internal/style"
)

// Bucket is one month of the timeline. Months rather than days because the
// question `when` answers is "roughly when did this come up", and a daily
// histogram over a two-month corpus is a list of mostly-zero rows.
type Bucket struct {
	Month    string `json:"month"`
	Hits     int    `json:"hits"`
	Sessions int    `json:"sessions"`
}

// When is the whole result of `recall when`.
type When struct {
	Verb      string      `json:"verb"`
	Query     string      `json:"query"`
	Scope     Scope       `json:"scope"`
	Hits      int         `json:"hits"`
	First     string      `json:"first,omitempty"`
	Last      string      `json:"last,omitempty"`
	Buckets   []Bucket    `json:"buckets,omitempty"`
	Sessions  []Session   `json:"sessions"`
	Elsewhere []Elsewhere `json:"elsewhere,omitempty"`
	Terms     []Term      `json:"terms,omitempty"`
	Coverage  Coverage    `json:"coverage"`
	// pal is unexported so encoding/json cannot reach it: colour is
	// structurally unable to arrive in --json or --format jsonl.
	pal style.Palette
}

// WithPalette returns a copy that renders its text form in colour. The zero
// palette is the default, so a caller that never asks gets plain bytes.
func (w When) WithPalette(p style.Palette) When { w.pal = p; return w }

// Text is the human form of a when.
func (w When) Text() []byte { return w.text(false) }

// Brief is the timeline with the snippets left out.
func (w When) Brief() []byte { return w.text(true) }

// JSONL is one object per shown match, then the coverage record, matching what
// find emits so a caller reads both verbs with one parser.
func (w When) JSONL() ([]byte, error) {
	return Find{Query: w.Query, Hits: w.Hits, Sessions: w.Sessions, Coverage: w.Coverage}.JSONL()
}

// IDs is the session ids alone, oldest first.
func (w When) IDs() []byte {
	var b strings.Builder
	for _, s := range w.Sessions {
		b.WriteString(s.ID)
		b.WriteByte('\n')
	}
	return []byte(b.String())
}

func (w When) text(brief bool) []byte {
	var b strings.Builder
	if len(w.Sessions) == 0 {
		where := "on this machine"
		if !w.Scope.All && w.Scope.Repo != "" {
			where = "in " + w.Scope.Repo
		}
		fmt.Fprintf(&b, "no hits for %s %s\n", quote(w.Query), where)
		writeElsewhere(&b, w.Query, w.Elsewhere, w.pal)
		writeTerms(&b, w.Terms, w.pal)
		writeLines(&b, w.Coverage.Lines(), w.pal)
		return []byte(b.String())
	}

	fmt.Fprintf(&b, "%s  first %s · last %s · %d %s in %d %s\n",
		quote(w.Query), dash(w.First), dash(w.Last),
		w.Hits, plural(w.Hits, "hit", "hits"),
		len(w.Sessions), plural(len(w.Sessions), "session", "sessions"))
	for _, bk := range w.Buckets {
		fmt.Fprintf(&b, "  %s  %4d %s · %d %s\n", bk.Month,
			bk.Hits, plural(bk.Hits, "hit", "hits"), bk.Sessions, plural(bk.Sessions, "session", "sessions"))
	}
	b.WriteString("\noldest first\n")
	for _, s := range w.Sessions {
		s.writeAs(&b, brief, w.pal)
	}
	writeLines(&b, w.Coverage.Lines(), w.pal)
	return []byte(b.String())
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func parseStamp(ts string) time.Time {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return time.Time{}
	}
	return t
}
