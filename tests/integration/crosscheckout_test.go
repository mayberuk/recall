package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mayberuk/recall/internal/corpusgen"
)

// crossCheckout is a generated session store under a temporary home, plus the
// recall binary built from this checkout. It is the portable form of acceptance
// case a1, which on the author's machine reads a 1.5 GB corpus nobody else has.
type crossCheckout struct {
	binary string
	home   string
	env    []string
	needle corpusgen.Plant
	from   string // the checkout the search is run in, which the needle is absent from
}

func setupCrossCheckout(t *testing.T) crossCheckout {
	t.Helper()
	home := t.TempDir()
	corpus, err := corpusgen.Generate(filepath.Join(home, ".claude"), corpusgen.Small())
	if err != nil {
		t.Fatalf("corpusgen.Generate: %v", err)
	}
	if want := filepath.Join(home, ".claude", "projects"); corpus.Root != want {
		t.Fatalf("corpus root is %s, want %s — the store must sit where HOME says it does", corpus.Root, want)
	}
	needle, from, ok := corpus.CrossCheckout()
	if !ok {
		t.Fatal("the generated corpus carries no cross-checkout needle")
	}

	// Every location recall reads is derived from HOME and nothing else, so a
	// leaked variable from the developer's own shell cannot point this test at
	// the real session store. Emptying one reads as unset.
	env := append(os.Environ(),
		"HOME="+home,
		"RECALL_HOME=",
		"XDG_DATA_HOME=",
		"CLAUDE_PROJECTS_DIR=",
		"CLAUDE_CODE_SESSION_ID=",
		"NO_COLOR=1",
	)
	return crossCheckout{binary: buildRecall(t), home: home, env: env, needle: needle, from: from}
}

// buildRecall compiles the CLI under test. The scope of a search is decided by
// the process's own working directory, so the property this file is about can
// only be exercised by a real process standing in a real checkout.
func buildRecall(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "recall")
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/recall")
	cmd.Dir = repoRoot(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build ./cmd/recall: %v\n%s", err, out)
	}
	return bin
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, self, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate this test's own source")
	}
	root, err := filepath.Abs(filepath.Join(filepath.Dir(self), "..", ".."))
	if err != nil {
		t.Fatalf("resolve the repo root: %v", err)
	}
	return root
}

func (cc crossCheckout) find(t *testing.T, dir string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(cc.binary, append([]string{"find"}, args...)...)
	cmd.Dir = dir
	cmd.Env = cc.env
	out, err := cmd.Output()
	code := 0
	var exit *exec.ExitError
	if err != nil {
		var ok bool
		if exit, ok = err.(*exec.ExitError); !ok {
			t.Fatalf("run recall find: %v", err)
		}
		code = exit.ExitCode()
		t.Logf("recall find %v exited %d: %s", args, code, exit.Stderr)
	}
	return string(out), code
}

// TestANeedleInAnotherCheckoutOfTheSameRepoIsReturned is the reason recall
// exists: the session store keys by checkout path, so a question asked in one
// clone of a repo used to miss everything said in another. The needle lives
// only in the second checkout and the search runs from the first.
func TestANeedleInAnotherCheckoutOfTheSameRepoIsReturned(t *testing.T) {
	cc := setupCrossCheckout(t)
	if cc.from == cc.needle.Cwd {
		t.Fatalf("the needle is in the checkout being searched from (%s), so nothing crosses", cc.from)
	}

	out, code := cc.find(t, cc.from, cc.needle.Term, "--ids")
	if code != 0 {
		t.Fatalf("recall find exited %d from %s; the needle's session was not returned", code, cc.from)
	}
	ids := strings.Fields(out)
	for _, id := range ids {
		if id == cc.needle.Session {
			return
		}
	}
	t.Errorf("recall find returned %v, want the session holding the needle, %s (recorded under %s)",
		ids, cc.needle.Session, cc.needle.Cwd)
}

// TestTheSearchDoesNotReachOutsideTheCurrentRepo is the other half: the scope
// really is the repo, so a hit returned in the test above was returned because
// the two checkouts are one repo and not because the search ignored scope.
func TestTheSearchDoesNotReachOutsideTheCurrentRepo(t *testing.T) {
	cc := setupCrossCheckout(t)
	elsewhere := filepath.Join(cc.home, ".claude", "checkouts", "repo02-1")
	if st, err := os.Stat(elsewhere); err != nil || !st.IsDir() {
		t.Fatalf("the generated corpus has no unrelated checkout at %s", elsewhere)
	}

	out, code := cc.find(t, elsewhere, cc.needle.Term, "--ids")
	if code != 1 {
		t.Errorf("searching an unrelated repo exited %d with %q, want exit 1 and no sessions",
			code, strings.TrimSpace(out))
	}
	if ids := strings.Fields(out); len(ids) != 0 {
		t.Errorf("searching an unrelated repo returned %v, want nothing", ids)
	}
}

// TestNothingOutsideTheTemporaryHomeIsRead guards the premise both tests above
// rest on. A run that fell back to the developer's real session store could
// pass this file while proving nothing a contributor could reproduce.
func TestNothingOutsideTheTemporaryHomeIsRead(t *testing.T) {
	cc := setupCrossCheckout(t)

	out, code := cc.find(t, cc.from, cc.needle.Term, "--all")
	if code != 0 {
		t.Fatalf("recall find --all exited %d", code)
	}
	if !strings.Contains(out, cc.needle.Session) {
		t.Errorf("a machine-wide search did not return %s", cc.needle.Session)
	}

	archive := filepath.Join(cc.home, ".local", "share", "recall")
	if _, err := os.Stat(archive); err != nil {
		t.Errorf("no archive under the temporary home at %s: %v — recall wrote somewhere else", archive, err)
	}
}
