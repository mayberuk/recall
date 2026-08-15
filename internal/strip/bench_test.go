package strip

import (
	"bufio"
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/mayberuk/recall/bench"
	"github.com/mayberuk/recall/internal/jsonl"
	"github.com/mayberuk/recall/internal/schema"
)

func TestMain(m *testing.M) {
	code := m.Run()
	bench.Cleanup()
	os.Exit(code)
}

func realCorpus(t testing.TB) []string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home directory: %v", err)
	}
	root := filepath.Join(home, ".claude", "projects")
	var files []string
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() && filepath.Ext(path) == ".jsonl" {
			files = append(files, path)
		}
		return nil
	})
	if err != nil || len(files) == 0 {
		t.Skipf("no readable session store under %s", root)
	}
	return files
}

// stripFrom reads each file from a per-file byte offset and strips it, exactly
// as the archive walk does. A zero offset is the cold pass over the whole
// corpus; the offsets a previous pass ended at make it the incremental one.
// It never writes: the session store is the only copy of the corpus.
func stripFrom(t testing.TB, files []string, from map[string]int64) (turns int, bytesOut int64) {
	s := New()
	for _, path := range files {
		r, err := jsonl.OpenAt(path, from[path])
		if err != nil {
			continue
		}
		for r.Next() {
			rec, ok := r.Record()
			if !ok {
				continue
			}
			out, _ := s.Strip(rec)
			for _, turn := range out {
				turns++
				bytesOut += int64(len(turn.Text))
			}
		}
		_ = r.Close()
	}
	return turns, bytesOut
}

// TestColdStripOverTheRealCorpusWithinGate measures the machine's own session
// store. `make bench-gate` enforces the same threshold against a generated
// corpus, which is the one a contributor can reproduce; this reports what the
// real shape costs.
func TestColdStripOverTheRealCorpusWithinGate(t *testing.T) {
	if os.Getenv("RECALL_REAL_CORPUS") != "1" {
		t.Skip("set RECALL_REAL_CORPUS=1 to measure against ~/.claude/projects")
	}
	if raceDetector {
		t.Skip("the race detector slows execution several-fold, so the wall-clock gate cannot be judged under it")
	}
	files := realCorpus(t)

	var runs []time.Duration
	var turns int
	var stripped int64
	for range 3 {
		start := time.Now()
		turns, stripped = stripFrom(t, files, nil)
		runs = append(runs, time.Since(start))
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i] < runs[j] })
	median := runs[1]

	t.Logf("full strip: %d files, %d turns, %d MB of text, runs=%v median=%s (gate %s)",
		len(files), turns, stripped>>20, runs, median, bench.StripCold.Limit)
	if median > bench.StripCold.Limit {
		t.Errorf("cold full strip took %s, over the %s gate", median, bench.StripCold.Limit)
	}
}

// BenchmarkStrip is the cold pass over a generated corpus and the incremental
// pass that follows it, which are the two costs the archive actually pays.
func BenchmarkStrip(b *testing.B) {
	for _, size := range bench.Sizes {
		g, err := bench.Corpus(size)
		if err != nil {
			b.Fatalf("Corpus(%s): %v", size, err)
		}
		files, err := bench.Files(g.Root)
		if err != nil {
			b.Fatalf("Files(%s): %v", size, err)
		}

		b.Run(string(size), func(b *testing.B) {
			b.Run("cold", func(b *testing.B) {
				b.ReportAllocs()
				b.ResetTimer()
				for range b.N {
					stripFrom(b, files, nil)
				}
			})
			b.Run("incremental", func(b *testing.B) {
				from := tailOffsets(b, files, 100)
				if turns, _ := stripFrom(b, files, from); turns == 0 {
					b.Fatal("the incremental pass stripped nothing, so it measures an empty read")
				}
				b.ReportAllocs()
				b.ResetTimer()
				for range b.N {
					stripFrom(b, files, from)
				}
			})
		})
	}
}

// BenchmarkStripRecord is the marginal cost of one record, over records drawn
// from the generated corpus rather than written by hand: a hand-written record
// measures the shape somebody thought to write down.
func BenchmarkStripRecord(b *testing.B) {
	for _, size := range bench.Sizes {
		g, err := bench.Corpus(size)
		if err != nil {
			b.Fatalf("Corpus(%s): %v", size, err)
		}
		files, err := bench.Files(g.Root)
		if err != nil {
			b.Fatalf("Files(%s): %v", size, err)
		}
		recs := sampleRecords(b, files[0], 512)

		b.Run(string(size), func(b *testing.B) {
			s := New()
			var sink []schema.Turn
			b.ReportAllocs()
			b.ResetTimer()
			for i := range b.N {
				sink, _ = s.Strip(recs[i%len(recs)])
			}
			_ = sink
		})
	}
}

// tailOffsets is where a cursor from a previous pass would sit: the last
// 1/divisor of each file, rounded forward to a line boundary, because a cursor
// only ever holds the offset just past a record it read whole.
func tailOffsets(t testing.TB, files []string, divisor int64) map[string]int64 {
	t.Helper()
	out := make(map[string]int64, len(files))
	for _, path := range files {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		at := int64(len(body)) - int64(len(body))/divisor
		if next := bytes.IndexByte(body[at:], '\n'); next >= 0 {
			at += int64(next) + 1
		}
		out[path] = at
	}
	return out
}

func sampleRecords(t testing.TB, path string, most int) []jsonl.Record {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()

	var recs []jsonl.Record
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 8<<20)
	for sc.Scan() && len(recs) < most {
		line := bytes.Clone(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		rec, ok := jsonl.Parse(jsonl.Line{Bytes: line, Length: len(line)})
		if !ok {
			continue
		}
		recs = append(recs, rec)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if len(recs) == 0 {
		t.Fatalf("no records parsed from %s", path)
	}
	return recs
}
