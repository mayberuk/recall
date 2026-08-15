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

	start = time.Now()
	warm := mustUpdate(t, s)
	warmTook := time.Since(start)

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
	t.Logf("cold %.2fs (gate %s) · incremental %.2fs (gate %s) · %d files · %d records · %d turns · %d sessions · %.1f MB on disk · vanished %d · unreadable %d",
		coldTook.Seconds(), bench.ArchiveCold.Limit, warmTook.Seconds(), bench.ArchiveIncremental.Limit,
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

func benchStore(b *testing.B, root, dir string) *Store {
	b.Helper()
	s, err := Open(Options{Dir: dir, Root: root, Strip: strip.New().Strip, Resolve: repo.New().Repo})
	if err != nil {
		b.Fatalf("Open: %v", err)
	}
	return s
}
