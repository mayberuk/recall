package strip

import (
	"io/fs"
	"path/filepath"
	"sort"
	"sync"
	"testing"

	"github.com/mayberuk/recall/internal/fixtures"
	"github.com/mayberuk/recall/internal/jsonl"
	"github.com/mayberuk/recall/internal/schema"
)

func corpusPaths(t *testing.T, c fixtures.Corpus) []string {
	t.Helper()
	var paths []string
	err := filepath.WalkDir(c.Root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && filepath.Ext(path) == ".jsonl" {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk corpus: %v", err)
	}
	sort.Strings(paths)
	return paths
}

func stripPath(t *testing.T, s *Stripper, path string) []schema.Turn {
	t.Helper()
	r, err := jsonl.Open(path)
	if err != nil {
		t.Errorf("open %s: %v", path, err)
		return nil
	}
	defer r.Close()
	var out []schema.Turn
	for r.Next() {
		if rec, ok := r.Record(); ok {
			turns, _ := s.Strip(rec)
			out = append(out, turns...)
		}
	}
	return out
}

func sortTurns(turns []schema.Turn) {
	sort.Slice(turns, func(i, j int) bool {
		a, b := turns[i], turns[j]
		switch {
		case a.Session != b.Session:
			return a.Session < b.Session
		case a.UUID != b.UUID:
			return a.UUID < b.UUID
		case a.Tier != b.Tier:
			return a.Tier < b.Tier
		default:
			return a.Text < b.Text
		}
	})
}

// The archive walks the corpus on a worker pool and injects Strip as one shared
// function, so a shared Stripper must produce the same turns and the same
// counts as a sequential pass. Run under -race, this is what catches a scratch
// buffer or an unguarded counter before it becomes a flaky archive.
func TestStripIsSafeUnderConcurrentUse(t *testing.T) {
	c := fixtures.Materialize(t)
	paths := corpusPaths(t, c)

	sequential := New()
	var want []schema.Turn
	const rounds = 4
	for i := 0; i < rounds; i++ {
		for _, path := range paths {
			want = append(want, stripPath(t, sequential, path)...)
		}
	}
	sortTurns(want)

	shared := New()
	var mu sync.Mutex
	var got []schema.Turn
	var wg sync.WaitGroup
	for i := 0; i < rounds; i++ {
		for _, path := range paths {
			wg.Add(1)
			go func(path string) {
				defer wg.Done()
				turns := stripPath(t, shared, path)
				mu.Lock()
				got = append(got, turns...)
				mu.Unlock()
			}(path)
		}
	}
	wg.Wait()
	sortTurns(got)

	if len(got) != len(want) {
		t.Fatalf("concurrent pass produced %d turns, sequential produced %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("turn %d differs:\n concurrent %#v\n sequential %#v", i, got[i], want[i])
		}
	}

	wantObs, gotObs := sequential.Observation(), shared.Observation()
	if gotObs.Typed != wantObs.Typed || gotObs.CommandArgs != wantObs.CommandArgs ||
		gotObs.HumanShapedMain != wantObs.HumanShapedMain || gotObs.Tally.Lines != wantObs.Tally.Lines {
		t.Errorf("observation differs: concurrent %+v, sequential %+v", gotObs, wantObs)
	}
	for typ, n := range wantObs.Tally.Unknown {
		if gotObs.Tally.Unknown[typ] != n {
			t.Errorf("unknown type %q counted %d concurrently, %d sequentially", typ, gotObs.Tally.Unknown[typ], n)
		}
	}
}

// A caller may keep one Stripper per worker instead of sharing one. Merging
// their observations must land on the same corpus-wide numbers.
func TestPerWorkerObservationsMerge(t *testing.T) {
	c := fixtures.Materialize(t)
	paths := corpusPaths(t, c)

	sequential := New()
	for _, path := range paths {
		stripPath(t, sequential, path)
	}

	var mu sync.Mutex
	var merged Observation
	var wg sync.WaitGroup
	for _, path := range paths {
		wg.Add(1)
		go func(path string) {
			defer wg.Done()
			worker := New()
			stripPath(t, worker, path)
			obs := worker.Observation()
			mu.Lock()
			merged.Merge(obs)
			mu.Unlock()
		}(path)
	}
	wg.Wait()

	want := sequential.Observation()
	if merged.Typed != want.Typed || merged.CommandArgs != want.CommandArgs ||
		merged.HumanShapedMain != want.HumanShapedMain || merged.Tally.Lines != want.Tally.Lines {
		t.Errorf("merged %+v, want %+v", merged, want)
	}
	for typ, n := range want.Tally.Unknown {
		if merged.Tally.Unknown[typ] != n {
			t.Errorf("unknown type %q merged to %d, want %d", typ, merged.Tally.Unknown[typ], n)
		}
	}
}
