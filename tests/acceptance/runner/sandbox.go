package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
)

// A sandbox is a throwaway archive location. internal/archive resolves its directory from
// RECALL_HOME (then XDG_DATA_HOME), and its corpus from CLAUDE_PROJECTS_DIR, so a case can have a
// private, deletable archive and still read the one real corpus. HOME is deliberately left alone:
// overriding it changes how git behaves during repo resolution, which several cases are about.
type sandbox struct {
	Name    string `json:"name"`
	Root    string `json:"root"`
	Archive string `json:"archive"`
	Corpus  string `json:"corpus"`
}

func newSandbox(parent, name, corpusRoot string) (*sandbox, error) {
	root := filepath.Join(parent, name)
	archive := filepath.Join(root, "archive")
	if err := os.MkdirAll(archive, 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(root, "xdg-data"), 0o755); err != nil {
		return nil, err
	}
	return &sandbox{Name: name, Root: root, Archive: archive, Corpus: corpusRoot}, nil
}

func (s *sandbox) env() map[string]string {
	return map[string]string{
		"RECALL_HOME":         s.Archive,
		"CLAUDE_PROJECTS_DIR": s.Corpus,
		"XDG_DATA_HOME":       filepath.Join(s.Root, "xdg-data"),
		// The spike found this machine's FZF_DEFAULT_OPTS injects a bat-based --preview that
		// hijacks any fzf invocation that does not clear it.
		"FZF_DEFAULT_OPTS": "",
		"NO_COLOR":         "1",
		"CLICOLOR":         "0",
		"TERM":             "dumb",
	}
}

type fileDigest struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

func snapshotTree(root string) ([]fileDigest, error) {
	var out []fileDigest
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		sum, err := sha256File(path)
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		out = append(out, fileDigest{Path: rel, Size: info.Size(), SHA256: sum})
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, err
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

type corpusEntry struct {
	Size   int64  `json:"size"`
	MTime  int64  `json:"mtime"`
	SHA256 string `json:"sha256,omitempty"`
}

type corpusManifest struct {
	Root    string                 `json:"root"`
	Files   int                    `json:"files"`
	Bytes   int64                  `json:"bytes"`
	Digest  string                 `json:"digest"`
	entries map[string]corpusEntry `json:"-"`
}

// manifestCorpus fingerprints the corpus before and after the run so any write under
// ~/.claude/projects is caught. The before pass hashes contents; the after pass only stats, and
// diffCorpus re-hashes the handful of files that moved. Without content, a same-size mtime change
// is ambiguous, and a write under ~/.claude/projects cannot be undone once made.
func manifestCorpus(root string, hash bool) (corpusManifest, error) {
	m := corpusManifest{Root: root, entries: map[string]corpusEntry{}}
	var paths []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		m.entries[rel] = corpusEntry{Size: info.Size(), MTime: info.ModTime().UnixNano()}
		paths = append(paths, rel)
		m.Files++
		m.Bytes += info.Size()
		return nil
	})
	if hash {
		hashAll(root, paths, m.entries)
	}
	lines := make([]string, 0, len(m.entries))
	for rel, e := range m.entries {
		lines = append(lines, fmt.Sprintf("%s\t%d\t%d\t%s", rel, e.Size, e.MTime, e.SHA256))
	}
	sort.Strings(lines)
	h := sha256.New()
	h.Write([]byte(strings.Join(lines, "\n")))
	m.Digest = hex.EncodeToString(h.Sum(nil))
	return m, err
}

func hashAll(root string, paths []string, entries map[string]corpusEntry) {
	work := make(chan string, len(paths))
	for _, p := range paths {
		work <- p
	}
	close(work)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < runtime.NumCPU(); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for rel := range work {
				sum, err := sha256File(filepath.Join(root, rel))
				if err != nil {
					continue
				}
				mu.Lock()
				e := entries[rel]
				e.SHA256 = sum
				entries[rel] = e
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
}

type corpusDelta struct {
	Grew           []string `json:"grew_append_only"`
	TouchedOnly    []string `json:"touched_but_content_identical"`
	ContentChanged []string `json:"content_changed_without_growing"`
	Shrank         []string `json:"shrank"`
	Appeared       []string `json:"appeared"`
	Removed        []string `json:"removed"`
}

func diffCorpus(before, after corpusManifest) corpusDelta {
	var d corpusDelta
	for rel, b := range before.entries {
		a, ok := after.entries[rel]
		switch {
		case !ok:
			d.Removed = append(d.Removed, rel)
		case a.Size > b.Size:
			d.Grew = append(d.Grew, rel)
		case a.Size < b.Size:
			d.Shrank = append(d.Shrank, rel)
		case a.MTime != b.MTime:
			sum, err := sha256File(filepath.Join(after.Root, rel))
			if err != nil || sum != b.SHA256 {
				d.ContentChanged = append(d.ContentChanged, rel)
			} else {
				d.TouchedOnly = append(d.TouchedOnly, rel)
			}
		}
	}
	for rel := range after.entries {
		if _, ok := before.entries[rel]; !ok {
			d.Appeared = append(d.Appeared, rel)
		}
	}
	for _, s := range [][]string{d.Grew, d.TouchedOnly, d.ContentChanged, d.Shrank, d.Appeared, d.Removed} {
		sort.Strings(s)
	}
	return d
}

// dangerous names only the changes a read cannot produce and an append does not explain.
func (d corpusDelta) dangerous() bool {
	return len(d.Shrank) > 0 || len(d.Removed) > 0 || len(d.ContentChanged) > 0
}
