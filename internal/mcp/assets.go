package mcp

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Assets is the instruction and install-manifest tree a shipped binary has no
// repo to read from otherwise: SKILL.md, the Cursor and Windsurf rules, and
// the per-client plugin/extension manifests.
//
// The "all:" prefix is required, not decorative: several paths under assets
// (.claude-plugin/, .codex-plugin/, .mcp.json) are dot-prefixed by the
// vendors' own conventions, and a bare "//go:embed assets" silently drops any
// file or directory whose name starts with "." or "_".
//
//go:embed all:assets
var Assets embed.FS

// assetsRoot is the embedded tree's own root, fixed by the directive above.
const assetsRoot = "assets"

// ExportAssets writes the embedded tree to dir, creating directories as
// needed. integrations/ is this call's checked-in output: a shipped binary
// has no repo to read these files from, so they are embedded here and this
// function is what a marketplace consumer's checkout is generated from.
func ExportAssets(dir string) error {
	return fs.WalkDir(Assets, assetsRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(assetsRoot, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return os.MkdirAll(dir, 0o755)
		}

		target := filepath.Join(dir, rel)
		if !withinDir(dir, target) {
			return fmt.Errorf("mcp: refusing to write %q outside %q", target, dir)
		}

		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := Assets.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

// withinDir reports whether target is dir itself or a descendant of it. Every
// path fs.WalkDir hands the callback above is already relative and free of
// ".." components by the fs.FS contract, so this can never actually trigger
// against the embedded tree; it stands as the escape guard ExportAssets's own
// contract promises, rather than trusting that contract silently.
func withinDir(dir, target string) bool {
	dir = filepath.Clean(dir)
	target = filepath.Clean(target)
	if target == dir {
		return true
	}
	return strings.HasPrefix(target, dir+string(filepath.Separator))
}
