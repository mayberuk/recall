package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Repo identity computed from git directly, per docs/design.md's repo-identity decision.
// The harness derives the expected identity itself rather than reading it back from recall, which
// is the difference between an assertion and a tautology.

type repoIdentity struct {
	Kind  string `json:"kind"` // remote | no-remote | outside
	Name  string `json:"name"`
	Value string `json:"value"` // remote URL, or toplevel path for a remoteless repo
	Via   string `json:"via"`   // ancestor directory the identity resolved at
}

type repoResolver struct {
	cache map[string]repoIdentity
}

func newRepoResolver() *repoResolver { return &repoResolver{cache: map[string]repoIdentity{}} }

func (r *repoResolver) resolve(cwd string) repoIdentity {
	if id, ok := r.cache[cwd]; ok {
		return id
	}
	id := r.walk(cwd)
	r.cache[cwd] = id
	return id
}

// walk continues past any failure, not only a missing directory: an orphaned worktree has a .git
// file pointing at a pruned gitdir and every git command there exits 128.
func (r *repoResolver) walk(cwd string) repoIdentity {
	d := filepath.Clean(cwd)
	for {
		if st, err := os.Stat(d); err == nil && st.IsDir() {
			if url, err := gitOut(d, "remote", "get-url", "origin"); err == nil && url != "" {
				return repoIdentity{Kind: "remote", Name: remoteName(url), Value: url, Via: d}
			}
			if top, err := gitOut(d, "rev-parse", "--show-toplevel"); err == nil && top != "" {
				return repoIdentity{Kind: "no-remote", Name: filepath.Base(top), Value: top, Via: d}
			}
		}
		parent := filepath.Dir(d)
		if parent == d || parent == "/" || parent == "." {
			return repoIdentity{Kind: "outside", Name: "", Value: "", Via: ""}
		}
		d = parent
	}
}

func gitOut(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

// remoteName is the repo name recall's --repo flag takes: the last path segment of the remote,
// minus a .git suffix.
func remoteName(url string) string {
	u := strings.TrimSuffix(strings.TrimSpace(url), ".git")
	u = strings.TrimSuffix(u, "/")
	if i := strings.LastIndexAny(u, "/:"); i >= 0 {
		u = u[i+1:]
	}
	return u
}
