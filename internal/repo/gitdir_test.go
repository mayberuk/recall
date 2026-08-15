package repo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCommonDirFailsWhenThePointerNamesNoGitdirLine covers the fallthrough
// commonDir takes when readPointer cannot make sense of a .git file: neither a
// worktree's commondir file nor a fallback candidate exists, so the walk must
// keep climbing rather than invent a repo from a shape that isn't there.
func TestCommonDirFailsWhenThePointerNamesNoGitdirLine(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".git"), []byte("not a pointer\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := commonDir(dir); ok {
		t.Error("commonDir succeeded on a .git file with no gitdir: line")
	}
}

// TestCommonDirRecognizesAGitdirWithNoCommondirFile is the submodule shape: the
// gitdir a .git file points at is its own repo directory, not a linked
// worktree, so it carries no commondir file and is itself the common dir.
func TestCommonDirRecognizesAGitdirWithNoCommondirFile(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "app")
	gitdir := filepath.Join(base, "super", ".git", "modules", "app")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(gitdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: "+gitdir+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, ok := commonDir(dir)
	if !ok {
		t.Fatal("commonDir failed on a submodule-shaped gitdir")
	}
	if got != gitdir {
		t.Errorf("commonDir = %q, want the gitdir itself %q", got, gitdir)
	}
	// A submodule's gitdir does not end in ".git", so its toplevel is itself
	// rather than its parent — the branch a normal repo's ".git" never takes.
	if top := toplevelOf(got); top != gitdir {
		t.Errorf("toplevelOf(%q) = %q, want the gitdir unchanged", got, top)
	}
}

func TestToplevelOfARegularDotGitDirIsItsParent(t *testing.T) {
	common := filepath.Join(string(filepath.Separator), "repo", ".git")
	want := filepath.Join(string(filepath.Separator), "repo")
	if got := toplevelOf(common); got != want {
		t.Errorf("toplevelOf(%q) = %q, want %q", common, got, want)
	}
}

func TestPrunedCommonDirFailsWithoutAConfigFile(t *testing.T) {
	base := t.TempDir()
	gitdir := filepath.Join(base, "repo", ".git", "worktrees", "id")
	if err := os.MkdirAll(gitdir, 0o755); err != nil {
		t.Fatal(err)
	}
	// No config file was created beside the candidate common dir.
	if _, ok := prunedCommonDir(gitdir); ok {
		t.Error("prunedCommonDir succeeded without a config file at the candidate")
	}
}

func TestPrunedCommonDirFailsWhenConfigIsADirectory(t *testing.T) {
	base := t.TempDir()
	gitdir := filepath.Join(base, "repo", ".git", "worktrees", "id")
	if err := os.MkdirAll(gitdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(base, "repo", ".git", "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, ok := prunedCommonDir(gitdir); ok {
		t.Error("prunedCommonDir accepted a config path that is a directory")
	}
}

// TestCallerFrameFallsBackToTopWhenItNoLongerExists covers the one failure
// callerFrame can hit on its own: the repo's toplevel has vanished, so there is
// no SameFile identity left to search for and the raw toplevel is the honest
// answer.
func TestCallerFrameFallsBackToTopWhenItNoLongerExists(t *testing.T) {
	top := filepath.Join(t.TempDir(), "gone")
	dir := filepath.Join(t.TempDir(), "elsewhere", "cwd")
	if got := callerFrame(dir, top); got != top {
		t.Errorf("callerFrame = %q, want the toplevel %q unchanged", got, top)
	}
}

func TestReadPointerRejectsAnOversizedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".git")
	oversized := strings.Repeat("a", pointerLimit+1)
	if err := os.WriteFile(path, []byte(oversized), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := readPointer(path, filepath.Dir(path)); ok {
		t.Error("readPointer accepted a file larger than the pointer limit")
	}
}

func TestReadPointerSkipsBlankLinesBeforeTheGitdirLine(t *testing.T) {
	base := t.TempDir()
	path := filepath.Join(base, ".git")
	if err := os.WriteFile(path, []byte("\n   \ngitdir: ../actual\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, ok := readPointer(path, base)
	if !ok {
		t.Fatal("readPointer rejected a pointer with leading blank lines")
	}
	want := resolveAgainst(base, "../actual")
	if got != want {
		t.Errorf("readPointer = %q, want %q", got, want)
	}
}

func TestReadPointerRejectsAnEmptyGitdirTarget(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".git")
	if err := os.WriteFile(path, []byte("gitdir: \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := readPointer(path, filepath.Dir(path)); ok {
		t.Error("readPointer accepted a gitdir: line with no target")
	}
}

func TestReadLineRejectsBlankContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "commondir")
	if err := os.WriteFile(path, []byte("   \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := readLine(path); ok {
		t.Error("readLine accepted a file with only whitespace")
	}
}
