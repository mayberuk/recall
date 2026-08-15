// Package fperr carries recall's failure vocabulary.
//
// A failure prints one human line followed by a final `ERROR_CODE=<slug>` line
// on stderr. The slug is the stable part that a caller parses; the human wording
// above it may change. Success is silent.
package fperr

import "fmt"

// Code is the machine-readable slug emitted as the final stderr line.
type Code string

const (
	UnknownVerb Code = "unknown_verb"
	ArgError    Code = "arg_error"

	NotFound Code = "not_found"

	// NoHits is a search that ran correctly and matched nothing. It is a result,
	// not a failure: it prints its own report on stdout and exits 1 in silence,
	// so a caller can branch on it the way it branches on grep.
	NoHits Code = "no_hits"

	// CorpusUnreadable is a session store that could not be read at all. It is
	// distinct from NotFound because the requirements dealbreaker is a silent
	// false negative: a query that searched less than it claims must say so with
	// its own slug rather than resolve to an empty result.
	CorpusUnreadable Code = "corpus_unreadable"

	// SourceVanished is a transcript deleted between stat and open by Claude
	// Code's own retention cleanup. Reported, never silently skipped.
	SourceVanished Code = "source_vanished"

	BadArchive Code = "bad_archive"

	// OutputTooLarge is the byte cap refusing to emit. It refuses rather than
	// truncates, because a truncated answer looks complete.
	OutputTooLarge Code = "output_too_large"

	AtomicWriteFailed Code = "atomic_write_failed"

	ToolMissing Code = "tool_missing"

	// Internal is the code for a failure recall did not classify. It exists so an
	// unexpected error still ends in a parseable ERROR_CODE line rather than
	// being mislabelled as one of the specific classes above.
	Internal Code = "internal_error"
)

// Error is a failure with a code. Exit is zero unless a documented exception
// needs a specific process status; main.go's table supplies the default.
type Error struct {
	Code Code
	Msg  string
	Exit int
}

func (e *Error) Error() string { return e.Msg }

// New builds a coded error, formatting msg with the fmt verbs.
func New(code Code, format string, a ...any) *Error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, a...)}
}

// WithExit builds an error whose process status overrides the code's default.
func WithExit(code Code, exit int, format string, a ...any) *Error {
	e := New(code, format, a...)
	e.Exit = exit
	return e
}
