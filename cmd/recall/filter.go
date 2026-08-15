package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mayberuk/recall/internal/fperr"
	"github.com/mayberuk/recall/internal/render"
	"github.com/mayberuk/recall/internal/schema"
)

// stringList is a flag that may be given more than once, so `--not a --not b`
// accumulates instead of the last one winning.
type stringList []string

func (l *stringList) String() string { return strings.Join(*l, ",") }

func (l *stringList) Set(v string) error {
	*l = append(*l, v)
	return nil
}

// relativeUnits are the suffixes an agent writes without being told they exist.
// "last week" is one of the four question shapes and there was no time filter
// at all, so the spellings are deliberately forgiving.
var relativeUnits = map[byte]time.Duration{
	'h': time.Hour,
	'd': 24 * time.Hour,
	'w': 7 * 24 * time.Hour,
	'm': 30 * 24 * time.Hour,
	'y': 365 * 24 * time.Hour,
}

// parseWhen reads either a relative age (`2w`, `3d`, `12h`) or a date. Relative
// forms are subtracted from now, which is what makes `--since 2w` mean the last
// two weeks rather than a fortnight after the epoch.
func parseWhen(flag, v string, now time.Time) (time.Time, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return time.Time{}, nil
	}
	if d, ok := relative(v); ok {
		return now.Add(-d), nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02 15:04", "2006-01-02", "2006-01"} {
		if t, err := time.Parse(layout, v); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fperr.New(fperr.ArgError,
		"%s takes an age like 2w, 3d or 12h, or a date like 2026-08-01, got %q", flag, v)
}

func relative(v string) (time.Duration, bool) {
	if len(v) < 2 {
		return 0, false
	}
	unit, ok := relativeUnits[v[len(v)-1]|0x20]
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(v[:len(v)-1])
	if err != nil || n < 0 {
		return 0, false
	}
	return time.Duration(n) * unit, true
}

func parseAuthor(v string) (schema.Author, error) {
	switch schema.Author(strings.ToLower(strings.TrimSpace(v))) {
	case "":
		return "", nil
	case schema.AuthorHuman:
		return schema.AuthorHuman, nil
	case schema.AuthorAssistant:
		return schema.AuthorAssistant, nil
	case schema.AuthorAgent:
		return schema.AuthorAgent, nil
	case schema.AuthorSystem:
		return schema.AuthorSystem, nil
	default:
		return "", fperr.New(fperr.ArgError,
			"--author takes human, assistant, agent or system, got %q", v)
	}
}

// filter is everything that decides whether one turn is searched at all. It is
// built once per query and handed to the scanner, which counts the turns it
// rejects: a narrowing that changed the answer has to be visible, and the
// coverage line's searched-of-total figures are where it shows.
type filter struct {
	author schema.Author

	// authorFlag is the spelling the caller used, since --mine and
	// --author human are the same narrowing and the footer should quote back
	// the one that was typed.
	authorFlag string

	branch  string
	agent   string
	session string
	since   time.Time
	until   time.Time

	// self is the session doing the asking. Asking what happened before and
	// getting your own session back is worse than useless, so it is excluded by
	// default; --include-self clears this.
	self string

	// dropRecall drops recall's own recorded command lines and output. The tool
	// reads the transcripts it is itself recorded in, so its results rank top
	// for the query being asked, and it gets worse the more it is used.
	dropRecall bool

	// dropped counts what each default exclusion actually removed, so the
	// coverage line reports a narrowing that happened rather than one that was
	// merely configured.
	dropped *drops
}

type drops struct{ self, recall int }

// snapshot is what the exclusions have removed so far. A caller takes one right
// after the search it wants to report on, because the same predicate runs again
// for the wider probe and for the terms-nearby survey.
func (f filter) snapshot() drops {
	if f.dropped == nil {
		return drops{}
	}
	return *f.dropped
}

func (f filter) empty() bool {
	return f.author == "" && f.branch == "" && f.agent == "" && f.session == "" &&
		f.since.IsZero() && f.until.IsZero() && f.self == "" && !f.dropRecall
}

// keep is the predicate handed to the scanner, or nil when nothing narrows.
func (f *filter) keep() func(*schema.Turn) bool {
	if f.empty() {
		return nil
	}
	if f.dropped == nil {
		f.dropped = &drops{}
	}
	return func(t *schema.Turn) bool {
		switch {
		case f.author != "" && t.Author != f.author:
			return false
		case f.branch != "" && !strings.EqualFold(t.Branch, f.branch):
			return false
		case f.agent != "" && !strings.Contains(strings.ToLower(t.Agent), strings.ToLower(f.agent)):
			return false
		case f.session != "" && !strings.HasPrefix(t.Session, f.session):
			return false
		case f.self != "" && t.Session == f.self:
			f.dropped.self++
			return false
		case f.dropRecall && isRecallOutput(t):
			f.dropped.recall++
			return false
		}
		if f.since.IsZero() && f.until.IsZero() {
			return true
		}
		ts, err := time.Parse(time.RFC3339, t.TS)
		if err != nil {
			// An undated turn cannot be placed, and dropping it would be a
			// false negative produced by a filter it was never tested against.
			return true
		}
		if !f.since.IsZero() && ts.Before(f.since) {
			return false
		}
		return f.until.IsZero() || !ts.After(f.until)
	}
}

// recallVerbs are the command words that make a shell line a recall invocation.
var recallVerbs = [...]string{"find ", "show ", "when ", "doctor", "guide", "turns "}

// isRecallOutput reports whether a turn is recall talking to itself: its own
// command line recorded as a tool call, or its own output recorded as a tool
// result. The coverage line is the fingerprint for the output, because it is
// emitted by every searching command and by nothing else on this machine.
func isRecallOutput(t *schema.Turn) bool {
	switch t.Tier {
	case schema.TierResult:
		return strings.Contains(t.Text, "── ") && strings.Contains(t.Text, " searched · ")
	case schema.TierInvocation:
		at := strings.Index(t.Text, "recall ")
		if at < 0 || (at > 0 && isShellWordByte(t.Text[at-1])) {
			return false
		}
		rest := t.Text[at+len("recall "):]
		for _, v := range recallVerbs {
			if strings.HasPrefix(rest, v) {
				return true
			}
		}
		return strings.HasPrefix(rest, "--")
	}
	return false
}

func isShellWordByte(c byte) bool {
	return c == '/' || c == '-' || c == '_' || c == '.' ||
		(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// limits are the filter's typed entries for the coverage line, so a caller
// reading JSON sees the same narrowing the text form prints. Every one of these
// can turn a real hit into a zero, so none is applied silently.
func (f filter) limits(scannedTurns, totalTurns int) []render.Limit {
	var named []string
	if f.author != "" {
		named = append(named, f.authorFlag)
	}
	if f.branch != "" {
		named = append(named, "--branch "+f.branch)
	}
	if f.agent != "" {
		named = append(named, "--agent "+f.agent)
	}
	if f.session != "" {
		named = append(named, "--session "+f.session)
	}
	if !f.since.IsZero() {
		named = append(named, "--since "+f.since.Format("2006-01-02"))
	}
	if !f.until.IsZero() {
		named = append(named, "--until "+f.until.Format("2006-01-02"))
	}
	if len(named) == 0 {
		return nil
	}
	return []render.Limit{{
		Flag:  strings.Join(named, " "),
		What:  "turns",
		Shown: scannedTurns,
		Total: totalTurns,
	}}
}

// narrowings are the two exclusions recall applies without being asked. They
// are reported as counts of what was skipped rather than as caps, because that
// is the number that tells a caller whether to re-run with them off.
func (d drops) narrowings() []string {
	var out []string
	if d.self > 0 {
		out = append(out, fmt.Sprintf(
			"%d turns of your own session were skipped (--include-self)", d.self))
	}
	if d.recall > 0 {
		out = append(out, fmt.Sprintf(
			"%d turns of recall's own commands and output were skipped (--include-recall)", d.recall))
	}
	return out
}
