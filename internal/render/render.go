// Package render turns recall's results into its two output forms: short plain
// text, and JSON complete enough that a caller never has to parse the text.
//
// It also owns the coverage line, which every searching command emits. Output
// is built whole in memory and measured before a byte of it is written, because
// the cap has to refuse an oversized answer rather than truncate one — a
// truncated answer looks complete, which is the failure the cap exists to stop.
package render

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mayberuk/recall/internal/fperr"
	"github.com/mayberuk/recall/internal/style"
)

// Emit writes body only if it fits under max, and refuses with the size and the
// flag to raise otherwise.
func Emit(w io.Writer, body []byte, max int64) error {
	// Measured on the content, never on the presentation: colour is bytes the
	// terminal consumes and neither the reader nor an agent ever takes in, so a
	// styled answer must not be refused for a width it does not really have.
	if int64(len(style.Strip(string(body)))) > max {
		return fperr.New(fperr.OutputTooLarge,
			"output is %d bytes, --max-bytes is %d — ask for less (--limit, --hits, --around, no --full) or raise --max-bytes",
			len(style.Strip(string(body))), max)
	}
	_, err := w.Write(body)
	return err
}

// JSON is the machine form: one compact object, newline-terminated.
func JSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, fperr.New(fperr.Internal, "cannot encode the result as JSON: %v", err)
	}
	return buf.Bytes(), nil
}

// WithSize appends the footer line stating what this answer costs. Almost no
// CLI reports its own size; for a caller budgeting a turn of context it is the
// difference between spending blind and spending on purpose.
//
// The line counts itself, which needs a fixpoint: adding it lengthens the body
// it reports. Two rounds settle it unless the digit count changes, and three
// always do.
func WithSize(body []byte) []byte {
	plain := len(style.Strip(string(body)))
	total := plain
	line := ""
	for i := 0; i < 3; i++ {
		next := sizeLine(total)
		if next == line {
			break
		}
		total = plain + len(next)
		line = next
	}
	return append(body, line...)
}

func sizeLine(n int) string {
	return fmt.Sprintf("── %s · ~%d tokens\n", byteSize(n), (n+BytesPerToken-1)/BytesPerToken)
}

// BytesPerToken is the rule of thumb the size footer estimates with. It is
// approximate and says so, because the alternative is a tokenizer dependency
// for a number a caller uses to decide whether to ask for less.
const BytesPerToken = 4

func byteSize(n int) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	case n < 1024*1024*1024:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	default:
		return fmt.Sprintf("%.1f GB", float64(n)/(1024*1024*1024))
	}
}

// duration renders a stat's elapsed milliseconds at the precision a human
// reads fastest at that scale. Rounding a sub-millisecond search to "0 ms"
// would read as no work done, so it keeps a decimal there instead.
func duration(ms float64) string {
	switch {
	case ms < 1:
		return fmt.Sprintf("%.1f ms", ms)
	case ms < 1000:
		return fmt.Sprintf("%.0f ms", ms)
	default:
		return fmt.Sprintf("%.1f s", ms/1000)
	}
}

// DefaultSnippet is how many bytes of a turn a hit line shows.
const DefaultSnippet = 140

// The marks that bracket the matched words inside a snippet. A hit line that
// does not say which words matched makes the reader search the line again.
const (
	MarkOpen  = "«"
	MarkClose = "»"
)

// wordSlack is how far a snippet edge may move to land between words. Beyond
// it, cutting mid-word costs less than the context given up.
const wordSlack = 16

// Snippet is one line of turn text centred on a match, cut between words where
// it can be, with the match marked and runs of whitespace collapsed so a hit
// inside a code block still reads as one line.
func Snippet(text string, offset, length, width int) string {
	if width <= 0 {
		width = DefaultSnippet
	}
	offset, length = locate(text, offset, length)

	pad := (width - length) / 2
	if pad < 0 {
		pad = 0
	}
	end := offset + length
	from := wordStart(text, backward(text, offset-pad), offset)
	to := wordEnd(text, forward(text, end+pad), end)

	out := collapse(mark(text, from, offset, end, to))
	if from > 0 {
		out = "…" + out
	}
	if to < len(text) {
		out += "…"
	}
	return out
}

// Excerpt is at most width bytes of a turn centred on a match, with line breaks
// kept: a conclusion is usually a paragraph, and collapsing it to one line is
// what makes a quoted answer unreadable.
//
// Unlike a snippet it does not mark the match. A passage is the thing a caller
// quotes onward, and guillemets inside a table cell, an identifier or a URL
// corrupt what they paste — the same turn would then read differently through
// `turns` than through `show`.
func Excerpt(text string, offset, length, width int) string {
	if width <= 0 || len(text) <= width {
		return text
	}
	offset, length = locate(text, offset, length)
	end := offset + length

	// With no match to centre on, half the window would fall off the front of
	// the string and the caller would get width/2 bytes for a width it asked
	// for. Anchoring at the start spends the whole budget.
	pad := width - length
	if length > 0 {
		pad /= 2
	}
	if pad < 0 {
		pad = 0
	}
	from := wordStart(text, backward(text, offset-pad), offset)
	to := wordEnd(text, forward(text, end+pad), end)

	out := strings.TrimSpace(text[from:to])
	if from > 0 {
		out = "…" + out
	}
	if to < len(text) {
		out += "…"
	}
	return out
}

// locate clamps a match to the text it claims to sit in. A span that does not
// fit is reported as no span at all rather than as a shorter one somewhere
// else: marking the wrong words is worse than marking none.
func locate(text string, offset, length int) (int, int) {
	if offset < 0 || length < 0 || offset > len(text) || offset+length > len(text) {
		return 0, 0
	}
	return offset, length
}

// mark brackets the matched words, and leaves an empty match unmarked.
func mark(text string, from, offset, end, to int) string {
	if end <= offset {
		return text[from:to]
	}
	return text[from:offset] + MarkOpen + text[offset:end] + MarkClose + text[end:to]
}

// wordStart moves a snippet's left edge forward to just after a space, giving
// up if the nearest one is further than wordSlack or past the match itself.
func wordStart(s string, at, limit int) int {
	if at <= 0 {
		return 0
	}
	for i := at; i < limit && i-at < wordSlack; i++ {
		if isSpace(s[i]) {
			return i + 1
		}
	}
	return at
}

// wordEnd moves a snippet's right edge back to just before a space, giving up
// if that would cut into the match.
func wordEnd(s string, at, floor int) int {
	if at >= len(s) {
		return len(s)
	}
	for i := at; i > floor && at-i < wordSlack; i-- {
		if isSpace(s[i]) {
			return i
		}
	}
	return at
}

func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

// backward and forward move an index onto a rune boundary, so a snippet cut
// through a multi-byte character does not emit a replacement char.
func backward(s string, i int) int {
	if i <= 0 {
		return 0
	}
	for i > 0 && !utf8.RuneStart(s[i]) {
		i--
	}
	return i
}

func forward(s string, i int) int {
	if i >= len(s) {
		return len(s)
	}
	for i < len(s) && !utf8.RuneStart(s[i]) {
		i++
	}
	return i
}

func collapse(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	space := false
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			space = true
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		b.WriteRune(r)
	}
	return b.String()
}

// Day is a date for human output, empty for a zero time.
func Day(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02")
}

// Stamp is a time for JSON output, empty for a zero time.
func Stamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
