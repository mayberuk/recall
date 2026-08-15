// Package atomicfile is the single write path for every file recall mutates.
//
// Writes create the temp file in the target's own directory (so the rename never
// crosses a filesystem), fsync before renaming (so a crash cannot leave a
// truncated target), preserve the target's existing mode, and remove the temp on
// every error path. The archive and its cursor depend on this: a half-written
// archive is unverifiable once the raw transcripts have been deleted.
package atomicfile

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/mayberuk/recall/internal/fperr"
)

const defaultMode os.FileMode = 0o644

// Write replaces path's contents with data, keeping the target's current mode.
func Write(path string, data []byte) error {
	mode := defaultMode
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	return WriteMode(path, data, mode)
}

// WriteMode replaces path's contents with data and forces mode.
func WriteMode(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)

	tmp, err := os.CreateTemp(dir, ".recall-tmp-*")
	if err != nil {
		return fperr.New(fperr.AtomicWriteFailed, "cannot create a temp file in %s: %v", dir, err)
	}
	tmpName := tmp.Name()

	cleanup := func(cause error) error {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return cause
	}

	if _, err := tmp.Write(data); err != nil {
		return cleanup(fperr.New(fperr.AtomicWriteFailed, "cannot write %s: %v", tmpName, err))
	}
	if err := tmp.Sync(); err != nil {
		return cleanup(fperr.New(fperr.AtomicWriteFailed, "cannot fsync %s: %v", tmpName, err))
	}
	if err := tmp.Chmod(mode); err != nil {
		return cleanup(fperr.New(fperr.AtomicWriteFailed, "cannot set mode on %s: %v", tmpName, err))
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fperr.New(fperr.AtomicWriteFailed, "cannot close %s: %v", tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fperr.New(fperr.AtomicWriteFailed, "cannot rename %s to %s: %v", tmpName, path, err)
	}
	return nil
}

// WriteJSON encodes v and writes it atomically.
func WriteJSON(path string, v any) error {
	data, err := Marshal(v)
	if err != nil {
		return err
	}
	return Write(path, data)
}

// Marshal encodes v the one way recall encodes JSON: indented, HTML-unescaped,
// newline-terminated. Identical input always produces identical bytes, which is
// what makes the golden-file tests and the byte-identical-archive gate (a9)
// meaningful.
func Marshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, fperr.New(fperr.AtomicWriteFailed, "cannot encode JSON for %T: %v", v, err)
	}
	return buf.Bytes(), nil
}

// MarshalCompact encodes v as one line, the shape every JSONL row uses.
func MarshalCompact(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, fperr.New(fperr.AtomicWriteFailed, "cannot encode JSON for %T: %v", v, err)
	}
	return buf.Bytes(), nil
}
