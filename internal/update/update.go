// Package update knows whether a newer recall has been published, and can
// replace the running binary with it.
//
// It is the only part of recall that opens a socket, and it does so only from
// the two verbs a user typed for that purpose: `recall update` and `recall
// doctor`. Every other verb reads the cached answer from disk and nothing
// else. That is deliberate rather than incidental. recall promises no daemon
// and no background process, and a search that quietly resolved a hostname
// would break both, as well as the start-to-exit gate the acceptance suite
// holds this binary to.
package update

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mayberuk/recall/internal/style"
)

// SilenceEnv disables the check and the notice alike. Set and non-empty, which
// is the NO_COLOR convention internal/style already follows: an empty value is
// a variable someone unset, not a preference.
const SilenceEnv = "RECALL_NO_UPDATE_CHECK"

// stateFile lives beside the archive. It records what the last deliberate
// check learned, so a search can answer "is there a newer one" without asking
// anybody.
const stateFile = "update.json"

// quiet is how long a notice waits before repeating. A daily reminder is a
// reminder; one per search is a nag nobody reads.
const quiet = 24 * time.Hour

// Silenced reports whether the user has turned the whole mechanism off.
func Silenced() bool {
	v, ok := os.LookupEnv(SilenceEnv)
	return ok && v != ""
}

// State is the cached result of the last check. Its zero value means nothing
// has been checked yet, which is the correct starting point: recall says
// nothing about updates until a verb that is allowed to ask has asked.
type State struct {
	Latest    string    `json:"latest"`
	CheckedAt time.Time `json:"checked_at"`
	// No omitempty: encoding/json cannot omit a zero time.Time, because a
	// struct is never empty to it. The tag would read as a promise the
	// encoder does not keep.
	NotifiedAt time.Time `json:"notified_at"`
}

// Load reads the cached state. Every failure yields the zero value rather than
// an error: a missing, truncated or hand-edited cache must never turn a search
// into a failure, because this file is not part of any answer.
func Load(dir string) State {
	b, err := os.ReadFile(filepath.Join(dir, stateFile))
	if err != nil {
		return State{}
	}
	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		return State{}
	}
	return s
}

// Save writes the cache, creating the directory if the archive has not been
// built yet.
func Save(dir string, s State) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, stateFile), b, 0o644)
}

// Notice is the one line to print, or "" when there is nothing to say. It is
// separate from Nag so the decision can be tested without a writer, a clock or
// a filesystem.
func Notice(current string, s State) string {
	if !Newer(s.Latest, current) {
		return ""
	}
	return fmt.Sprintf("recall %s is available (you have %s). Run `recall update`.",
		strings.TrimPrefix(s.Latest, "v"), strings.TrimPrefix(current, "v"))
}

// Nag writes the notice to w when there is one, w is a terminal, and one has
// not gone out recently. It returns whether it wrote, which is what the tests
// assert on.
//
// w is always stderr. The notice must never touch stdout: an answer's bytes
// are counted, capped and compared against a baseline, and a line of chatter
// in the middle of them would corrupt all three.
func Nag(w io.Writer, current, dir string, now time.Time) bool {
	if Silenced() || !style.IsTerminal(w) {
		return false
	}
	s := Load(dir)
	if now.Sub(s.NotifiedAt) < quiet {
		return false
	}
	line := Notice(current, s)
	if line == "" {
		return false
	}
	fmt.Fprintln(w, line)
	s.NotifiedAt = now
	// A cache that cannot be written means the notice repeats next time, which
	// is a smaller harm than failing a search over a reminder.
	_ = Save(dir, s)
	return true
}

// IsRelease reports whether v is a version this tool could have been installed
// as. A build with no ldflags reports "dev", and comparing that against a tag
// is meaningless in both directions.
func IsRelease(v string) bool {
	_, ok := parse(v)
	return ok
}

// Newer reports whether tag is a later release than current.
//
// A build with no version stamped (`dev`, the default for `go build` without
// ldflags) is never behind: someone running a local build did not get it from
// a release and should not be told to replace it with one.
func Newer(tag, current string) bool {
	a, ok := parse(tag)
	if !ok {
		return false
	}
	b, ok := parse(current)
	if !ok {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return false
}

// parse reads a `v1.2.3` tag into its three numbers. Anything else, including
// a pre-release suffix this project has never published, is not comparable and
// is reported as such rather than guessed at.
func parse(v string) ([3]int, bool) {
	var out [3]int
	parts := strings.Split(strings.TrimPrefix(strings.TrimSpace(v), "v"), ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}
