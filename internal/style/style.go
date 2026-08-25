// Package style carries recall's terminal colour: six roles and no palette of
// its own. Every role resolves to a named ANSI attribute — reverse video, bold,
// dim, magenta, green, yellow, red — which the terminal maps through whatever
// theme its owner already chose. recall picks the *role*; the user's terminal
// picks the colour. A tool that ships its own hexes fights every theme it lands
// in and loses.
//
// That is also why there is no truecolor ladder here. A ladder exists to
// degrade a specific colour down to the nearest one a terminal can show; not
// one role below names a specific colour, so there is nothing to degrade and
// the only two states are on and off.
//
// The zero Palette writes nothing. Plain output is byte-identical to output
// from before this package existed, which is what --json and --format jsonl
// rest on: those paths hold the zero value and cannot acquire an escape byte
// by accident.
package style

import (
	"io"
	"os"
	"strings"
)

// The attribute set, in full. Nothing here names a colour by number beyond the
// eight the terminal owns, and Status deliberately keeps the conventional
// green/yellow/red: a tool that rebrands "this failed" into its own accent
// makes the reader learn a private language to find out something went wrong.
const (
	reset    = "\x1b[0m"
	seqMatch = "\x1b[7m"  // reverse video — the terminal's own two colours, swapped
	seqHands = "\x1b[35m" // magenta
	seqKey   = "\x1b[1m"  // bold
	seqQuiet = "\x1b[2m"  // dim
	seqOK    = "\x1b[32m"
	seqWarn  = "\x1b[33m"
	seqFail  = "\x1b[31m"
)

// Mode is the resolved answer to "should this run emit colour", before the
// environment gets a vote. Auto lets the environment decide; the other two are
// the user overriding it.
type Mode string

const (
	Auto   Mode = "auto"
	Always Mode = "always"
	Never  Mode = "never"
)

// Modes lists the accepted --color values, for the flag's help and its error.
var Modes = []string{string(Auto), string(Always), string(Never)}

// ValidMode reports whether s is one of Modes.
func ValidMode(s string) bool {
	for _, m := range Modes {
		if s == m {
			return true
		}
	}
	return false
}

// Palette applies the six roles. Its zero value emits nothing, so a caller that
// never resolves one still renders correct plain text.
type Palette struct{ on bool }

// Enabled reports whether this palette writes escape sequences.
func (p Palette) Enabled() bool { return p.on }

// Resolve decides whether w gets colour. Always and Never are taken at their
// word. Auto asks, in order: NO_COLOR set to anything non-empty, TERM=dumb, and
// whether w is a character device — a pipe, a file, or a capture buffer all
// answer no, which is what keeps escapes out of anything a program will parse.
func Resolve(mode Mode, w io.Writer) Palette {
	switch mode {
	case Never:
		return Palette{}
	case Always:
		return Palette{on: true}
	}
	if v, ok := os.LookupEnv("NO_COLOR"); ok && v != "" {
		return Palette{}
	}
	if os.Getenv("TERM") == "dumb" {
		return Palette{}
	}
	return Palette{on: IsTerminal(w)}
}

// IsTerminal reports whether w is a character device. Deriving it from the file
// mode keeps this dependency-free: the two direct modules recall allows both
// earned their place on measurement, and a third for one syscall would not.
//
// Exported because the update notice needs the same question answered about
// stderr, and two spellings of "is this a terminal" would eventually disagree.
func IsTerminal(w io.Writer) bool {
	f, ok := w.(interface{ Stat() (os.FileInfo, error) })
	if !ok {
		return false
	}
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func (p Palette) wrap(seq, s string) string {
	if !p.on || s == "" {
		return s
	}
	return seq + s + reset
}

// Match marks a search hit. Reverse video rather than a colour, because the hit
// is the one thing on screen that has to be found by eye in a wall of text, and
// swapping foreground for background is the only marker that survives every
// theme including the ones whose background is already the accent colour.
func (p Palette) Match(s string) string { return p.wrap(seqMatch, s) }

// Handle marks something the reader will copy or retype: a session id, a
// suggested `run:` line. Magenta is the one discretionary colour in the system.
func (p Palette) Handle(s string) string { return p.wrap(seqHands, s) }

// Key marks a field the eye scans down a column. Weight, not hue — colour is
// already carrying three other jobs and a fourth would make none of them read.
func (p Palette) Key(s string) string { return p.wrap(seqKey, s) }

// Quiet recedes: the coverage footer, an elision, a ×N repeat count. Present
// for the reader who wants it, out of the way of the reader who does not.
func (p Palette) Quiet(s string) string { return p.wrap(seqQuiet, s) }

// Status is ok, warn and fail in the conventional green, yellow and red.
type Status int

const (
	OK Status = iota
	Warn
	Fail
)

// Status colours a verdict. An unknown Status is returned unstyled rather than
// guessing, so a new one added later reads as plain text instead of the wrong
// colour.
func (p Palette) Status(k Status, s string) string {
	switch k {
	case OK:
		return p.wrap(seqOK, s)
	case Warn:
		return p.wrap(seqWarn, s)
	case Fail:
		return p.wrap(seqFail, s)
	}
	return s
}

// Strip removes every escape sequence this package can emit. It exists so a
// caller can measure what an answer really costs: the size footer and
// --max-bytes both count content, and a byte spent turning a word magenta is
// not content the reader or the agent has to take in.
func Strip(s string) string {
	if !strings.Contains(s, "\x1b") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && (s[j] == ';' || (s[j] >= '0' && s[j] <= '9')) {
				j++
			}
			if j < len(s) && s[j] == 'm' {
				i = j + 1
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}
