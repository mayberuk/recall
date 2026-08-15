package render

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/mayberuk/recall/internal/schema"
)

// Turn is one turn printed in full. Show is the verb that recovers a conclusion
// and its reasoning, so the words are not snippeted here — the byte cap bounds
// the answer instead, by refusing.
type Turn struct {
	Index  int           `json:"index"`
	UUID   string        `json:"uuid"`
	TS     string        `json:"ts"`
	Tier   schema.Tier   `json:"tier"`
	Author schema.Author `json:"author"`
	Agent  string        `json:"agent,omitempty"`
	Match  bool          `json:"match"`
	Text   string        `json:"text"`

	// Truncated says --chars cut this turn, and Length is what it was. A cut
	// that does not say so is the failure the byte cap exists to prevent.
	Truncated bool `json:"truncated,omitempty"`
	Length    int  `json:"length,omitempty"`
}

// Window is a run of consecutive turns around one or more matches. Overlapping
// windows are merged before they reach here, so a turn is never printed twice.
type Window struct {
	From  int    `json:"from"`
	To    int    `json:"to"`
	Turns []Turn `json:"turns"`
}

// Show is the whole result of `recall show`.
type Show struct {
	Verb    string `json:"verb"`
	Session string `json:"session"`
	Repo    string `json:"repo,omitempty"`
	Branch  string `json:"branch,omitempty"`
	Query   string `json:"query,omitempty"`
	Anchor  string `json:"anchor"`

	// Turns counts the tiers being shown, so the same session reports 112
	// without --results and 245 with it. Tiers is printed beside it, because
	// two runs quoting different totals for one session reads as a
	// contradiction rather than as two different questions.
	Turns    int           `json:"turns"`
	Tiers    []schema.Tier `json:"tiers"`
	Shown    int           `json:"shown"`
	Matches  int           `json:"matches"`
	Fitted   int           `json:"fitted"`
	Full     bool          `json:"full"`
	Windows  []Window      `json:"windows"`
	Coverage Coverage      `json:"coverage"`
}

// JSONL is one object per printed turn, then the coverage record.
func (s Show) JSONL() ([]byte, error) {
	var b bytes.Buffer
	for _, w := range s.Windows {
		for _, t := range w.Turns {
			line, err := JSON(turnLine{Type: "turn", Session: s.Session, Repo: s.Repo, Branch: s.Branch, Query: s.Query, Turn: t})
			if err != nil {
				return nil, err
			}
			b.Write(line)
		}
	}
	line, err := JSON(coverageLine{Type: "coverage", Query: s.Query, Sessions: 1, Coverage: s.Coverage})
	if err != nil {
		return nil, err
	}
	b.Write(line)
	return b.Bytes(), nil
}

type turnLine struct {
	Type    string `json:"type"`
	Session string `json:"session"`
	Repo    string `json:"repo,omitempty"`
	Branch  string `json:"branch,omitempty"`
	Query   string `json:"query,omitempty"`
	Turn
}

// Anchors are how a window was chosen: around each match, around one named
// turn, or the end of the session when the caller named neither.
const (
	AnchorQuery = "query"
	AnchorTurn  = "turn"
	AnchorTail  = "tail"
	AnchorFull  = "full"
)

// Text is the human form of a show.
func (s Show) Text() []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "%s  %s\n", s.Session, strings.Join(nonEmpty(
		s.Repo, s.Branch, s.turnCount(),
	), "  "))
	if s.Anchor == AnchorTail && s.Query == "" {
		b.WriteString("no query given, so this is the end of the session\n")
	}
	if len(s.Windows) == 0 {
		fmt.Fprintf(&b, "nothing to show for %s in this session\n", quote(s.Query))
	}
	for _, w := range s.Windows {
		w.write(&b, s.Turns)
	}
	writeLines(&b, s.Coverage.Lines())
	return []byte(b.String())
}

// turnCount names the tiers it counted. Without that the number changes under
// --results and looks like the tool disagreeing with itself.
func (s Show) turnCount() string {
	names := make([]string, 0, len(s.Tiers))
	for _, t := range s.Tiers {
		names = append(names, string(t))
	}
	if len(names) == 0 {
		names = append(names, string(schema.TierConversation))
	}
	return fmt.Sprintf("%d %s (%s)", s.Turns, plural(s.Turns, "turn", "turns"), strings.Join(names, " + "))
}

func (w Window) write(b *strings.Builder, total int) {
	fmt.Fprintf(b, "\nturns %d-%d of %d\n", w.From+1, w.To+1, total)
	for _, t := range w.Turns {
		t.write(b)
	}
}

func (t Turn) write(b *strings.Builder) {
	mark := "  "
	if t.Match {
		mark = "> "
	}
	who := string(t.Author)
	if t.Agent != "" {
		who += "/" + t.Agent
	}
	if t.Tier != schema.TierConversation {
		who += " [" + string(t.Tier) + "]"
	}
	fmt.Fprintf(b, "%s%s  %s\n", mark, Day(parseStamp(t.TS)), who)
	for _, line := range strings.Split(t.Text, "\n") {
		b.WriteString("    ")
		b.WriteString(line)
		b.WriteByte('\n')
	}
	if t.Truncated {
		fmt.Fprintf(b, "    … %d bytes in this turn; --chars 0 for all of it\n", t.Length)
	}
}
