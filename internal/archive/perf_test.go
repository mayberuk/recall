package archive

import (
	"os"
	"testing"
	"time"

	"github.com/mayberuk/recall/bench"
	"github.com/mayberuk/recall/internal/repo"
	"github.com/mayberuk/recall/internal/schema"
	"github.com/mayberuk/recall/internal/strip"
)

func TestMain(m *testing.M) {
	code := m.Run()
	bench.Cleanup()
	os.Exit(code)
}

// The no-index architecture rests on measurement, so the gates are correctness
// properties, not warnings. Opt in with RECALL_REAL_CORPUS=1; the corpus is read
// and never written, and the archive goes to a temp directory.
func TestRealCorpusPerformanceGates(t *testing.T) {
	if os.Getenv("RECALL_REAL_CORPUS") != "1" {
		t.Skip("set RECALL_REAL_CORPUS=1 to measure against ~/.claude/projects")
	}
	if raceEnabled {
		t.Skip("a wall-clock gate under the race detector measures the detector")
	}
	root, err := DefaultRoot()
	if err != nil {
		t.Fatalf("DefaultRoot: %v", err)
	}
	if _, err := os.Stat(root); err != nil {
		t.Skipf("no corpus at %s: %v", root, err)
	}

	// The stub strip this package's other tests use emits conversation turns
	// only, which would leave the tier split untested at the size that matters.
	s := storeWith(t, root, "", strip.New().Strip)

	start := time.Now()
	cold := mustUpdate(t, s)
	coldTook := time.Since(start)

	// Best of five, because the incremental pass is a couple of dozen
	// milliseconds and one sample of that is noise. Every repeat is another no-op
	// refresh, so the best of them is the figure a caller invoking the CLI back to
	// back actually waits through.
	var warm Result
	warmTook := time.Duration(1<<62 - 1)
	for range 5 {
		start = time.Now()
		warm = mustUpdate(t, s)
		if took := time.Since(start); took < warmTook {
			warmTook = took
		}
	}

	m, ok := s.loadMeta()
	if !ok {
		t.Fatal("metadata did not load")
	}
	var onDisk int64
	for _, tier := range tierFiles {
		st := m.Tiers[string(tier)]
		onDisk += st.Bytes
		t.Logf("  %-12s %8.1f MB  %7d turns", tier, float64(st.Bytes)/(1<<20), st.Turns)
	}
	t.Logf("cold %.2fs (gate %s) · incremental %.1f ms (gate %s) · %d files · %d records · %d turns · %d sessions · %.1f MB on disk · vanished %d · unreadable %d",
		coldTook.Seconds(), bench.ArchiveCold.Limit, float64(warmTook.Microseconds())/1000, bench.ArchiveIncremental.Limit,
		cold.FilesSeen, cold.RecordsRead, m.Turns, m.Sessions, float64(onDisk)/(1<<20),
		len(cold.Vanished), len(cold.Unreadable))
	t.Logf("coverage: live from %s · content from %s to %s · archive reaches before the live window: %v",
		cold.Coverage.LiveFrom.Format(time.RFC3339), cold.Coverage.ContentFrom.Format(time.RFC3339),
		cold.Coverage.ContentTo.Format(time.RFC3339), cold.Coverage.ReachesBeforeLive())
	t.Logf("worst per-file skew: %d days on %s", cold.Coverage.MaxFileSkewDays(), cold.Coverage.MaxSkewFile)

	convTook := timeLoad(t, s, schema.TierConversation)
	allTook := timeLoad(t, s)
	t.Logf("load: conversation %.0f ms (budget %s) · all tiers %.0f ms (budget %s)",
		float64(convTook.Microseconds())/1000, bench.LoadConversation.Limit,
		float64(allTook.Microseconds())/1000, bench.LoadAllTiers.Limit)

	for _, m := range []struct {
		gate     bench.Gate
		measured time.Duration
	}{
		{bench.ArchiveCold, coldTook},
		{bench.ArchiveIncremental, warmTook},
		{bench.LoadConversation, convTook},
		{bench.LoadAllTiers, allTook},
	} {
		if m.measured > m.gate.Limit {
			t.Errorf("%s took %s, over the %s gate", m.gate.Name, m.measured, m.gate.Limit)
		}
	}

	// A live session writes to its own transcript while this runs, so a handful
	// of files legitimately grow or appear between the two passes. A cursor that
	// was not trusted would re-read all of them.
	if warm.FilesSkipped*100 < warm.FilesSeen*99 {
		t.Errorf("incremental pass skipped only %d of %d files", warm.FilesSkipped, warm.FilesSeen)
	}
	t.Logf("incremental churn: %d grew, %d new or re-read whole, %d skipped of %d",
		warm.FilesAppended, warm.FilesWhole, warm.FilesSkipped, warm.FilesSeen)
}

// timeLoad reports the best of three reads, which is what a warm page cache
// gives a CLI invoked repeatedly.
func timeLoad(t *testing.T, s *Store, tiers ...schema.Tier) time.Duration {
	t.Helper()
	best := time.Duration(1<<62 - 1)
	for range 3 {
		start := time.Now()
		if _, err := s.Turns(tiers...); err != nil {
			t.Fatalf("Turns(%v): %v", tiers, err)
		}
		if took := time.Since(start); took < best {
			best = took
		}
	}
	return best
}

// BenchmarkArchive is the cold build and the update that finds nothing new,
// which are the two passes every invocation of the CLI chooses between.
func BenchmarkArchive(b *testing.B) {
	for _, size := range bench.Sizes {
		g, err := bench.Corpus(size)
		if err != nil {
			b.Fatalf("Corpus(%s): %v", size, err)
		}

		b.Run(string(size), func(b *testing.B) {
			b.Run("cold", func(b *testing.B) {
				b.ReportAllocs()
				for range b.N {
					b.StopTimer()
					s := benchStore(b, g.Root, b.TempDir())
					b.StartTimer()
					if _, err := s.Update(); err != nil {
						b.Fatalf("Update: %v", err)
					}
				}
			})
			b.Run("warm", func(b *testing.B) {
				s := benchStore(b, g.Root, b.TempDir())
				res, err := s.Update()
				if err != nil {
					b.Fatalf("Update: %v", err)
				}
				if res.FilesSeen == 0 {
					b.Fatal("the cold build saw no files, so the warm pass measures nothing")
				}
				b.ReportAllocs()
				b.ResetTimer()
				for range b.N {
					if _, err := s.Update(); err != nil {
						b.Fatalf("Update: %v", err)
					}
				}
			})
		})
	}
}

// BenchmarkLoad is reading the archive back, which every search does before it
// searches anything and which no other benchmark isolates. The two shapes are
// the two a search asks for: the conversation tier alone, and every tier.
func BenchmarkLoad(b *testing.B) {
	for _, size := range bench.Sizes {
		g, err := bench.Corpus(size)
		if err != nil {
			b.Fatalf("Corpus(%s): %v", size, err)
		}
		dir := b.TempDir()
		s := benchStore(b, g.Root, dir)
		if _, err := s.Update(); err != nil {
			b.Fatalf("Update: %v", err)
		}

		b.Run(string(size), func(b *testing.B) {
			for _, shape := range []struct {
				name  string
				tiers []schema.Tier
			}{
				{"conversation", []schema.Tier{schema.TierConversation}},
				{"all-tiers", nil},
			} {
				b.Run(shape.name, func(b *testing.B) {
					turns, err := s.Turns(shape.tiers...)
					if err != nil {
						b.Fatalf("Turns: %v", err)
					}
					if len(turns) == 0 {
						b.Fatal("the archive holds no turns, so this measures nothing")
					}
					b.ReportAllocs()
					b.ResetTimer()
					for range b.N {
						if _, err := s.Turns(shape.tiers...); err != nil {
							b.Fatalf("Turns: %v", err)
						}
					}
				})
			}
		})
	}
}

func benchStore(b *testing.B, root, dir string) *Store {
	b.Helper()
	s, err := Open(Options{Dir: dir, Root: root, Provider: strip.ClaudeCode(), Resolve: repo.New().Repo})
	if err != nil {
		b.Fatalf("Open: %v", err)
	}
	return s
}
