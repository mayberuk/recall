// Command recall answers what was said in any past Claude Code session on this
// machine — which session it was, what was concluded, when it was first said —
// without pulling transcript text into the asking agent's context.
//
// One query reaches every project directory, because the session store keys by
// checkout path and one logical repo spans many directories.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"text/tabwriter"

	"github.com/mayberuk/recall/internal/fperr"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || isHelp(args[0]) {
		printUsage(stdout)
		return 0
	}
	if isVersion(args[0]) {
		printVersion(stdout)
		return 0
	}
	e, ok := registry[args[0]]
	if !ok {
		return report(stderr, fperr.New(fperr.UnknownVerb,
			"unknown verb %q — %s", args[0], validVerbs()))
	}
	// Help is answered here rather than inside the verb, because stdlib flag
	// reports -h as a parse failure and the top-level help sends every caller to
	// `recall <verb> --help` — which then exited 1 with "flag: help requested".
	if wantsHelp(args[1:]) {
		printVerbUsage(stdout, args[0])
		return 0
	}
	return report(stderr, e.run(args[1:]))
}

// wantsHelp accepts the bare word `help` only where a caller could not have
// meant it as a query term — first, before anything else. A flag spelling is
// unambiguous anywhere. `recall find why we need help` is a search.
func wantsHelp(args []string) bool {
	for i, a := range args {
		if a == "--help" || a == "-h" || (i == 0 && a == "help") {
			return true
		}
	}
	return false
}

func validVerbs() string {
	names := verbNames()
	if len(names) == 0 {
		return "no verbs are registered in this build"
	}
	return "valid verbs: " + strings.Join(names, ", ")
}

// exitFor is the one place recall's process exit statuses are defined, grouped
// by what a caller does next. 1 is "the search ran and matched nothing", which
// is grep's convention and the one every caller already knows; 2 is "you asked
// wrongly"; 3 upwards are the tool's own failures.
func exitFor(c fperr.Code) int {
	switch c {
	case fperr.NoHits, fperr.NotFound:
		return 1
	case fperr.UnknownVerb, fperr.ArgError:
		return 2
	case fperr.CorpusUnreadable, fperr.SourceVanished, fperr.BadArchive:
		return 3
	case fperr.OutputTooLarge:
		return 4
	case fperr.AtomicWriteFailed:
		return 5
	case fperr.ToolMissing:
		return 6
	default:
		return 7
	}
}

// report prints a failure as one human line then a final `ERROR_CODE=<slug>`
// line, both on stderr, and returns the process status. Success is silent, and
// so is an empty result: it has already printed its own report on stdout, and a
// caller branching on exit 1 does not need a second copy on stderr.
func report(w io.Writer, err error) int {
	if err == nil {
		return 0
	}
	fe := &fperr.Error{Code: fperr.Internal, Msg: err.Error()}
	var typed *fperr.Error
	if errors.As(err, &typed) {
		fe = typed
	}
	if fe.Code == fperr.NoHits {
		return exitFor(fe.Code)
	}
	fmt.Fprintf(w, "recall: %s\n", fe.Msg)
	fmt.Fprintf(w, "ERROR_CODE=%s\n", fe.Code)
	if fe.Exit != 0 {
		return fe.Exit
	}
	return exitFor(fe.Code)
}

func isHelp(s string) bool { return s == "--help" || s == "-h" || s == "help" }

func isVersion(s string) bool { return s == "--version" || s == "version" }

var version = "dev"

func printVersion(w io.Writer) {
	fmt.Fprintf(w, "recall %s\n", version)
	if exe, err := os.Executable(); err == nil {
		fmt.Fprintf(w, "binary: %s\n", exe)
	}
	fmt.Fprintf(w, "go:     %s\n", runtime.Version())
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "recall — search past Claude Code sessions across this whole machine")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Usage: recall <verb> [flags]")
	fmt.Fprintln(w, "")
	names := verbNames()
	if len(names) == 0 {
		fmt.Fprintln(w, "  no verbs are registered in this build")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, name := range names {
		e := registry[name]
		fmt.Fprintf(tw, "  recall %s %s\t%s\n", name, e.args, e.summary)
	}
	_ = tw.Flush()
	// These four decided the one observed session where an agent gave up: it did
	// not know tool output was excluded, it did not know a long query could come
	// back empty, and it guessed a short session id would work.
	fmt.Fprint(w, `
How a query is read
  Terms are ANDed, and when no turn carries them all you get the turns carrying
  the most, with a line saying so. "quoted words" match as a phrase.
  --all-terms requires every term. --not <term> skips turns carrying it.

What is searched
  Conversation only. Tool output needs --results; tool command lines --tools.
  The current repo only, across all its checkouts. --all reaches the machine.
  Your own session and recall's own output are excluded; --include-self and
  --include-recall put them back.

Session ids
  Any unique prefix works: recall show 5fd86b00 is enough.

Exit codes
  0 hits · 1 nothing matched · 2 bad usage · 3 archive problem · 4 output refused

  recall guide            the full map of question shape to command
  recall <verb> --help    that verb's flags
`)
}

// printVerbUsage is what `recall <verb> --help` prints: the verb's own line,
// every flag it accepts with its default, and worked examples.
func printVerbUsage(w io.Writer, verb string) {
	e, ok := registry[verb]
	if !ok {
		printUsage(w)
		return
	}
	fmt.Fprintf(w, "recall %s %s — %s\n\n", verb, e.args, e.summary)
	if e.flags != nil {
		fmt.Fprintln(w, "Flags")
		e.flags.SetOutput(w)
		e.flags.PrintDefaults()
		e.flags.SetOutput(io.Discard)
	}
	if len(e.examples) > 0 {
		fmt.Fprintln(w, "\nExamples")
		for _, ex := range e.examples {
			fmt.Fprintf(w, "  %s\n", ex)
		}
	}
	fmt.Fprintln(w, "\nRun `recall guide` for which verb answers which question.")
}
