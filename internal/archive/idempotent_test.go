package archive

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/mayberuk/recall/internal/fixtures"
	"github.com/mayberuk/recall/internal/schema"
)

// The cheap half of a9: a second run over an unchanged corpus does not reopen
// the archive at all. Byte-identity here proves only that nothing was written —
// the encoding is deterministic is proved by the two tests below.
func TestUnchangedSecondRunNeverTouchesTheArchive(t *testing.T) {
	c := corpus(t)
	s := newStore(t, c.Root)
	mustUpdate(t, s)
	first := archived(t, s)
	before, err := os.Stat(s.TierPath(schema.TierConversation))
	if err != nil {
		t.Fatalf("stat the archive: %v", err)
	}

	files := storeFiles(t, s)
	res := mustUpdate(t, s)
	if res.Wrote {
		t.Error("a second run rewrote the store")
	}
	sameFiles(t, "unchanged second run", files, storeFiles(t, s))
	after, err := os.Stat(s.TierPath(schema.TierConversation))
	if err != nil {
		t.Fatalf("stat the archive: %v", err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("the archive was rewritten: mtime moved from %s to %s", before.ModTime(), after.ModTime())
	}
	if !bytes.Equal(first, archived(t, s)) {
		t.Fatal("a second run changed the archive bytes")
	}
}

// The write order is archive, then metadata, then cursor. A cursor that lands
// first claims turns the archive does not hold, and once the raw files age out
// there is nothing left to notice the gap against.
func TestAFailedArchiveWriteLeavesTheCursorAndMetadataAlone(t *testing.T) {
	c := corpus(t)
	s := newStore(t, c.Root)
	mustUpdate(t, s)

	cursorBefore, err := os.ReadFile(s.CursorPath())
	if err != nil {
		t.Fatalf("read the cursor: %v", err)
	}
	metaBefore, err := os.ReadFile(s.MetaPath())
	if err != nil {
		t.Fatalf("read the metadata: %v", err)
	}

	appendRecord(t, c.Path(fixtures.FileRemoteless),
		grownRecord(fixtures.SessRemoteless, "eeeeeeee-0000-4000-8000-00000000000b",
			"2026-08-12T22:00:00.000Z", "written while the archive is unwritable"))

	if err := os.Remove(s.TierPath(schema.TierConversation)); err != nil {
		t.Fatalf("remove the archive: %v", err)
	}
	if err := os.Mkdir(s.TierPath(schema.TierConversation), 0o755); err != nil {
		t.Fatalf("block the archive path: %v", err)
	}

	if _, err := s.Update(); err == nil {
		t.Fatal("Update reported success with an unwritable archive path")
	}

	cursorAfter, err := os.ReadFile(s.CursorPath())
	if err != nil {
		t.Fatalf("read the cursor: %v", err)
	}
	if !bytes.Equal(cursorBefore, cursorAfter) {
		t.Error("the cursor advanced past turns the archive does not hold")
	}
	metaAfter, err := os.ReadFile(s.MetaPath())
	if err != nil {
		t.Fatalf("read the metadata: %v", err)
	}
	if !bytes.Equal(metaBefore, metaAfter) {
		t.Error("the metadata was updated for an archive that was never written")
	}
}

func TestForcedFullRereadProducesIdenticalBytes(t *testing.T) {
	c := corpus(t)
	s := newStore(t, c.Root)
	mustUpdate(t, s)
	before := archived(t, s)
	files := storeFiles(t, s)

	forced, err := Open(Options{Dir: s.Dir(), Root: c.Root, Strip: stubStrip, Resolve: stubResolve, Force: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	res := mustUpdate(t, forced)
	if res.FilesWhole != res.FilesSeen {
		t.Fatalf("Force read %d of %d files whole", res.FilesWhole, res.FilesSeen)
	}
	if !res.Wrote {
		t.Fatal("Force did not rewrite, so this proves nothing about the encoding")
	}
	if !bytes.Equal(before, archived(t, s)) {
		t.Fatal("re-reading the whole corpus changed the archive bytes")
	}
	sameFiles(t, "forced full re-read", files, storeFiles(t, s))
}

// a9 in full: consecutive runs that each rewrite the whole store produce
// byte-identical content in every file, not only in the tier files. The corpus
// is static here, so any difference is serialization and not new data.
func TestConsecutiveRewritesProduceIdenticalFiles(t *testing.T) {
	c := corpus(t)
	dir := t.TempDir()
	prev := map[string]string{}
	for run := range 3 {
		s, err := Open(Options{Dir: dir, Root: c.Root, Strip: stubStrip, Resolve: stubResolve, Force: true})
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		res := mustUpdate(t, s)
		if !res.Wrote {
			t.Fatalf("run %d wrote nothing", run+1)
		}
		files := storeFiles(t, s)
		if run > 0 {
			sameFiles(t, fmt.Sprintf("run %d against run %d", run+1, run), prev, files)
		}
		prev = files
	}
}

// The record order has to be a function of the turns themselves, not of the
// order the files were read in, or a corpus archived incrementally and the same
// corpus archived cold would disagree.
func TestIncrementalBuildMatchesColdBuildByte(t *testing.T) {
	c := corpus(t)
	path := c.Path(fixtures.FileRemoteless)
	full, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	head := full[:strings.Index(string(full), "\n")+1]
	if err := os.WriteFile(path, head, 0o644); err != nil {
		t.Fatalf("truncate %s: %v", path, err)
	}

	incremental := newStore(t, c.Root)
	mustUpdate(t, incremental)

	if err := os.WriteFile(path, full, 0o644); err != nil {
		t.Fatalf("restore %s: %v", path, err)
	}
	res := mustUpdate(t, incremental)
	if res.FilesAppended != 1 {
		t.Fatalf("resumed %d files, want 1", res.FilesAppended)
	}

	cold := newStore(t, c.Root)
	mustUpdate(t, cold)

	if !bytes.Equal(archived(t, incremental), archived(t, cold)) {
		t.Fatal("an incrementally built archive differs byte for byte from a cold one")
	}
	sameFiles(t, "incremental against cold", storeFiles(t, cold), storeFiles(t, incremental))
}
