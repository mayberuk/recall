package style_test

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/mayberuk/recall/internal/style"
)

// TestZeroPaletteIsByteIdentical holds the guarantee the JSON and JSONL paths
// rest on: a palette nobody resolved cannot introduce an escape byte.
func TestZeroPaletteIsByteIdentical(t *testing.T) {
	var p style.Palette
	const s = "6e2b8d15  2026-06-03  github.com/acme/payments"
	for name, got := range map[string]string{
		"Match":  p.Match(s),
		"Handle": p.Handle(s),
		"Key":    p.Key(s),
		"Quiet":  p.Quiet(s),
		"Status": p.Status(style.Fail, s),
	} {
		if got != s {
			t.Errorf("%s on the zero palette returned %q, want the input unchanged", name, got)
		}
	}
	if p.Enabled() {
		t.Error("the zero palette reports itself enabled")
	}
}

// TestEachRoleEmitsItsOwnAttribute pins each role to its attribute. The expected
// values are the SGR codes themselves — 7 reverse, 1 bold, 2 dim, 31/32/33 the
// conventional fail/ok/warn, 35 magenta — read off the standard rather than off
// what this package happens to produce, so the test would fail on a wrong code
// rather than agreeing with it.
func TestEachRoleEmitsItsOwnAttribute(t *testing.T) {
	p := style.Resolve(style.Always, nil)
	if !p.Enabled() {
		t.Fatal("--color always did not enable colour")
	}
	for _, c := range []struct {
		role string
		got  string
		want string
	}{
		{"Match", p.Match("x"), "\x1b[7mx\x1b[0m"},
		{"Handle", p.Handle("x"), "\x1b[35mx\x1b[0m"},
		{"Key", p.Key("x"), "\x1b[1mx\x1b[0m"},
		{"Quiet", p.Quiet("x"), "\x1b[2mx\x1b[0m"},
		{"ok", p.Status(style.OK, "x"), "\x1b[32mx\x1b[0m"},
		{"warn", p.Status(style.Warn, "x"), "\x1b[33mx\x1b[0m"},
		{"fail", p.Status(style.Fail, "x"), "\x1b[31mx\x1b[0m"},
	} {
		if c.got != c.want {
			t.Errorf("%s emitted %q, want %q", c.role, c.got, c.want)
		}
	}
}

// TestEmptyStringStaysEmpty keeps a styled empty field from becoming two
// escape sequences around nothing, which would pad every absent column.
func TestEmptyStringStaysEmpty(t *testing.T) {
	p := style.Resolve(style.Always, nil)
	if got := p.Match(""); got != "" {
		t.Errorf("styling the empty string returned %q, want it empty", got)
	}
}

// TestUnknownStatusIsNotGuessed: a Status added later must read as plain text
// rather than borrowing whichever colour happens to be first in the switch.
func TestUnknownStatusIsNotGuessed(t *testing.T) {
	p := style.Resolve(style.Always, nil)
	if got := p.Status(style.Status(99), "x"); got != "x" {
		t.Errorf("an unknown Status returned %q, want the input unchanged", got)
	}
}

func TestNoColorDisablesEvenOnATerminal(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	if style.Resolve(style.Auto, os.Stdout).Enabled() {
		t.Error("NO_COLOR was set and auto still enabled colour")
	}
}

// TestNoColorEmptyIsNotSet follows the NO_COLOR convention: presence with an
// empty value does not count, so `NO_COLOR= recall find x` is not a request for
// monochrome.
func TestNoColorEmptyIsNotSet(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "xterm-256color")
	if !style.Resolve(style.Always, nil).Enabled() {
		t.Error("an empty NO_COLOR suppressed an explicit --color always")
	}
}

// TestAlwaysBeatsTheEnvironment: --color always is the user overriding
// detection, including detection that would otherwise be right.
func TestAlwaysBeatsTheEnvironment(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	t.Setenv("TERM", "dumb")
	if !style.Resolve(style.Always, new(bytes.Buffer)).Enabled() {
		t.Error("--color always was overridden by the environment")
	}
}

func TestNeverBeatsATerminal(t *testing.T) {
	if style.Resolve(style.Never, os.Stdout).Enabled() {
		t.Error("--color never still enabled colour")
	}
}

func TestDumbTerminalGetsNoColour(t *testing.T) {
	t.Setenv("TERM", "dumb")
	os.Unsetenv("NO_COLOR")
	if style.Resolve(style.Auto, os.Stdout).Enabled() {
		t.Error("TERM=dumb still enabled colour")
	}
}

// TestNonTerminalWritersGetNoColour is the one that keeps escapes out of a
// pipe: every writer a program reads from is a buffer, a pipe or a file, and
// none of them is a character device.
func TestNonTerminalWritersGetNoColour(t *testing.T) {
	os.Unsetenv("NO_COLOR")
	t.Setenv("TERM", "xterm-256color")

	file, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()

	for name, wr := range map[string]any{
		"a buffer":       new(bytes.Buffer),
		"a regular file": file,
		"a pipe":         w,
		"nil":            nil,
	} {
		var out interface{ Write([]byte) (int, error) }
		if wr != nil {
			out = wr.(interface{ Write([]byte) (int, error) })
		}
		if style.Resolve(style.Auto, out).Enabled() {
			t.Errorf("auto enabled colour writing to %s", name)
		}
	}
}

func TestValidMode(t *testing.T) {
	for _, ok := range style.Modes {
		if !style.ValidMode(ok) {
			t.Errorf("ValidMode(%q) is false but the value is listed in Modes", ok)
		}
	}
	for _, bad := range []string{"", "yes", "Always", "no", "1"} {
		if style.ValidMode(bad) {
			t.Errorf("ValidMode(%q) is true, want false", bad)
		}
	}
}

// TestStripRemovesEveryRole is what the size footer and --max-bytes depend on:
// after Strip, a styled answer must weigh exactly what the plain one weighs.
func TestStripRemovesEveryRole(t *testing.T) {
	p := style.Resolve(style.Always, nil)
	plain := "6e2b8d15  4 hits · «idempotenc»y ×2\n── scanned 3.0 KB · 14 turns · 6 ms"
	styled := p.Handle("6e2b8d15") + "  " + p.Key("4 hits") + " · " +
		p.Match("«idempotenc») ") + p.Quiet("×2") + "\n" + p.Quiet("── scanned 3.0 KB")

	if !strings.Contains(styled, "\x1b") {
		t.Fatal("the styled fixture carries no escapes, so this test proves nothing")
	}
	if got := style.Strip(styled); strings.Contains(got, "\x1b") {
		t.Errorf("Strip left escape bytes behind: %q", got)
	}
	if got := style.Strip(plain); got != plain {
		t.Errorf("Strip altered text that had no escapes: %q", got)
	}
	// the invariant that matters: stripping restores the exact byte count
	for _, s := range []string{"idempotency", "── 3 sessions · 3 searched", "×2", ""} {
		if got := style.Strip(p.Quiet(s)); got != s {
			t.Errorf("Strip(Quiet(%q)) = %q, want the original", s, got)
		}
	}
}

// TestStripLeavesNonSGRTextAlone: an ESC that is not an SGR sequence is content
// as far as this package is concerned, and content is never dropped.
func TestStripLeavesNonSGRTextAlone(t *testing.T) {
	for _, s := range []string{"a\x1bb", "\x1b", "\x1b[", "\x1b[3", "\x1b[3Z", "plain"} {
		if got := style.Strip(s); got != s {
			t.Errorf("Strip(%q) = %q, want it unchanged", s, got)
		}
	}
}
