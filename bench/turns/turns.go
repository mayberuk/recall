// Package turns strips a generated corpus into the form scan, rank and the
// archive are measured against.
//
// It sits below bench rather than inside it because internal/strip's own
// benchmarks import bench, and a package that strips cannot be imported by the
// package doing the stripping.
package turns

import (
	"fmt"
	"sync"

	"github.com/mayberuk/recall/bench"
	"github.com/mayberuk/recall/internal/jsonl"
	"github.com/mayberuk/recall/internal/schema"
	"github.com/mayberuk/recall/internal/strip"
)

// Strip reads every transcript under root, in a fixed file order, and returns
// the turns.
func Strip(root string) ([]schema.Turn, error) {
	files, err := bench.Files(root)
	if err != nil {
		return nil, err
	}
	s := strip.New()
	var out []schema.Turn
	for _, path := range files {
		r, err := jsonl.Open(path)
		if err != nil {
			return nil, fmt.Errorf("bench: cannot open %s: %w", path, err)
		}
		for r.Next() {
			if rec, ok := r.Record(); ok {
				turns, _ := s.Strip(rec)
				out = append(out, turns...)
			}
		}
		if err := r.Close(); err != nil {
			return nil, fmt.Errorf("bench: cannot close %s: %w", path, err)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("bench: %s stripped to nothing", root)
	}
	return out, nil
}

// Load is the stripped corpus of a size, held for the life of the process:
// every search benchmark reads the same slice, so what they report is the cost
// of searching and not the cost of stripping again.
func Load(size bench.Size) ([]schema.Turn, error) {
	cache.Lock()
	defer cache.Unlock()
	if turns, ok := cache.bySize[size]; ok {
		return turns, nil
	}
	g, err := bench.Corpus(size)
	if err != nil {
		return nil, err
	}
	turns, err := Strip(g.Root)
	if err != nil {
		return nil, err
	}
	if cache.bySize == nil {
		cache.bySize = map[bench.Size][]schema.Turn{}
	}
	cache.bySize[size] = turns
	return turns, nil
}

var cache struct {
	sync.Mutex
	bySize map[bench.Size][]schema.Turn
}
