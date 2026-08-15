package repo

import (
	"os"
	"path/filepath"
	"strings"
)

// pointerLimit caps the .git pointer read. The file holds one gitdir line;
// anything larger is not a pointer and is left to the walk above.
const pointerLimit = 4 << 10

// commonDir is the git directory shared by a repo and all of its worktrees,
// found from dir's own .git entry. Absent or unreadable is not an error: the
// caller keeps walking.
func commonDir(dir string) (string, bool) {
	gitPath := filepath.Join(dir, ".git")
	fi, err := os.Stat(gitPath)
	if err != nil {
		return "", false
	}
	if fi.IsDir() {
		return gitPath, true
	}
	gitdir, ok := readPointer(gitPath, dir)
	if !ok {
		return "", false
	}
	if link, ok := readLine(filepath.Join(gitdir, "commondir")); ok {
		return resolveAgainst(gitdir, link), true
	}
	if fi, err := os.Stat(gitdir); err == nil && fi.IsDir() {
		return gitdir, true
	}
	return prunedCommonDir(gitdir)
}

// prunedCommonDir recovers the parent repo of a worktree whose gitdir has been
// pruned, which is where `git remote get-url origin` exits 128. A linked
// worktree's gitdir is always <common>/worktrees/<id>, and the candidate is
// accepted only when a config file is really there, so no repo is invented from
// the shape of a path alone.
func prunedCommonDir(gitdir string) (string, bool) {
	parent := filepath.Dir(gitdir)
	if filepath.Base(parent) != "worktrees" {
		return "", false
	}
	candidate := filepath.Dir(parent)
	if fi, err := os.Stat(filepath.Join(candidate, "config")); err != nil || fi.IsDir() {
		return "", false
	}
	return candidate, true
}

func toplevelOf(common string) string {
	if filepath.Base(common) == ".git" {
		return filepath.Dir(common)
	}
	return common
}

// callerFrame restates a repo root in the path frame the cwd was recorded in.
// git writes a symlink-resolved absolute path into a worktree's .git pointer, so
// a worktree inside its parent would otherwise key on /private/var where the
// session's own cwd says /var — one repo, two identities.
func callerFrame(dir, top string) string {
	if top == dir || strings.HasPrefix(dir, top+string(filepath.Separator)) {
		return top
	}
	target, err := os.Stat(top)
	if err != nil {
		return top
	}
	for at := dir; ; {
		if fi, err := os.Stat(at); err == nil && os.SameFile(fi, target) {
			return at
		}
		parent := filepath.Dir(at)
		if parent == at {
			return top
		}
		at = parent
	}
}

func readPointer(path, base string) (string, bool) {
	if fi, err := os.Stat(path); err != nil || fi.Size() > pointerLimit {
		return "", false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(string(data), "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "gitdir:")
		if !ok {
			continue
		}
		target := strings.TrimSpace(rest)
		if target == "" {
			return "", false
		}
		return resolveAgainst(base, target), true
	}
	return "", false
}

func readLine(path string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	line, _, _ := strings.Cut(string(data), "\n")
	line = strings.TrimSpace(line)
	if line == "" {
		return "", false
	}
	return line, true
}

func resolveAgainst(base, target string) string {
	if filepath.IsAbs(target) {
		return filepath.Clean(target)
	}
	return filepath.Clean(filepath.Join(base, target))
}
