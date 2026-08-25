package render

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mayberuk/recall/internal/style"
)

// coloured is the sample answer rendered with every attribute on, for the tests
// that need real escape sequences rather than a hand-built fixture.
func coloured(t *testing.T) (plain, styled string) {
	t.Helper()
	f := sampleFind()
	f.Coverage = sampleCoverage()
	// the fixture's snippets carry no match markers, and a test that never
	// exercises one would pass while styleSnippet was wrong
	f.Sessions[0].Shown[0].Snippet = "…ran " + MarkOpen + "agvtool" + MarkClose + " what-version…"
	f.Sessions[0].Shown[0].Occurrences = 2
	plain = string(f.Text())
	styled = string(f.WithPalette(style.Resolve(style.Always, nil)).Text())
	if !strings.Contains(styled, "\x1b") {
		t.Fatal("the styled render carries no escapes, so these tests prove nothing")
	}
	return plain, styled
}

// TestColourIsPurelyAdditive is the guarantee everything else rests on: turning
// colour on must not change one character of what the answer says. Anything
// else and the terminal and the agent are reading different answers.
func TestColourIsPurelyAdditive(t *testing.T) {
	plain, styled := coloured(t)
	if got := style.Strip(styled); got != plain {
		t.Errorf("stripping the styled render did not reproduce the plain one\nplain:\n%s\nstripped:\n%s", plain, got)
	}
}

// TestTheDefaultRenderIsPlain: a Find nobody handed a palette must be exactly
// what this package emitted before colour existed. Every JSON path, every pipe
// and every test in this package depends on it.
func TestTheDefaultRenderIsPlain(t *testing.T) {
	f := sampleFind()
	f.Coverage = sampleCoverage()
	for name, body := range map[string][]byte{"Text": f.Text(), "Brief": f.Brief(), "IDs": f.IDs()} {
		if strings.Contains(string(body), "\x1b") {
			t.Errorf("%s emitted an escape byte with no palette set:\n%s", name, body)
		}
	}
}

// TestJSONCannotBeColoured. The palette lives in an unexported field, so this
// holds however hard a caller tries.
func TestJSONCannotBeColoured(t *testing.T) {
	f := sampleFind().WithPalette(style.Resolve(style.Always, nil))
	f.Coverage = sampleCoverage()

	body, err := JSON(f)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "\x1b") {
		t.Errorf("--json carried an escape byte:\n%s", body)
	}
	lines, err := f.JSONL()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(lines), "\x1b") {
		t.Errorf("--format jsonl carried an escape byte:\n%s", lines)
	}
	// and the JSON still parses, i.e. the extra field did not leak in as a key
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("the JSON form no longer parses: %v", err)
	}
	if _, ok := got["pal"]; ok {
		t.Error("the palette reached the JSON object")
	}
}

// TestFZFRecordsAreNeverColoured. Field 1 of an fzf record is parsed as a
// session id by every binding recall ships; an attribute there breaks them all.
func TestFZFRecordsAreNeverColoured(t *testing.T) {
	f := sampleFind().WithPalette(style.Resolve(style.Always, nil))
	f.Coverage = sampleCoverage()
	records, note := f.FZF()
	if strings.Contains(string(records)+string(note), "\x1b") {
		t.Error("an fzf surface carried an escape byte")
	}
}

// TestTheMatchMarkersBecomeTheAttribute: the guillemets exist because plain
// text has no way to say "these words matched". Where reverse video says it,
// the brackets are noise — but they must survive in the plain form, which other
// tests and the JSON contract pin.
func TestTheMatchMarkersBecomeTheAttribute(t *testing.T) {
	h := Hit{Snippet: "…ran " + MarkOpen + "agvtool" + MarkClose + " what-version…", Occurrences: 3}

	var plain strings.Builder
	h.write(&plain, style.Palette{})
	if !strings.Contains(plain.String(), MarkOpen+"agvtool"+MarkClose) {
		t.Errorf("the plain form lost its match markers: %q", plain.String())
	}

	var lit strings.Builder
	h.write(&lit, style.Resolve(style.Always, nil))
	got := lit.String()
	if !strings.Contains(got, "\x1b[7m"+MarkOpen+"agvtool"+MarkClose+"\x1b[0m") {
		t.Errorf("the match and its markers are not inverted together: %q", got)
	}
	if !strings.Contains(got, "\x1b[2m ×3\x1b[0m") {
		t.Errorf("the repeat count does not recede: %q", got)
	}
	if style.Strip(got) != plain.String() {
		t.Errorf("stripping the coloured hit did not reproduce the plain one:\n%q\n%q", style.Strip(got), plain.String())
	}
}

// TestUnbalancedMarkersAreLeftAlone. A snippet cut mid-marker must render as
// text rather than swallowing the rest of the line into an attribute.
func TestUnbalancedMarkersAreLeftAlone(t *testing.T) {
	p := style.Resolve(style.Always, nil)
	for _, s := range []string{
		"an open " + MarkOpen + " and no close",
		"a close " + MarkClose + " with no open",
		MarkOpen,
		"",
	} {
		got := styleSnippet(p, s)
		if style.Strip(got) != s {
			t.Errorf("styleSnippet(%q) does not strip back to itself: got %q", s, style.Strip(got))
		}
	}
}

// TestTheSizeFooterCountsContentNotColour. The footer answers "what does this
// answer cost to read", and an escape sequence costs a reader nothing.
func TestTheSizeFooterCountsContentNotColour(t *testing.T) {
	plain, styled := coloured(t)
	plainSized := string(WithSize([]byte(plain)))
	styledSized := string(WithSize([]byte(styled)))

	tail := func(s string) string {
		lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
		return lines[len(lines)-1]
	}
	if got, want := tail(styledSized), tail(plainSized); got != want {
		t.Errorf("the size footer changed when colour was on:\n coloured: %q\n plain:    %q", got, want)
	}
}

// TestTheCapMeasuresContentNotColour: an answer that fits must not be refused
// for the width of its own escape sequences.
func TestTheCapMeasuresContentNotColour(t *testing.T) {
	plain, styled := coloured(t)
	if len(styled) <= len(plain) {
		t.Fatal("the styled render is not longer than the plain one, so this test proves nothing")
	}
	var sink strings.Builder
	// a cap that exactly fits the content, and would refuse if escapes counted
	if err := Emit(&sink, []byte(styled), int64(len(plain))); err != nil {
		t.Errorf("a styled answer whose content fits the cap was refused: %v", err)
	}
	// and the cap must still bite on real content
	if err := Emit(&sink, []byte(styled), int64(len(plain)-1)); err == nil {
		t.Error("a styled answer whose content exceeds the cap was emitted anyway")
	}
}
