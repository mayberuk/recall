// Package repo resolves a session's cwd to the repo identity it belongs to.
//
// One logical repo routinely spans a dozen checkout directories once clones
// and worktrees are counted, so identity is the configured remote, found by
// walking up from cwd. The walk continues past any failure at a level, not
// only a missing directory: an orphaned worktree's .git file points at a
// pruned gitdir where git itself exits 128, and the session there still
// belongs to the parent repo. Nothing here runs git or reaches the network.
package repo

import (
	"path/filepath"
	"strings"
	"sync"
)

// Kind classifies what a cwd resolved to. The values are the identities the
// shared fixtures pin, so a test compares against fixtures.Manifest directly.
type Kind string

const (
	KindRemote   Kind = "remote"
	KindNoRemote Kind = "repo, no remote"
	KindNone     Kind = "outside any repo"
)

// Identity is one repo as resolved from a cwd. ID is what a stripped turn
// carries: a normalized remote, or the toplevel path when the repo has none.
// Both are stable across every checkout and worktree of the same repo.
type Identity struct {
	Kind     Kind
	ID       string
	Name     string
	Remote   string
	Toplevel string
}

var unresolved = Identity{Kind: KindNone, ID: string(KindNone)}

// Resolver caches by directory. A strip pass resolves ~300K records across ~150
// distinct directories, so the cache is what keeps resolution off the profile.
type Resolver struct {
	mu    sync.Mutex
	cache map[string]Identity
}

// New returns an empty resolver. A resolver caches for its own lifetime and
// never invalidates, so a long-lived one will not see a repo gain a remote.
func New() *Resolver { return &Resolver{cache: make(map[string]Identity)} }

// Repo is Resolve's ID, the form internal/archive injects and a stripped turn
// stores.
func (r *Resolver) Repo(cwd string) string { return r.Resolve(cwd).ID }

// Resolve walks up from cwd to the nearest ancestor with a configured remote,
// falling back to the innermost repo on the way when no ancestor has one.
func (r *Resolver) Resolve(cwd string) Identity {
	// A relocated record carries relocatedCwd and no cwd. Resolving "" would
	// walk from recall's own working directory and file those turns under
	// whatever repo it happened to be run from.
	if strings.TrimSpace(cwd) == "" {
		return unresolved
	}
	start := filepath.Clean(cwd)
	if !filepath.IsAbs(start) {
		return unresolved
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	var chain []string
	var found []Identity
	inherited := unresolved
	for dir := start; ; {
		if cached, ok := r.cache[dir]; ok {
			inherited = cached
			break
		}
		here := identityAt(dir)
		chain = append(chain, dir)
		found = append(found, here)
		if here.Kind == KindRemote {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	// Assigning top-down is what makes the cache safe: a remoteless repo answers
	// only for itself and below, never for the ancestors walked past it, and it
	// never displaces a remote found higher, as a vendored checkout would.
	answer := inherited
	for i := len(chain) - 1; i >= 0; i-- {
		switch {
		case found[i].Kind == KindRemote:
			answer = found[i]
		case found[i].Kind == KindNoRemote && answer.Kind != KindRemote:
			answer = found[i]
		}
		r.cache[chain[i]] = answer
	}
	return answer
}

// identityAt is the repo rooted at or pointed to by dir, with a zero Kind when
// dir is not part of one.
func identityAt(dir string) Identity {
	common, ok := commonDir(dir)
	if !ok {
		return Identity{}
	}
	top := callerFrame(dir, toplevelOf(common))
	if raw := originURL(filepath.Join(common, "config")); raw != "" {
		if id, name := normalizeRemote(raw); id != "" {
			return Identity{Kind: KindRemote, ID: id, Name: name, Remote: raw, Toplevel: top}
		}
	}
	return Identity{Kind: KindNoRemote, ID: top, Name: filepath.Base(top), Toplevel: top}
}
