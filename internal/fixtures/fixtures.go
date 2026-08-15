package fixtures

import (
	"bytes"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// ScratchToken is replaced by the scratch root everywhere it appears inside the
// checked-in fixture JSONL. Absolute cwd values cannot be committed, and three
// fixtures need a cwd that resolves against real git state.
const ScratchToken = "{{SCRATCH}}"

// Corpus is a materialized copy of tests/fixtures/corpus plus the git scratch
// state its cwd values point at.
type Corpus struct {
	Root     string
	Scratch  string
	Manifest Manifest
}

// Path is the absolute location of a corpus-relative fixture file.
func (c Corpus) Path(rel string) string { return filepath.Join(c.Root, rel) }

// ScratchPath is the absolute location of a scratch-relative directory.
func (c Corpus) ScratchPath(rel string) string { return filepath.Join(c.Scratch, rel) }

// Materialize copies the shared corpus into t.TempDir, substitutes the scratch
// root into it, builds the four git shapes the cwd fixtures resolve against, and
// returns the manifest describing what was planted.
//
// It skips rather than fails when git is missing: a repo-identity test that
// passes because git never ran is worse than one that does not run.
func Materialize(t testing.TB) Corpus {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("fixtures: git is not on PATH, skipping: %v", err)
	}

	base := t.TempDir()
	scratch := filepath.Join(base, "scratch")
	root := filepath.Join(base, "projects")

	buildScratch(t, scratch)
	copyCorpus(t, sourceProjects(t), root, scratch)

	m := manifest(scratch)
	applySkew(t, filepath.Join(root, m.SkewFile), m.SkewMTime)
	return Corpus{Root: root, Scratch: scratch, Manifest: m}
}

// sourceProjects locates the checked-in corpus from this file's own path, so a
// test in any package finds it regardless of its working directory.
func sourceProjects(t testing.TB) string {
	t.Helper()
	_, self, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("fixtures: cannot locate the package source")
	}
	dir := filepath.Join(filepath.Dir(self), "..", "..", "tests", "fixtures", "corpus", "projects")
	abs, err := filepath.Abs(dir)
	if err != nil {
		t.Fatalf("fixtures: cannot resolve %s: %v", dir, err)
	}
	if _, err := os.Stat(abs); err != nil {
		t.Fatalf("fixtures: shared corpus missing at %s: %v", abs, err)
	}
	return abs
}

func copyCorpus(t testing.TB, src, dst, scratch string) {
	t.Helper()
	token := []byte(ScratchToken)
	repl := []byte(scratch)
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, bytes.ReplaceAll(data, token, repl), 0o644)
	})
	if err != nil {
		t.Fatalf("fixtures: cannot materialize the corpus: %v", err)
	}
}

func applySkew(t testing.TB, path, mtime string) {
	t.Helper()
	when, err := time.Parse(time.RFC3339, mtime)
	if err != nil {
		t.Fatalf("fixtures: bad skew mtime %q: %v", mtime, err)
	}
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatalf("fixtures: cannot set mtime on %s: %v", path, err)
	}
}

func buildScratch(t testing.TB, scratch string) {
	t.Helper()
	normal := filepath.Join(scratch, ScratchNormal)
	mkdirs(t, filepath.Join(normal, "android"))

	initRepo(t, normal)
	writeFile(t, filepath.Join(normal, "README.md"), "scratch repo with an origin\n")
	writeFile(t, filepath.Join(normal, "android", "build.gradle"), "// subdirectory of the same repo\n")
	git(t, normal, "add", "-A")
	git(t, normal, "commit", "-m", "seed")
	git(t, normal, "remote", "add", "origin", OriginURL)

	remoteless := filepath.Join(scratch, ScratchRemoteless)
	mkdirs(t, remoteless)
	initRepo(t, remoteless)
	writeFile(t, filepath.Join(remoteless, "README.md"), "scratch repo with no remote\n")
	git(t, remoteless, "add", "-A")
	git(t, remoteless, "commit", "-m", "seed")

	buildOrphanWorktree(t, normal, filepath.Join(scratch, ScratchOrphan))
}

// buildOrphanWorktree leaves a worktree whose .git file points at a gitdir that
// no longer exists. `git remote get-url origin` exits 128 inside it, which is
// what stops a naive ancestor walk; the walk must continue past that failure and
// resolve the parent repo above it.
func buildOrphanWorktree(t testing.TB, normal, orphan string) {
	t.Helper()
	git(t, normal, "worktree", "add", "-b", "orphan-branch", orphan)
	if err := os.RemoveAll(filepath.Join(normal, ".git", "worktrees", filepath.Base(orphan))); err != nil {
		t.Fatalf("fixtures: cannot prune the worktree gitdir: %v", err)
	}
	cmd := exec.Command("git", "remote", "get-url", "origin")
	cmd.Dir = orphan
	if err := cmd.Run(); err == nil {
		t.Fatalf("fixtures: %s still resolves a remote, so it does not pin the pruned-gitdir pathology", orphan)
	}
}

func initRepo(t testing.TB, dir string) {
	t.Helper()
	git(t, dir, "init", "-q", "-b", "main")
	git(t, dir, "config", "user.email", "fixtures@example.invalid")
	git(t, dir, "config", "user.name", "recall fixtures")
	git(t, dir, "config", "commit.gpgsign", "false")
}

func git(t testing.TB, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_TERMINAL_PROMPT=0",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fixtures: git %v in %s: %v\n%s", args, dir, err, out)
	}
}

func mkdirs(t testing.TB, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("fixtures: cannot create %s: %v", dir, err)
	}
}

func writeFile(t testing.TB, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("fixtures: cannot write %s: %v", path, err)
	}
}
