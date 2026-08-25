package render

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/mayberuk/recall/internal/schema"
	"github.com/mayberuk/recall/internal/style"
)

// Passage is one matched turn, quoted rather than summarised, stamped with
// where it came from so a caller can cite it and jump back to it.
type Passage struct {
	Session string        `json:"session"`
	UUID    string        `json:"uuid"`
	Cite    string        `json:"cite"`
	TS      string        `json:"ts"`
	Repo    string        `json:"repo,omitempty"`
	Branch  string        `json:"branch,omitempty"`
	Tier    schema.Tier   `json:"tier"`
	Author  schema.Author `json:"author"`
	Agent   string        `json:"agent,omitempty"`

	Occurrences int    `json:"occurrences"`
	Terms       int    `json:"terms"`
	Text        string `json:"text"`
	Truncated   bool   `json:"truncated"`
	Length      int    `json:"length"`
}

// Turns is the whole result of `recall turns`: the passages themselves, ranked
// across every session at once.
//
// It exists because the answer to "what did we conclude" is a passage, and
// reaching one used to cost find, then a choice, then show — three round trips,
// each one a chance for a caller to give up.
type Turns struct {
	Verb      string      `json:"verb"`
	Query     string      `json:"query"`
	Scope     Scope       `json:"scope"`
	Hits      int         `json:"hits"`
	Matched   int         `json:"matched_turns"`
	Passages  []Passage   `json:"passages"`
	Elsewhere []Elsewhere `json:"elsewhere,omitempty"`
	Terms     []Term      `json:"terms,omitempty"`
	Coverage  Coverage    `json:"coverage"`
	// pal is unexported so encoding/json cannot reach it: colour is
	// structurally unable to arrive in --json or --format jsonl.
	pal style.Palette
}

// WithPalette returns a copy that renders its text form in colour. The zero
// palette is the default, so a caller that never asks gets plain bytes.
func (t Turns) WithPalette(p style.Palette) Turns { t.pal = p; return t }

// Text is the human form: a header per passage, then its words.
func (t Turns) Text() []byte {
	var b strings.Builder
	if len(t.Passages) == 0 {
		where := "on this machine"
		if !t.Scope.All && t.Scope.Repo != "" {
			where = "in " + t.Scope.Repo
		}
		fmt.Fprintf(&b, "no turns carry %s %s\n", quote(t.Query), where)
		writeElsewhere(&b, t.Query, t.Elsewhere, t.pal)
		writeTerms(&b, t.Terms, t.pal)
		writeLines(&b, t.Coverage.Lines(), t.pal)
		return []byte(b.String())
	}

	fmt.Fprintf(&b, "%d of %d matched %s for %s\n",
		len(t.Passages), t.Matched, plural(t.Matched, "turn", "turns"), quote(t.Query))
	for _, psg := range t.Passages {
		psg.write(&b, t.pal)
	}
	writeLines(&b, t.Coverage.Lines(), t.pal)
	return []byte(b.String())
}

// Brief drops the passages' words and keeps their citations, which is the
// cheapest form that still lets a caller pick one to fetch in full.
func (t Turns) Brief() []byte {
	var b strings.Builder
	for _, p := range t.Passages {
		fmt.Fprintf(&b, "%s  %s\n", t.pal.Handle(p.Cite), strings.Join(nonEmpty(
			Day(parseStamp(p.TS)), p.Repo, p.Branch, p.who()), "  "))
	}
	writeLines(&b, t.Coverage.Lines(), t.pal)
	return []byte(b.String())
}

// IDs is one citation per line, ready to pass back to `recall show --turn`.
func (t Turns) IDs() []byte {
	var b strings.Builder
	for _, p := range t.Passages {
		b.WriteString(p.Cite)
		b.WriteByte('\n')
	}
	return []byte(b.String())
}

// JSONL is one passage per line, then the coverage record.
func (t Turns) JSONL() ([]byte, error) {
	var b bytes.Buffer
	for _, p := range t.Passages {
		line, err := JSON(passageLine{Type: "passage", Query: t.Query, Passage: p})
		if err != nil {
			return nil, err
		}
		b.Write(line)
	}
	line, err := JSON(coverageLine{Type: "coverage", Query: t.Query, Sessions: t.sessions(), Hits: t.Hits, Coverage: t.Coverage})
	if err != nil {
		return nil, err
	}
	b.Write(line)
	return b.Bytes(), nil
}

type passageLine struct {
	Type  string `json:"type"`
	Query string `json:"query"`
	Passage
}

func (t Turns) sessions() int {
	seen := map[string]struct{}{}
	for _, p := range t.Passages {
		seen[p.Session] = struct{}{}
	}
	return len(seen)
}

func (p Passage) write(b *strings.Builder, pal style.Palette) {
	fmt.Fprintf(b, "\n%s  %s\n", pal.Handle(p.Cite), strings.Join(nonEmpty(
		Day(parseStamp(p.TS)), p.Repo, p.Branch, p.who(), pal.Quiet(p.repeat())), "  "))
	for _, line := range strings.Split(p.Text, "\n") {
		b.WriteString("    ")
		b.WriteString(line)
		b.WriteByte('\n')
	}
	if p.Truncated {
		// The backticks stay inside the attribute for the same reason the
		// guillemets do in styleSnippet: colour may add to an answer, never
		// subtract from it, because every reported size is measured by
		// stripping the attributes back off.
		cmd := pal.Handle(fmt.Sprintf("`recall show %s --turn %s`", short(p.Session), p.UUID))
		fmt.Fprintf(b, "    %s\n", pal.Quiet(fmt.Sprintf("… %d bytes in this turn; ", p.Length))+
			cmd+pal.Quiet(" for all of it"))
	}
}

func (p Passage) who() string {
	who := string(p.Author)
	if p.Agent != "" {
		who += "/" + p.Agent
	}
	if p.Tier != schema.TierConversation {
		who += " [" + string(p.Tier) + "]"
	}
	return who
}

func (p Passage) repeat() string {
	if p.Occurrences > 1 {
		return fmt.Sprintf("×%d", p.Occurrences)
	}
	return ""
}

// short is the prefix length `show` resolves, which is what a suggested command
// should paste as.
func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
