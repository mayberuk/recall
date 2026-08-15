package repo

import (
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"github.com/mayberuk/recall/internal/fixtures"
)

// wantRemoteID is fixtures.OriginURL reduced by the rule in §Repo identity
// resolution: host without credentials or port, path without the .git suffix.
const (
	wantRemoteID   = "example.invalid/acme/normal"
	wantRemoteName = "normal"
)

func TestResolveCWDShapes(t *testing.T) {
	corpus := fixtures.Materialize(t)
	r := New()

	for _, shape := range corpus.Manifest.CWDShapes {
		t.Run(shape.Name, func(t *testing.T) {
			got := r.Resolve(shape.CWD)

			if string(got.Kind) != shape.Identity {
				t.Errorf("kind = %q, want %q", got.Kind, shape.Identity)
			}
			if got.Remote != shape.Remote {
				t.Errorf("remote = %q, want %q", got.Remote, shape.Remote)
			}
			if got.Toplevel != shape.Toplevel {
				t.Errorf("toplevel = %q, want %q", got.Toplevel, shape.Toplevel)
			}

			var wantID string
			switch shape.Identity {
			case fixtures.RepoRemote:
				wantID = wantRemoteID
			case fixtures.RepoNoRemote:
				wantID = shape.Toplevel
			case fixtures.RepoNone:
				wantID = fixtures.RepoNone
			}
			if got.ID != wantID {
				t.Errorf("id = %q, want %q", got.ID, wantID)
			}
			if got.ID != r.Repo(shape.CWD) {
				t.Errorf("Repo disagrees with Resolve for %s", shape.CWD)
			}
		})
	}
}

// TestOrphanWorktreeResolvesToParentRepo is the finding that required
// internal/repo's ancestor walk: git cannot answer inside a pruned worktree,
// and the session there belongs to the parent repo. It does not exercise the
// gitdir pointer —
// the shared fixture's orphan sits inside its parent, so the ancestor walk
// rescues it and this stays green with prunedCommonDir disabled.
// TestWorktreeOutsideParentRepo is the only guard on that path.
func TestOrphanWorktreeResolvesToParentRepo(t *testing.T) {
	corpus := fixtures.Materialize(t)
	orphan := corpus.ScratchPath(fixtures.ScratchOrphan)

	cmd := exec.Command("git", "remote", "get-url", "origin")
	cmd.Dir = orphan
	if err := cmd.Run(); err == nil {
		t.Fatalf("git resolves a remote in %s, so this test no longer covers the pruned gitdir", orphan)
	}

	got := New().Resolve(orphan)
	if got.Kind != KindRemote {
		t.Fatalf("kind = %q, want %q", got.Kind, KindRemote)
	}
	if got.Remote != fixtures.OriginURL {
		t.Errorf("remote = %q, want %q", got.Remote, fixtures.OriginURL)
	}
	if got.Toplevel != corpus.ScratchPath(fixtures.ScratchNormal) {
		t.Errorf("toplevel = %q, want the parent repo %q", got.Toplevel, corpus.ScratchPath(fixtures.ScratchNormal))
	}
}

// TestOneRepoOneIdentity is acceptance case a1 in miniature: every checkout and
// worktree of one repo must produce one identity or a session hides.
func TestOneRepoOneIdentity(t *testing.T) {
	corpus := fixtures.Materialize(t)
	r := New()

	want := r.Repo(corpus.ScratchPath(fixtures.ScratchNormal))
	if want != wantRemoteID {
		t.Fatalf("checkout identity = %q, want %q", want, wantRemoteID)
	}
	for _, rel := range []string{fixtures.ScratchAndroid, fixtures.ScratchOrphan} {
		if got := r.Repo(corpus.ScratchPath(rel)); got != want {
			t.Errorf("%s = %q, want %q", rel, got, want)
		}
	}
	if got := r.Resolve(corpus.ScratchPath(fixtures.ScratchAndroid)).Name; got != wantRemoteName {
		t.Errorf("name = %q, want %q", got, wantRemoteName)
	}
}

// TestVanishedLeafResolvesThroughLivingAncestor covers 13 of the 14 cwd values
// no longer on disk: the checkout above the deleted directory still exists.
func TestVanishedLeafResolvesThroughLivingAncestor(t *testing.T) {
	corpus := fixtures.Materialize(t)
	gone := filepath.Join(corpus.ScratchPath(fixtures.ScratchNormal), ".claude", "worktrees", "deleted", "src")
	if _, err := os.Stat(gone); err == nil {
		t.Fatalf("%s exists, so this test does not cover a vanished path", gone)
	}

	if got := New().Repo(gone); got != wantRemoteID {
		t.Errorf("id = %q, want %q", got, wantRemoteID)
	}
}

// TestVanishedCheckoutIsHonestlyUnresolved is the cost of refusing a
// path-shaped guess: nothing survives above a wholly deleted checkout, so the
// answer is "outside any repo" rather than a sibling's identity.
func TestVanishedCheckoutIsHonestlyUnresolved(t *testing.T) {
	corpus := fixtures.Materialize(t)
	gone := corpus.ScratchPath(fixtures.ScratchNormal) + "-9"

	got := New().Resolve(gone)
	if got.Kind != KindNone {
		t.Fatalf("kind = %q, want %q", got.Kind, KindNone)
	}
	if got.ID != fixtures.RepoNone {
		t.Errorf("id = %q, want %q", got.ID, fixtures.RepoNone)
	}
}

func TestRemotelessRepoIsAnIdentityNotAFailure(t *testing.T) {
	corpus := fixtures.Materialize(t)
	top := corpus.ScratchPath(fixtures.ScratchRemoteless)

	got := New().Resolve(top)
	if string(got.Kind) != fixtures.RepoNoRemote {
		t.Fatalf("kind = %q, want %q", got.Kind, fixtures.RepoNoRemote)
	}
	if got.ID != top {
		t.Errorf("id = %q, want the toplevel %q", got.ID, top)
	}
	if got.ID == fixtures.RepoNone {
		t.Error("a remoteless repo resolved to unresolved")
	}
	if got.Name != filepath.Base(top) {
		t.Errorf("name = %q, want %q", got.Name, filepath.Base(top))
	}
}

func TestEmptyCWDDoesNotWalkFromTheProcessDirectory(t *testing.T) {
	for _, cwd := range []string{"", "   ", "relative/path"} {
		if got := New().Resolve(cwd); got.Kind != KindNone {
			t.Errorf("Resolve(%q) kind = %q, want %q", cwd, got.Kind, KindNone)
		}
	}
}

// TestCacheStopsAtTheNearestRemote pins both halves of the performance
// property: one entry per directory visited, and the walk stops climbing once a
// remote resolves.
func TestCacheStopsAtTheNearestRemote(t *testing.T) {
	corpus := fixtures.Materialize(t)
	android := corpus.ScratchPath(fixtures.ScratchAndroid)
	normal := corpus.ScratchPath(fixtures.ScratchNormal)

	r := New()
	r.Resolve(android)
	if len(r.cache) != 2 {
		t.Fatalf("cache holds %d directories, want the 2 visited: %v", len(r.cache), r.cache)
	}
	for _, dir := range []string{android, normal} {
		if r.cache[dir].ID != wantRemoteID {
			t.Errorf("cache[%s] = %q, want %q", dir, r.cache[dir].ID, wantRemoteID)
		}
	}

	before := len(r.cache)
	r.Resolve(android)
	if len(r.cache) != before {
		t.Errorf("a repeat resolve grew the cache from %d to %d", before, len(r.cache))
	}
}

// TestRemotelessRepoDoesNotLeakToSiblings is the direction that hides a
// session: the ancestors walked past a remoteless repo are not part of it, so
// caching the answer onto them files every unrelated sibling under that repo.
func TestRemotelessRepoDoesNotLeakToSiblings(t *testing.T) {
	requireGit(t)
	base := t.TempDir()
	remoteless := filepath.Join(base, "remoteless")
	sibling := filepath.Join(base, "plain", "notes")
	initRepo(t, remoteless)
	if err := os.MkdirAll(sibling, 0o755); err != nil {
		t.Fatal(err)
	}

	r := New()
	// Resolving the repo first is what puts the shared parent in the cache.
	if got := r.Resolve(remoteless); string(got.Kind) != fixtures.RepoNoRemote {
		t.Fatalf("kind = %q, want %q", got.Kind, fixtures.RepoNoRemote)
	}

	got := r.Resolve(sibling)
	if got.Kind != KindNone {
		t.Errorf("sibling kind = %q, want %q", got.Kind, KindNone)
	}
	if got.ID != fixtures.RepoNone {
		t.Errorf("sibling id = %q, want %q", got.ID, fixtures.RepoNone)
	}
	if parent := r.Resolve(base); parent.Kind != KindNone {
		t.Errorf("shared parent kind = %q, want %q", parent.Kind, KindNone)
	}
}

// TestOuterRemoteBeatsNearerRemotelessRepo pins the precedence the contract
// leaves open. A vendored or scratch checkout inside a repo is not an identity
// of its own: splitting one off would scatter a repo's sessions across as many
// identities as it has nested checkouts.
func TestOuterRemoteBeatsNearerRemotelessRepo(t *testing.T) {
	requireGit(t)
	base := t.TempDir()
	outer := filepath.Join(base, "outer")
	inner := filepath.Join(outer, "vendor", "inner")

	initRepo(t, outer)
	git(t, outer, "remote", "add", "origin", fixtures.OriginURL)
	initRepo(t, inner)

	got := New().Resolve(inner)
	if got.Kind != KindRemote {
		t.Fatalf("kind = %q, want %q", got.Kind, KindRemote)
	}
	if got.ID != wantRemoteID {
		t.Errorf("id = %q, want %q", got.ID, wantRemoteID)
	}
	if got.Toplevel != outer {
		t.Errorf("toplevel = %q, want %q", got.Toplevel, outer)
	}
}

// TestConcurrentResolve exists because internal/archive injects Resolve into a
// corpus walk, so the cache mutex is load-bearing rather than decorative.
func TestConcurrentResolve(t *testing.T) {
	corpus := fixtures.Materialize(t)
	shapes := corpus.Manifest.CWDShapes

	want := make([]Identity, len(shapes))
	serial := New()
	for i, shape := range shapes {
		want[i] = serial.Resolve(shape.CWD)
	}

	const workers = 8
	shared := New()
	got := make([][]Identity, workers)
	var wg sync.WaitGroup
	for w := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got[w] = make([]Identity, len(shapes))
			for i, shape := range shapes {
				got[w][i] = shared.Resolve(shape.CWD)
			}
		}()
	}
	wg.Wait()

	for w := range workers {
		for i, shape := range shapes {
			if got[w][i] != want[i] {
				t.Errorf("worker %d resolved %s to %+v, want %+v", w, shape.Name, got[w][i], want[i])
			}
		}
	}
}

// TestWorktreeOutsideParentRepo covers the shape the shared corpus cannot: a
// worktree with no repo above it, where only the gitdir pointer connects it to
// its parent. Four of the real cwd values live under ~/dev/worktrees/.
func TestWorktreeOutsideParentRepo(t *testing.T) {
	requireGit(t)
	base := t.TempDir()
	parent := filepath.Join(base, "parent")
	live := filepath.Join(base, "elsewhere", "live")
	pruned := filepath.Join(base, "elsewhere", "pruned")

	initRepo(t, parent)
	git(t, parent, "remote", "add", "origin", fixtures.OriginURL)
	git(t, parent, "worktree", "add", "-b", "live-branch", live)
	git(t, parent, "worktree", "add", "-b", "pruned-branch", pruned)
	if err := os.RemoveAll(filepath.Join(parent, ".git", "worktrees", "pruned")); err != nil {
		t.Fatalf("cannot prune the worktree gitdir: %v", err)
	}

	r := New()
	for _, dir := range []string{live, pruned} {
		got := r.Resolve(dir)
		if got.ID != wantRemoteID {
			t.Errorf("%s id = %q, want %q", dir, got.ID, wantRemoteID)
		}
		if !sameDir(t, got.Toplevel, parent) {
			t.Errorf("%s toplevel = %q, want %q", dir, got.Toplevel, parent)
		}
	}
}

// TestPointerToNothingKeepsWalking is requirement 2 in isolation: a .git file
// naming a gitdir that is neither present nor worktree-shaped is one level's
// failure, not the answer.
func TestPointerToNothingKeepsWalking(t *testing.T) {
	requireGit(t)
	base := t.TempDir()
	outer := filepath.Join(base, "outer")
	inner := filepath.Join(outer, "inner")

	initRepo(t, outer)
	git(t, outer, "remote", "add", "origin", fixtures.OriginURL)
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inner, ".git"), []byte("gitdir: "+filepath.Join(base, "nowhere")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := New().Repo(inner); got != wantRemoteID {
		t.Errorf("id = %q, want %q", got, wantRemoteID)
	}
}

func BenchmarkResolveWarm(b *testing.B) {
	corpus := fixtures.Materialize(b)
	r := New()
	dirs := make([]string, 0, len(corpus.Manifest.CWDShapes))
	for _, shape := range corpus.Manifest.CWDShapes {
		dirs = append(dirs, shape.CWD)
		r.Resolve(shape.CWD)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.Repo(dirs[i%len(dirs)])
	}
}

// sameDir compares paths by identity, not by text: a worktree outside its
// parent is reachable only through the pointer git wrote, which names the
// symlink-resolved path.
func sameDir(t testing.TB, a, b string) bool {
	t.Helper()
	fa, err := os.Stat(a)
	if err != nil {
		return false
	}
	fb, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(fa, fb)
}

func requireGit(t testing.TB) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git is not on PATH, skipping: %v", err)
	}
}

func initRepo(t testing.TB, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "init", "-q", "-b", "main")
	git(t, dir, "config", "user.email", "repo-tests@example.invalid")
	git(t, dir, "config", "user.name", "recall repo tests")
	git(t, dir, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-m", "seed")
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
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
}
