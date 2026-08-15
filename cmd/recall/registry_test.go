package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/mayberuk/recall/internal/fperr"
)

// withRegistry swaps in a registry for one test. The real one is populated by
// each cmd_<verb>.go's init(), which no test may depend on the contents of.
func withRegistry(t *testing.T, entries map[string]entry) {
	t.Helper()
	saved := registry
	registry = entries
	t.Cleanup(func() { registry = saved })
}

func mustPanic(t *testing.T, want string, fn func()) {
	t.Helper()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("no panic; want one mentioning %q", want)
		}
		if msg, ok := r.(string); !ok || !strings.Contains(msg, want) {
			t.Fatalf("panic = %v, want one mentioning %q", r, want)
		}
	}()
	fn()
}

func TestRegisterAndDispatch(t *testing.T) {
	withRegistry(t, map[string]entry{})

	var gotArgs []string
	Register("find", func(args []string) error {
		gotArgs = args
		return nil
	})

	var out, errOut bytes.Buffer
	if code := run([]string{"find", "--all", "wallet"}, &out, &errOut); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr: %s", code, errOut.String())
	}
	want := []string{"--all", "wallet"}
	if len(gotArgs) != len(want) || gotArgs[0] != want[0] || gotArgs[1] != want[1] {
		t.Errorf("verb received %v, want %v", gotArgs, want)
	}
}

// A second registration of the same verb would let one silently shadow the
// other, which is exactly what the no-shared-file scheme must not permit.
func TestDoubleRegistrationPanics(t *testing.T) {
	withRegistry(t, map[string]entry{})
	Register("find", func([]string) error { return nil })
	mustPanic(t, "duplicate verb registration: find", func() {
		Register("find", func([]string) error { return nil })
	})
}

func TestRegisterRejectsEmptyAndNil(t *testing.T) {
	withRegistry(t, map[string]entry{})
	mustPanic(t, "no name", func() { Register("", func([]string) error { return nil }) })
	mustPanic(t, "no run function", func() { Register("find", nil) })
}

func TestDescribeUnregisteredPanics(t *testing.T) {
	withRegistry(t, map[string]entry{})
	mustPanic(t, "unregistered verb: when", func() { Describe("when", "<query>", "place a topic in time", newFlags("when")) })
}

func TestUnknownVerbIsReported(t *testing.T) {
	withRegistry(t, map[string]entry{})
	Register("find", func([]string) error { return nil })
	Register("show", func([]string) error { return nil })

	var out, errOut bytes.Buffer
	code := run([]string{"fnid"}, &out, &errOut)

	if want := exitFor(fperr.UnknownVerb); code != want {
		t.Errorf("exit = %d, want %d", code, want)
	}
	stderr := errOut.String()
	if !strings.Contains(stderr, `unknown verb "fnid"`) {
		t.Errorf("stderr does not name the verb: %q", stderr)
	}
	if !strings.Contains(stderr, "valid verbs: find, show") {
		t.Errorf("stderr does not list the registered verbs: %q", stderr)
	}
	if !strings.HasSuffix(stderr, "ERROR_CODE=unknown_verb\n") {
		t.Errorf("stderr must end in the parseable code line, got %q", stderr)
	}
	if out.Len() != 0 {
		t.Errorf("a failure wrote to stdout: %q", out.String())
	}
}

func TestNoVerbListsRegisteredVerbs(t *testing.T) {
	withRegistry(t, map[string]entry{})
	Register("when", func([]string) error { return nil })
	Describe("when", "<query>", "place a topic in time", newFlags("when"))
	Register("find", func([]string) error { return nil })
	Describe("find", "<query>", "locate a session", newFlags("find"))

	var out, errOut bytes.Buffer
	if code := run(nil, &out, &errOut); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	got := out.String()
	for _, want := range []string{"recall find", "locate a session", "recall when", "place a topic in time"} {
		if !strings.Contains(got, want) {
			t.Errorf("usage is missing %q:\n%s", want, got)
		}
	}
	if strings.Index(got, "recall find") > strings.Index(got, "recall when") {
		t.Error("verbs must list in a stable order, and sorted is the only stable one for a map")
	}
}

// The scaffold ships before any verb does, so an empty registry must still run.
func TestUsageWithNoVerbsRegistered(t *testing.T) {
	withRegistry(t, map[string]entry{})
	var out, errOut bytes.Buffer
	if code := run(nil, &out, &errOut); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "no verbs are registered") {
		t.Errorf("usage = %q", out.String())
	}
}

func TestVerbErrorMapsToExitCode(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"arg error", fperr.New(fperr.ArgError, "bad flag"), 2},
		{"not found", fperr.New(fperr.NotFound, "no such session"), 1},
		{"corpus unreadable", fperr.New(fperr.CorpusUnreadable, "cannot read store"), 3},
		{"source vanished", fperr.New(fperr.SourceVanished, "deleted mid-scan"), 3},
		{"output too large", fperr.New(fperr.OutputTooLarge, "refusing 3 MB"), 4},
		{"write failed", fperr.New(fperr.AtomicWriteFailed, "cannot rename"), 5},
		{"tool missing", fperr.New(fperr.ToolMissing, "git not found"), 6},
		{"unclassified", errors.New("something else"), 7},
		{"explicit exit wins", fperr.WithExit(fperr.BadArchive, 42, "checksum"), 42},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			withRegistry(t, map[string]entry{})
			Register("find", func([]string) error { return tc.err })
			var out, errOut bytes.Buffer
			if code := run([]string{"find"}, &out, &errOut); code != tc.want {
				t.Errorf("exit = %d, want %d", code, tc.want)
			}
			if !strings.Contains(errOut.String(), "ERROR_CODE=") {
				t.Errorf("no ERROR_CODE line: %q", errOut.String())
			}
		})
	}
}

func TestHelpAndVersionExitZero(t *testing.T) {
	withRegistry(t, map[string]entry{})
	for _, arg := range []string{"--help", "-h", "help"} {
		var out, errOut bytes.Buffer
		if code := run([]string{arg}, &out, &errOut); code != 0 {
			t.Errorf("%s: exit = %d, want 0", arg, code)
		}
		if !strings.Contains(out.String(), "Usage: recall <verb>") {
			t.Errorf("%s printed no usage", arg)
		}
	}
	for _, arg := range []string{"--version", "version"} {
		var out, errOut bytes.Buffer
		if code := run([]string{arg}, &out, &errOut); code != 0 {
			t.Errorf("%s: exit = %d, want 0", arg, code)
		}
		if !strings.Contains(out.String(), "recall dev") {
			t.Errorf("%s printed %q", arg, out.String())
		}
	}
}

// TestUnknownVerbWithNoneRegisteredNamesNoAlternative is validVerbs' other
// branch: an unknown verb against an empty registry has no valid list to
// offer, so the message says so instead of printing an empty one.
func TestUnknownVerbWithNoneRegisteredNamesNoAlternative(t *testing.T) {
	withRegistry(t, map[string]entry{})
	var out, errOut bytes.Buffer
	run([]string{"fnid"}, &out, &errOut)
	if want := "no verbs are registered in this build"; !strings.Contains(errOut.String(), want) {
		t.Errorf("stderr = %q, want it to contain %q", errOut.String(), want)
	}
}

// TestVerbUsageForAnUnknownVerbFallsBackToTheTopLevelUsage covers
// printVerbUsage's own guard: `recall <verb> --help` for a verb that never
// registered has no flags or examples to print, so it falls back to the
// same usage a bare `recall` prints rather than panicking on a missing entry.
func TestVerbUsageForAnUnknownVerbFallsBackToTheTopLevelUsage(t *testing.T) {
	withRegistry(t, map[string]entry{})
	var out bytes.Buffer
	printVerbUsage(&out, "not-a-registered-verb")
	if want := "Usage: recall <verb>"; !strings.Contains(out.String(), want) {
		t.Errorf("output is missing the top-level usage fallback %q:\n%s", want, out.String())
	}
}

func TestSuccessIsSilentOnStderr(t *testing.T) {
	withRegistry(t, map[string]entry{})
	Register("doctor", func([]string) error { return nil })
	var out, errOut bytes.Buffer
	if code := run([]string{"doctor"}, &out, &errOut); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if errOut.Len() != 0 {
		t.Errorf("success wrote to stderr: %q", errOut.String())
	}
}
