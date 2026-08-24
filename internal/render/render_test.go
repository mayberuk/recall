package render

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mayberuk/recall/internal/fperr"
)

// TestEmitRefusesRatherThanTruncates is the dealbreaker in miniature: a
// truncated answer looks complete, so the cap declines to write anything.
func TestEmitRefusesRatherThanTruncates(t *testing.T) {
	body := []byte(strings.Repeat("x", 101))
	var out bytes.Buffer
	err := Emit(&out, body, 100)
	if err == nil {
		t.Fatal("101 bytes under a 100 byte cap was emitted")
	}
	var fe *fperr.Error
	if !errors.As(err, &fe) || fe.Code != fperr.OutputTooLarge {
		t.Errorf("code = %v, want %v", err, fperr.OutputTooLarge)
	}
	if out.Len() != 0 {
		t.Errorf("wrote %d bytes on a refusal; a partial write is a truncation", out.Len())
	}
}

func TestEmitWritesExactlyAtTheCap(t *testing.T) {
	body := []byte(strings.Repeat("x", 100))
	var out bytes.Buffer
	if err := Emit(&out, body, 100); err != nil {
		t.Fatalf("100 bytes under a 100 byte cap: %v", err)
	}
	if !bytes.Equal(out.Bytes(), body) {
		t.Errorf("wrote %d bytes, want %d", out.Len(), len(body))
	}
}

func TestSnippetCentresOnTheMatchAndMarksWhatItCut(t *testing.T) {
	text := strings.Repeat("a ", 200) + "needle" + strings.Repeat(" b", 200)
	offset := strings.Index(text, "needle")

	got := Snippet(text, offset, len("needle"), 40)
	if !strings.Contains(got, "needle") {
		t.Fatalf("snippet lost the match: %q", got)
	}
	if !strings.HasPrefix(got, "…") || !strings.HasSuffix(got, "…") {
		t.Errorf("cut text on both sides without saying so: %q", got)
	}
	if len(got) > 40+len("needle")+len("……")+4 {
		t.Errorf("snippet is %d bytes, far past the %d asked for: %q", len(got), 40, got)
	}
}

func TestSnippetCollapsesWhitespaceToOneLine(t *testing.T) {
	got := Snippet("alpha\n\n\tbeta   gamma", 8, 4, 100)
	if want := "alpha " + MarkOpen + "beta" + MarkClose + " gamma"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// A hit line that does not say which words matched makes the reader search the
// line again, which is the cost the snippet exists to remove.
func TestSnippetMarksTheWordsThatMatched(t *testing.T) {
	got := Snippet("the build number was bumped", 4, 5, 100)
	if want := "the " + MarkOpen + "build" + MarkClose + " number was bumped"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// A snippet wide enough to need cutting lands between words rather than through
// one, so long as a boundary is close by.
func TestSnippetCutsBetweenWords(t *testing.T) {
	text := "alpha bravo charlie delta needle echo foxtrot golf hotel"
	offset := strings.Index(text, "needle")
	got := Snippet(text, offset, len("needle"), 20)
	trimmed := strings.Trim(got, "…")
	if strings.HasPrefix(trimmed, " ") || strings.HasSuffix(trimmed, " ") {
		t.Errorf("snippet edge landed on whitespace: %q", got)
	}
	for _, word := range []string{"charlie", "delta", "echo"} {
		if strings.Contains(trimmed, word[:3]) && !strings.Contains(trimmed, word) {
			t.Errorf("snippet cut %q in half: %q", word, got)
		}
	}
}

// TestSnippetCutsOnRuneBoundaries keeps a window through multi-byte text from
// emitting a replacement character where it happened to land.
func TestSnippetCutsOnRuneBoundaries(t *testing.T) {
	text := strings.Repeat("é", 100) + "needle" + strings.Repeat("é", 100)
	offset := strings.Index(text, "needle")
	got := Snippet(text, offset, len("needle"), 21)
	if strings.ContainsRune(got, '�') {
		t.Errorf("snippet cut through a rune: %q", got)
	}
}

func TestSnippetSurvivesOffsetsOutsideTheText(t *testing.T) {
	if got := Snippet("short", 900, 4, 40); got != "short" {
		t.Errorf("out-of-range offset gave %q, want the whole text back and no mark", got)
	}
}

func TestJSONDoesNotEscapeHTMLInTranscriptText(t *testing.T) {
	blob, err := JSON(map[string]string{"text": "<command-args>plan</command-args>"})
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if bytes.Contains(blob, []byte(`\u003c`)) {
		t.Errorf("transcript text came back HTML-escaped, so a caller reading it gets different words: %s", blob)
	}
	if !bytes.Contains(blob, []byte(`<command-args>plan</command-args>`)) {
		t.Errorf("transcript text did not survive verbatim: %s", blob)
	}
}

func TestJSONReportsAnUnencodableValueRatherThanPanicking(t *testing.T) {
	_, err := JSON(map[string]any{"ch": make(chan int)})
	if err == nil {
		t.Fatal("a channel cannot be JSON-encoded, so this must fail rather than silently drop the field")
	}
	var fe *fperr.Error
	if !errors.As(err, &fe) || fe.Code != fperr.Internal {
		t.Errorf("code = %v, want %v", err, fperr.Internal)
	}
}

// byteSize and the coverage-line footer that calls it (WithSize) round to the
// unit a human reads fastest at that scale, not to a fixed decimal count.
func TestByteSizeScalesToTheNearestUnit(t *testing.T) {
	cases := []struct {
		n    int
		want string
	}{
		{500, "500 B"},
		{4096, "4.0 KB"},
		{5 * 1024 * 1024, "5.0 MB"},
		{2 * 1024 * 1024 * 1024, "2.0 GB"},
	}
	for _, tc := range cases {
		if got := byteSize(tc.n); got != tc.want {
			t.Errorf("byteSize(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}

// TestByteSizeCrossesTheGBBoundary is the case above the MB branch: a
// multi-pass all-tier search reports gigabytes rather than four-digit
// megabytes.
func TestByteSizeCrossesTheGBBoundary(t *testing.T) {
	if got, want := byteSize(1024*1024*1024-1), "1024.0 MB"; got != want {
		t.Errorf("byteSize(1 GB - 1) = %q, want %q", got, want)
	}
	if got, want := byteSize(1024*1024*1024), "1.0 GB"; got != want {
		t.Errorf("byteSize(1 GB) = %q, want %q", got, want)
	}
}

// TestDurationCoarsensAtEachBoundary matches the three regimes a human reads
// fastest: a decimal below a millisecond so no work does not read as no time,
// whole milliseconds through a second, and a decimal of seconds beyond it.
func TestDurationCoarsensAtEachBoundary(t *testing.T) {
	cases := []struct {
		ms   float64
		want string
	}{
		{0.4, "0.4 ms"},
		{0.999, "1.0 ms"},
		{1, "1 ms"},
		{27, "27 ms"},
		{999, "999 ms"},
		{1000, "1.0 s"},
		{1400, "1.4 s"},
	}
	for _, tc := range cases {
		if got := duration(tc.ms); got != tc.want {
			t.Errorf("duration(%v) = %q, want %q", tc.ms, got, tc.want)
		}
	}
}

func TestDayIsEmptyForAZeroTime(t *testing.T) {
	if got := Day(time.Time{}); got != "" {
		t.Errorf("Day(zero) = %q, want empty", got)
	}
}

// TestExcerptReturnsTheWholeTurnWhenItAlreadyFitsUnderWidth is the anchor case:
// nothing is cut, so nothing is marked and nothing is trimmed.
func TestExcerptReturnsTheWholeTurnWhenItAlreadyFitsUnderWidth(t *testing.T) {
	text := "short enough to keep whole"
	if got := Excerpt(text, 0, 0, 100); got != text {
		t.Errorf("Excerpt = %q, want the text unchanged", got)
	}
	if got := Excerpt(text, 0, 0, 0); got != text {
		t.Errorf("Excerpt with width<=0 = %q, want the text unchanged (no cap requested)", got)
	}
}

// TestExcerptDoesNotMarkTheMatch is the decision from the doc comment: unlike
// Snippet, a quoted passage carries no guillemets, because they would corrupt
// an identifier or a URL pasted back out of it.
func TestExcerptDoesNotMarkTheMatch(t *testing.T) {
	text := "alpha bravo charlie delta needle echo foxtrot golf hotel"
	offset := strings.Index(text, "needle")
	got := Excerpt(text, offset, len("needle"), 20)
	if strings.Contains(got, MarkOpen) || strings.Contains(got, MarkClose) {
		t.Errorf("Excerpt marked the match: %q", got)
	}
	if !strings.Contains(got, "needle") {
		t.Errorf("Excerpt lost the match entirely: %q", got)
	}
}

// TestExcerptSaysWhatItCutOnEachSide keeps the "…" markers the reader relies
// on to know a passage is partial rather than complete.
func TestExcerptSaysWhatItCutOnEachSide(t *testing.T) {
	text := strings.Repeat("a ", 200) + "needle" + strings.Repeat(" b", 200)
	offset := strings.Index(text, "needle")
	got := Excerpt(text, offset, len("needle"), 40)
	if !strings.HasPrefix(got, "…") || !strings.HasSuffix(got, "…") {
		t.Errorf("cut text on both sides without saying so: %q", got)
	}
}

// TestExcerptWithNoMatchAnchorsAtTheStart is the branch the doc comment names
// explicitly: with length 0 there is nothing to centre on, so the whole
// width budget goes to the front of the text instead of half falling off it.
func TestExcerptWithNoMatchAnchorsAtTheStart(t *testing.T) {
	text := strings.Repeat("word ", 100)
	got := Excerpt(text, 0, 0, 20)
	if strings.HasPrefix(got, "…") {
		t.Errorf("a match-less excerpt anchored at the start still claims something was cut before it: %q", got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("excerpt shorter than the text should say it was cut at the end: %q", got)
	}
}
