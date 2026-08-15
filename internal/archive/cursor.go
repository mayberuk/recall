package archive

import (
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/mayberuk/recall/internal/atomicfile"
	"github.com/mayberuk/recall/internal/fperr"
)

// parseCursor reads the per-file mark list. One malformed line discards the
// whole set: "no marks known" re-reads everything, and re-reading is the only
// direction that cannot lose a turn. Salvaging the lines that did parse would
// keep a mark for a file whose real mark is unknown, which loses turns silently.
func parseCursor(data []byte) (map[string]int64, bool) {
	marks := make(map[string]int64)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			continue
		}
		// A relative path may itself contain a colon, so the separator is the
		// last one, not the first.
		i := strings.LastIndexByte(line, ':')
		if i <= 0 || i == len(line)-1 {
			return nil, false
		}
		rel, size := line[:i], line[i+1:]
		n, err := strconv.ParseInt(size, 10, 64)
		if err != nil || n < 0 {
			return nil, false
		}
		if _, dup := marks[rel]; dup {
			return nil, false
		}
		marks[rel] = n
	}
	return marks, true
}

func formatCursor(marks map[string]int64) []byte {
	var b strings.Builder
	for _, rel := range slices.Sorted(maps.Keys(marks)) {
		b.WriteString(rel)
		b.WriteByte(':')
		b.WriteString(strconv.FormatInt(marks[rel], 10))
		b.WriteByte('\n')
	}
	return []byte(b.String())
}

// loadCursor reports ok=false for an absent, unreadable, or unparseable cursor.
// All three mean the same thing to the caller: no marks are known.
func (s *Store) loadCursor() (map[string]int64, bool) {
	data, err := os.ReadFile(s.CursorPath())
	if err != nil {
		return nil, false
	}
	return parseCursor(data)
}

func (s *Store) writeCursor(marks map[string]int64) error {
	if err := atomicfile.Write(s.CursorPath(), formatCursor(marks)); err != nil {
		return fperr.New(fperr.AtomicWriteFailed, "cannot write the cursor: %v", err)
	}
	return nil
}
