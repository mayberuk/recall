package repo

import (
	"bytes"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The real session store is read-only, always: it is the only copy of the
// corpus and Claude Code's own cleanup already destroyed 288 sessions.
const corpusRelPath = ".claude/projects"

// TestRealCorpusClassification is the check no fixture can stand in for: the
// resolver against every distinct cwd the machine has actually recorded, with
// git as an independent oracle wherever git can still answer. It runs whenever
// the store is readable, because an opt-in flag means the one assertion that
// covers acceptance case a1 never runs unless somebody remembers it.
func TestRealCorpusClassification(t *testing.T) {
	requireGit(t)

	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home directory, so there is no session store to check: %v", err)
	}
	root := filepath.Join(home, corpusRelPath)
	if _, err := os.Stat(root); err != nil {
		t.Skipf("no readable session store at %s, so the real-corpus check cannot run: %v", root, err)
	}

	cwds, relocated := scanCorpus(t, root)
	if len(cwds) == 0 {
		t.Fatalf("no cwd values found under %s", root)
	}

	r := New()
	start := time.Now()
	ids := make([]Identity, len(cwds))
	for i, cwd := range cwds {
		ids[i] = r.Resolve(cwd)
	}
	cold := time.Since(start)

	counts := map[Kind]int{}
	gone, recovered := 0, 0
	for i, cwd := range cwds {
		counts[ids[i].Kind]++
		if _, err := os.Stat(cwd); err != nil {
			gone++
			if ids[i].Kind != KindNone {
				recovered++
			}
		}
	}
	t.Logf("distinct cwd values: %d (%d resolved in %s)", len(cwds), len(cwds), cold)
	t.Logf("remote %d · repo-no-remote %d · outside-any-repo %d",
		counts[KindRemote], counts[KindNoRemote], counts[KindNone])
	t.Logf("no longer on disk: %d, of which %d resolved through a surviving ancestor", gone, recovered)
	t.Logf("relocatedCwd values: %d", len(relocated))
	for _, cwd := range relocated {
		t.Logf("  relocatedCwd %s -> %s", cwd, r.Repo(cwd))
	}
	for kind, label := range map[Kind]string{KindNoRemote: "repo, no remote", KindNone: "outside any repo"} {
		for i, cwd := range cwds {
			if ids[i].Kind == kind {
				t.Logf("  %s: %s", label, cwd)
			}
		}
	}

	byRepo := map[string]int{}
	for _, id := range ids {
		if id.Kind == KindRemote {
			byRepo[id.ID]++
		}
	}
	repos := make([]string, 0, len(byRepo))
	for id := range byRepo {
		repos = append(repos, id)
	}
	sort.Strings(repos)
	for _, id := range repos {
		t.Logf("  %-46s %d cwd values", id, byRepo[id])
	}

	checkAgainstGit(t, cwds, ids)
	checkMultiCheckoutRepoIsOneIdentity(t, ids)
	reportWarmCost(t, r, cwds)
}

// checkAgainstGit is the wrong-repo guard. Where git resolves a remote at all,
// the resolver must have found the same one.
func checkAgainstGit(t *testing.T, cwds []string, ids []Identity) {
	t.Helper()
	agreed, beyondGit := 0, 0
	for i, cwd := range cwds {
		if want, ok := gitOrigin(cwd); ok {
			if ids[i].Remote != want {
				t.Errorf("%s resolved remote %q, git says %q", cwd, ids[i].Remote, want)
			}
			agreed++
			continue
		}
		beyondGit++
		checkBeyondGit(t, cwd, ids[i])
	}
	t.Logf("git agrees on %d cwd values; %d are beyond what git resolves", agreed, beyondGit)
}

// checkBeyondGit covers the cwd values internal/repo exists for — a pruned
// worktree, a deleted directory, a repo with no remote. git cannot answer at
// the cwd, but it can at the root the resolver named, so the identity is
// re-derived there and then checked to be one that cwd may legitimately claim.
func checkBeyondGit(t *testing.T, cwd string, id Identity) {
	t.Helper()
	switch id.Kind {
	case KindNone:
		if id.Toplevel != "" || id.Remote != "" {
			t.Errorf("%s is unresolved but carries toplevel %q and remote %q", cwd, id.Toplevel, id.Remote)
		}
		return
	case KindRemote:
		want, ok := gitOrigin(id.Toplevel)
		if !ok {
			t.Errorf("%s resolved to %s, where git finds no remote at all", cwd, id.Toplevel)
		} else if want != id.Remote {
			t.Errorf("%s resolved remote %q, git reads %q at %s", cwd, id.Remote, want, id.Toplevel)
		}
	case KindNoRemote:
		if !isRepoRoot(id.Toplevel) {
			t.Errorf("%s resolved to %s, which git does not consider a repo", cwd, id.Toplevel)
		}
		if want, ok := gitOrigin(id.Toplevel); ok {
			t.Errorf("%s resolved as remoteless, but git reads remote %q at %s", cwd, want, id.Toplevel)
		}
		if id.ID != id.Toplevel {
			t.Errorf("%s has id %q, want the toplevel %q", cwd, id.ID, id.Toplevel)
		}
	}
	if !claimable(cwd, id.Toplevel) {
		t.Errorf("%s cannot claim %s: neither an ancestor of it nor the parent its worktree pointer names", cwd, id.Toplevel)
	}
}

// claimable restates the two ways a cwd may belong to a repo root it does not
// sit under: ancestry, or a worktree pointer naming a gitdir inside that root.
// A worktree checked out beside its parent rather than within it is the second
// case, and four real cwd values live under ~/dev/worktrees.
func claimable(cwd, top string) bool {
	if top == cwd || strings.HasPrefix(cwd, top+string(filepath.Separator)) {
		return true
	}
	for dir := cwd; ; {
		if fi, err := os.Stat(filepath.Join(dir, ".git")); err == nil && !fi.IsDir() {
			body, err := os.ReadFile(filepath.Join(dir, ".git"))
			if err != nil {
				return false
			}
			rest, _, _ := strings.Cut(strings.TrimPrefix(strings.TrimSpace(string(body)), "gitdir:"), "\n")
			return strings.HasPrefix(strings.TrimSpace(rest), top+string(filepath.Separator))
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
}

func gitOrigin(dir string) (string, bool) {
	cmd := exec.Command("git", "config", "--get", "remote.origin.url")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	return strings.TrimRight(string(out), "\n"), true
}

func isRepoRoot(dir string) bool {
	cmd := exec.Command("git", "rev-parse", "--git-dir")
	cmd.Dir = dir
	return cmd.Run() == nil
}

// checkMultiCheckoutRepoIsOneIdentity discovers a repo cloned into more than
// one sibling checkout directory (the "name", "name-2", "name-3" pattern the
// resolver exists to fold together) and checks every cwd under those
// checkouts resolves to one identity, using git's own remote config as the
// independent oracle that the siblings are the same repo. Skips if this
// machine's corpus holds no such repo.
func checkMultiCheckoutRepoIsOneIdentity(t *testing.T, ids []Identity) {
	t.Helper()

	toplevelsBySeries := map[string]map[string]bool{}
	idByToplevel := map[string]string{}
	for _, id := range ids {
		if id.Kind != KindRemote || id.Toplevel == "" {
			continue
		}
		idByToplevel[id.Toplevel] = id.ID
		series := filepath.Join(filepath.Dir(id.Toplevel), seriesName(filepath.Base(id.Toplevel)))
		if toplevelsBySeries[series] == nil {
			toplevelsBySeries[series] = map[string]bool{}
		}
		toplevelsBySeries[series][id.Toplevel] = true
	}

	for series, toplevels := range toplevelsBySeries {
		if len(toplevels) < 2 {
			continue
		}
		remotes := map[string]bool{}
		idsFound := map[string]bool{}
		for top := range toplevels {
			if remote, ok := gitOrigin(top); ok {
				remotes[remote] = true
			}
			idsFound[idByToplevel[top]] = true
		}
		if len(remotes) != 1 {
			continue // sibling directories, but git says different repos
		}
		if len(idsFound) != 1 {
			for id := range idsFound {
				t.Errorf("checkouts under %s resolve to more than one identity, e.g. %q", series, id)
			}
			return
		}
		for id := range idsFound {
			t.Logf("%d checkouts under %s resolve to one identity: %s", len(toplevels), series, id)
		}
		return
	}
	t.Skip("no repo with more than one sibling checkout directory found in this corpus, so the multi-checkout fold cannot be demonstrated here")
}

// seriesName strips a trailing "-<N>" checkout suffix so sibling clones of
// the same repo (e.g. "app", "app-2", "app-3") group under one series key.
func seriesName(base string) string {
	if i := strings.LastIndexByte(base, '-'); i > 0 {
		if _, err := strconv.Atoi(base[i+1:]); err == nil {
			return base[:i]
		}
	}
	return base
}

func reportWarmCost(t *testing.T, r *Resolver, cwds []string) {
	t.Helper()
	const records = 300000
	start := time.Now()
	for i := 0; i < records; i++ {
		r.Repo(cwds[i%len(cwds)])
	}
	elapsed := time.Since(start)
	t.Logf("%d cached lookups in %s (%s each)", records, elapsed, elapsed/records)
}

func scanCorpus(t *testing.T, root string) (cwds, relocated []string) {
	t.Helper()
	seen := map[string]bool{}
	seenRelocated := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()
		harvest(f, seen, seenRelocated)
		return nil
	})
	if err != nil {
		t.Fatalf("cannot walk %s: %v", root, err)
	}
	return sortedKeys(seen), sortedKeys(seenRelocated)
}

func harvest(rd io.Reader, cwds, relocated map[string]bool) {
	const (
		chunk = 1 << 20
		carry = 1 << 10
	)
	buf := make([]byte, 0, chunk+carry)
	tmp := make([]byte, chunk)
	for {
		n, err := rd.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
			collect(buf, []byte(`"cwd":"`), cwds)
			collect(buf, []byte(`"relocatedCwd":"`), relocated)
			if len(buf) > carry {
				buf = append(buf[:0], buf[len(buf)-carry:]...)
			}
		}
		if err != nil {
			return
		}
	}
}

func collect(buf, token []byte, into map[string]bool) {
	for at := 0; ; {
		i := bytes.Index(buf[at:], token)
		if i < 0 {
			return
		}
		start := at + i + len(token)
		end := start
		for end < len(buf) && buf[end] != '"' {
			if buf[end] == '\\' {
				end++
			}
			end++
		}
		if end >= len(buf) {
			return
		}
		var value string
		if json.Unmarshal(buf[start-1:end+1], &value) == nil && value != "" {
			into[value] = true
		}
		at = end
	}
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
