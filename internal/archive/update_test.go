package archive

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mayberuk/recall/internal/fixtures"
	"github.com/mayberuk/recall/internal/jsonl"
	"github.com/mayberuk/recall/internal/repo"
	"github.com/mayberuk/recall/internal/schema"
)

func grownRecord(session, uuid, ts, text string) string {
	return fmt.Sprintf(`{"parentUuid":null,"isSidechain":false,"type":"assistant","uuid":%q,`+
		`"timestamp":%q,"cwd":"/nowhere/normal","sessionId":%q,`+
		`"message":{"role":"assistant","content":[{"type":"text","text":%q}]},`+
		`"version":"2.1.231","gitBranch":"main"}`, uuid, ts, session, text)
}

func TestColdPassCoversEverySessionInTheCorpus(t *testing.T) {
	c := corpus(t)
	s := newStore(t, c.Root)
	res := mustUpdate(t, s)

	wantFiles := c.Manifest.SessionFiles + len(c.Manifest.SubagentDirs)
	if res.FilesSeen != wantFiles {
		t.Errorf("saw %d files, the corpus plants %d", res.FilesSeen, wantFiles)
	}
	if res.Coverage.Sessions != len(c.Manifest.Sessions) {
		t.Errorf("archived %d sessions, manifest plants %d", res.Coverage.Sessions, len(c.Manifest.Sessions))
	}

	sessions := map[string]bool{}
	for _, turn := range mustTurns(t, s) {
		sessions[turn.Session] = true
	}
	for _, want := range c.Manifest.Sessions {
		if !sessions[want] {
			t.Errorf("session %s is not in the archive", want)
		}
	}
}

// A file is not a session: the multi-session fixture carries two, and both have
// to reach the archive.
func TestMultiSessionFileYieldsBothSessions(t *testing.T) {
	c := corpus(t)
	s := newStore(t, c.Root)
	mustUpdate(t, s)

	found := map[string]int{}
	for _, turn := range mustTurns(t, s) {
		found[turn.Session]++
	}
	for _, want := range c.Manifest.MultiSessionIDs {
		if found[want] == 0 {
			t.Errorf("session %s from %s is missing", want, c.Manifest.MultiSessionFile)
		}
	}
}

// The archive fills Turn.Repo from the injected resolver; internal/strip fills
// every other field. Nothing downstream can scope a query to a repo if that
// assignment stops happening, and a1-original-failure is exactly that query.
func TestArchivedTurnsCarryTheResolvedRepo(t *testing.T) {
	c := corpus(t)
	s := newStore(t, c.Root)
	mustUpdate(t, s)

	found := map[string]map[string]bool{}
	for _, turn := range mustTurns(t, s) {
		if found[turn.CWD] == nil {
			found[turn.CWD] = map[string]bool{}
		}
		found[turn.CWD][turn.Repo] = true
	}

	distinct := map[string]bool{}
	for _, shape := range c.Manifest.CWDShapes {
		want := stubResolve(shape.CWD)
		got, ok := found[shape.CWD]
		if !ok {
			t.Errorf("%s: no archived turn carries cwd %s", shape.Name, shape.CWD)
			continue
		}
		if len(got) != 1 || !got[want] {
			t.Errorf("%s: turns with cwd %s carry repo %v, want only %q",
				shape.Name, shape.CWD, slices.Sorted(maps.Keys(got)), want)
		}
		distinct[want] = true
	}
	if len(distinct) < 2 {
		t.Errorf("every cwd resolved to the same repo %v; the resolver's output is not reaching the archive",
			slices.Sorted(maps.Keys(distinct)))
	}
}

// 947 of 1,077 real files are subagent transcripts, and a decision reached
// inside one belongs to the session that spawned it.
func TestSubagentTranscriptIsArchivedUnderItsParentSession(t *testing.T) {
	c := corpus(t)
	s := newStore(t, c.Root)
	mustUpdate(t, s)

	var carriers []string
	for _, turn := range mustTurns(t, s) {
		if strings.Contains(turn.Text, fixtures.NeedleSubagent) {
			carriers = append(carriers, turn.Session)
		}
	}
	if len(carriers) != 1 {
		t.Fatalf("%q reached the archive in %d turns, want 1", fixtures.NeedleSubagent, len(carriers))
	}
	if carriers[0] != fixtures.SessNeedle {
		t.Errorf("%q is filed under session %s, want the parent %s",
			fixtures.NeedleSubagent, carriers[0], fixtures.SessNeedle)
	}
}

// EACCES is the other half of the disappearing-source finding: a file that
// cannot be opened must be named, and must be tried again rather than left out
// of the archive for good.
func TestUnreadableFileIsReportedAndReadOnTheNextPass(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root, which ignores the file mode")
	}
	c := corpus(t)
	path := c.Path(fixtures.FileRemoteless)
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatalf("chmod %s: %v", path, err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o644) })

	s := newStore(t, c.Root)
	res := mustUpdate(t, s)
	if !slices.Contains(res.Unreadable, fixtures.FileRemoteless) {
		t.Fatalf("Unreadable = %v, want it to name %s", res.Unreadable, fixtures.FileRemoteless)
	}
	if len(res.Vanished) != 0 {
		t.Errorf("Vanished = %v; a file that exists but cannot be opened is not a vanished one", res.Vanished)
	}
	if holds(mustTurns(t, s), fixtures.NeedleRemoteless) {
		t.Fatalf("%q was archived from a file that could not be opened", fixtures.NeedleRemoteless)
	}

	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("chmod %s: %v", path, err)
	}
	next := mustUpdate(t, s)
	if next.FilesWhole+next.FilesAppended == 0 {
		t.Fatal("the unreadable file was skipped on the next pass; its mark must not claim it was read")
	}
	if !holds(mustTurns(t, s), fixtures.NeedleRemoteless) {
		t.Errorf("%q never reached the archive after the file became readable", fixtures.NeedleRemoteless)
	}
}

func holds(turns []schema.Turn, token string) bool {
	for _, turn := range turns {
		if strings.Contains(turn.Text, token) {
			return true
		}
	}
	return false
}

func TestDedupByRecordUUID(t *testing.T) {
	c := corpus(t)
	s := newStore(t, c.Root)
	res := mustUpdate(t, s)

	turns := mustTurns(t, s)
	present := map[string]bool{}
	for _, turn := range turns {
		present[turn.UUID] = true
	}
	for _, dup := range c.Manifest.DupUUIDs {
		if !present[dup.UUID] {
			t.Errorf("uuid %s carried by %v was dropped entirely", dup.UUID, dup.Files)
		}
	}

	entries, ok := s.loadEntries()
	if !ok {
		t.Fatal("archive did not load")
	}
	seen := map[string]bool{}
	for _, e := range entries {
		if e.UUID == "" {
			continue
		}
		key := fmt.Sprintf("%s#%s#%d", e.Session, e.UUID, e.Seq)
		if seen[key] {
			t.Errorf("session %s uuid %s turn %d is archived more than once", e.Session, e.UUID, e.Seq)
		}
		seen[key] = true
	}

	// Downstream sees a clean archive and cannot tell dedup did anything, so the
	// count of collapsed copies is reported rather than left implicit.
	if res.Collapsed != len(c.Manifest.DupUUIDs) {
		t.Errorf("collapsed %d copies, the manifest plants %d duplicated records",
			res.Collapsed, len(c.Manifest.DupUUIDs))
	}

	hits := 0
	for _, turn := range turns {
		if strings.Contains(turn.Text, fixtures.NeedleDuplicated) {
			hits++
		}
	}
	if hits != 1 {
		t.Errorf("%q appears in %d archived turns; it sits on one uuid that two files carry, so it must appear once",
			fixtures.NeedleDuplicated, hits)
	}
}

// `doctor` gets the corpus-wide duplicate count by re-reading everything with
// Force, so a forced pass over an already-built archive has to report the same
// figure a cold build did — not "every record I re-read was already held".
func TestForcedPassReportsTheSameCollapsedCountAsAColdBuild(t *testing.T) {
	c := corpus(t)
	s := newStore(t, c.Root)
	cold := mustUpdate(t, s)
	if cold.Collapsed == 0 {
		t.Fatal("the cold build collapsed nothing, so this proves nothing")
	}

	forced, err := Open(Options{Dir: s.Dir(), Root: c.Root, Provider: claudeCodeStub{}, Resolve: stubResolve, Force: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	res := mustUpdate(t, forced)
	if res.FilesWhole != res.FilesSeen {
		t.Fatalf("Force read %d of %d files whole", res.FilesWhole, res.FilesSeen)
	}
	if res.Collapsed != cold.Collapsed {
		t.Errorf("forced pass collapsed %d, the cold build collapsed %d; re-reading a record already ingested is the same copy twice, not a duplicate",
			res.Collapsed, cold.Collapsed)
	}
	if res.TurnsAdded != 0 {
		t.Errorf("forced pass added %d turns to an already-complete archive", res.TurnsAdded)
	}
}

// Dedup is keyed on the session as well as the uuid, and the two directions pull
// against each other: a resumed session copies a record into a second file and
// must collapse, while a fork copies it into a second session and must not.
func TestDedupKeyIsSessionAndUUID(t *testing.T) {
	const (
		uuid  = "9c1f0aa2-0000-4000-8000-00000000dead"
		sessA = "1a0b7c46-8d05-4e93-a712-000000000001"
		sessB = "2b0b7c46-8d05-4e93-a712-000000000002"
		text  = "the conclusion both copies carry"
		ts    = "2026-08-09T10:00:00.000Z"
	)

	t.Run("one session in two files collapses", func(t *testing.T) {
		root := tinyCorpus(t, map[string]string{
			"-p-one/" + sessA + ".jsonl": grownRecord(sessA, uuid, ts, text),
			"-p-two/" + sessA + ".jsonl": grownRecord(sessA, uuid, ts, text),
		})
		s := newStore(t, root)
		res := mustUpdate(t, s)
		if got := carriers(mustTurns(t, s), text); len(got) != 1 {
			t.Fatalf("the same record in two files of one session was archived %d times, want 1: %v", len(got), got)
		}
		if res.Collapsed != 1 {
			t.Errorf("Collapsed = %d, want the one duplicate copy", res.Collapsed)
		}
	})

	t.Run("two sessions keep their own copy", func(t *testing.T) {
		root := tinyCorpus(t, map[string]string{
			"-p-one/" + sessA + ".jsonl": grownRecord(sessA, uuid, ts, text),
			"-p-one/" + sessB + ".jsonl": grownRecord(sessB, uuid, ts, text),
		})
		s := newStore(t, root)
		res := mustUpdate(t, s)

		got := carriers(mustTurns(t, s), text)
		slices.Sort(got)
		want := []string{sessA, sessB}
		if !slices.Equal(got, want) {
			t.Fatalf("a forked record is filed under %v, want both %v; the session it was deleted from reports fewer hits than it holds", got, want)
		}
		if res.Coverage.Sessions != 2 {
			t.Errorf("archived %d sessions, want 2", res.Coverage.Sessions)
		}
		if res.Collapsed != 0 {
			t.Errorf("Collapsed = %d; a fork into a second session is not a duplicate copy", res.Collapsed)
		}
	})
}

func carriers(turns []schema.Turn, token string) []string {
	var out []string
	for _, turn := range turns {
		if strings.Contains(turn.Text, token) {
			out = append(out, turn.Session)
		}
	}
	return out
}

func TestUnchangedCorpusSkipsEveryFileAndWritesNothing(t *testing.T) {
	c := corpus(t)
	s := newStore(t, c.Root)
	mustUpdate(t, s)

	res := mustUpdate(t, s)
	if res.FilesSkipped != res.FilesSeen {
		t.Errorf("skipped %d of %d files; nothing changed", res.FilesSkipped, res.FilesSeen)
	}
	if res.FilesWhole != 0 || res.FilesAppended != 0 {
		t.Errorf("re-read %d whole and %d partial files; nothing changed", res.FilesWhole, res.FilesAppended)
	}
	if res.TurnsAdded != 0 {
		t.Errorf("added %d turns; nothing changed", res.TurnsAdded)
	}
	if res.Wrote {
		t.Error("wrote the store when nothing changed")
	}
}

func TestGrownFileResumesFromItsMark(t *testing.T) {
	c := corpus(t)
	s := newStore(t, c.Root)
	mustUpdate(t, s)
	before := len(mustTurns(t, s))

	appendRecord(t, c.Path(fixtures.FileRemoteless),
		grownRecord(fixtures.SessRemoteless, "eeeeeeee-0000-4000-8000-000000000009",
			"2026-08-12T20:00:00.000Z", "appended after the first pass"))

	res := mustUpdate(t, s)
	if res.FilesAppended != 1 {
		t.Errorf("resumed %d files, want 1", res.FilesAppended)
	}
	if res.FilesWhole != 0 {
		t.Errorf("re-read %d files whole; only one file grew", res.FilesWhole)
	}
	if res.RecordsRead != 1 {
		t.Errorf("read %d records; resuming from the mark should read only the appended one", res.RecordsRead)
	}
	if res.TurnsAdded != 1 {
		t.Errorf("added %d turns, want 1", res.TurnsAdded)
	}
	if got := len(mustTurns(t, s)); got != before+1 {
		t.Errorf("archive holds %d turns, want %d", got, before+1)
	}
}

func TestShrunkFileIsReadWholeAndLosesNothing(t *testing.T) {
	c := corpus(t)
	s := newStore(t, c.Root)
	mustUpdate(t, s)
	before := len(mustTurns(t, s))

	path := c.Path(fixtures.FileRemoteless)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	cut := strings.Index(string(body), "\n") + 1
	if err := os.WriteFile(path, body[:cut], 0o644); err != nil {
		t.Fatalf("truncate %s: %v", path, err)
	}

	res := mustUpdate(t, s)
	if res.FilesWhole != 1 || res.FilesAppended != 0 {
		t.Errorf("shrunk file: read %d whole and %d resumed, want 1 and 0", res.FilesWhole, res.FilesAppended)
	}
	if got := len(mustTurns(t, s)); got != before {
		t.Errorf("archive holds %d turns after a source shrank, held %d; the archive never expires", got, before)
	}
}

func TestTouchedFileWithoutGrowthIsReadWhole(t *testing.T) {
	c := corpus(t)
	s := newStore(t, c.Root)
	mustUpdate(t, s)

	path := c.Path(fixtures.FileRemoteless)
	when := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatalf("chtimes %s: %v", path, err)
	}

	res := mustUpdate(t, s)
	if res.FilesWhole != 1 {
		t.Errorf("read %d files whole; a file whose mtime moved without growing must be re-read", res.FilesWhole)
	}
	if res.FilesSkipped != res.FilesSeen-1 {
		t.Errorf("skipped %d of %d files, want %d", res.FilesSkipped, res.FilesSeen, res.FilesSeen-1)
	}
	if res.TurnsAdded != 0 {
		t.Errorf("a re-read of unchanged content added %d turns", res.TurnsAdded)
	}
}

// Claude Code's cleanup runs at startup and races the archiver. ENOENT is
// otherwise indistinguishable from "never existed", which is a silently
// incomplete archive.
func TestFileVanishingBetweenTheWalkAndTheStatIsReported(t *testing.T) {
	c := corpus(t)
	s := newStore(t, c.Root)
	gone := c.Path(fixtures.FileRemoteless)
	s.onListed = func([]string) {
		if err := os.Remove(gone); err != nil {
			t.Errorf("remove %s: %v", gone, err)
		}
	}

	res := mustUpdate(t, s)
	if len(res.Vanished) != 1 || res.Vanished[0] != fixtures.FileRemoteless {
		t.Fatalf("Vanished = %v, want [%s]", res.Vanished, fixtures.FileRemoteless)
	}
	if res.FilesSeen != res.FilesWhole+res.FilesAppended+res.FilesSkipped+len(res.Vanished) {
		t.Errorf("%d files seen but %d accounted for", res.FilesSeen,
			res.FilesWhole+res.FilesAppended+res.FilesSkipped+len(res.Vanished))
	}
}

func TestFileVanishingBetweenTheStatAndTheOpenIsReported(t *testing.T) {
	c := corpus(t)
	s := newStore(t, c.Root)
	gone := c.Path(fixtures.FileRemoteless)
	s.onStatted = func(path string) {
		if path != gone {
			return
		}
		if err := os.Remove(gone); err != nil {
			t.Errorf("remove %s: %v", gone, err)
		}
	}

	res := mustUpdate(t, s)
	if len(res.Vanished) != 1 || res.Vanished[0] != fixtures.FileRemoteless {
		t.Fatalf("Vanished = %v, want [%s]", res.Vanished, fixtures.FileRemoteless)
	}
	marks, ok := s.loadCursor()
	if !ok {
		t.Fatal("cursor did not parse")
	}
	if _, stale := marks[fixtures.FileRemoteless]; stale {
		t.Error("a vanished file kept its cursor mark; if it reappears it must be read whole")
	}
}

// The live boundary and the content boundary are minima over different sets:
// mtimes of the files still on disk, and dates of the words in the archive.
func TestCoverageReportsBothBoundaries(t *testing.T) {
	c := corpus(t)
	s := newStore(t, c.Root)
	res := mustUpdate(t, s)

	wantLive, err := time.Parse(time.RFC3339, c.Manifest.SkewMTime)
	if err != nil {
		t.Fatalf("manifest skew mtime: %v", err)
	}
	wantContent, err := time.Parse(time.RFC3339, c.Manifest.SkewContentTS)
	if err != nil {
		t.Fatalf("manifest skew content ts: %v", err)
	}

	if !res.Coverage.LiveFrom.Equal(wantLive) {
		t.Errorf("LiveFrom = %s, want the oldest source mtime %s", res.Coverage.LiveFrom, wantLive)
	}
	if !res.Coverage.ContentFrom.Equal(wantContent) {
		t.Errorf("ContentFrom = %s, want the oldest content timestamp %s", res.Coverage.ContentFrom, wantContent)
	}
	if !res.Coverage.ReachesBeforeLive() {
		t.Errorf("the archive reaches to %s and the oldest live file to %s, so it does reach further back",
			res.Coverage.ContentFrom, res.Coverage.LiveFrom)
	}
	if res.Coverage.LiveFiles != res.FilesSeen {
		t.Errorf("LiveFiles = %d, saw %d files", res.Coverage.LiveFiles, res.FilesSeen)
	}

	stored, err := s.Coverage()
	if err != nil {
		t.Fatalf("Coverage: %v", err)
	}
	if !stored.LiveFrom.Equal(res.Coverage.LiveFrom) || !stored.ContentFrom.Equal(res.Coverage.ContentFrom) {
		t.Errorf("stored coverage %+v does not match the run's %+v", stored, res.Coverage)
	}
}

func TestReachesBeforeLiveIsFalseWithoutBothBoundaries(t *testing.T) {
	live := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	old := time.Date(2026, 6, 7, 0, 0, 0, 0, time.UTC)
	cases := map[string]struct {
		cov  Coverage
		want bool
	}{
		"content older than live":  {Coverage{LiveFrom: live, ContentFrom: old}, true},
		"content newer than live":  {Coverage{LiveFrom: old, ContentFrom: live}, false},
		"boundaries equal":         {Coverage{LiveFrom: live, ContentFrom: live}, false},
		"no archived content":      {Coverage{LiveFrom: live}, false},
		"no live files":            {Coverage{ContentFrom: old}, false},
		"nothing known either way": {Coverage{}, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := tc.cov.ReachesBeforeLive(); got != tc.want {
				t.Errorf("ReachesBeforeLive() = %v, want %v", got, tc.want)
			}
		})
	}
}

// The 55-day figure is per-file — one file's mtime against its own oldest
// record — and a subtraction of the two global boundaries cannot express it.
func TestPerFileSkewMeasuresOneFileAgainstItsOwnContent(t *testing.T) {
	c := corpus(t)
	s := newStore(t, c.Root)
	res := mustUpdate(t, s)

	m, ok := s.loadMeta()
	if !ok {
		t.Fatal("metadata did not load")
	}
	mtime, haveMTime := m.MTimes[c.Manifest.SkewFile]
	oldest, haveOldest := m.Oldest[c.Manifest.SkewFile]
	if !haveMTime || !haveOldest {
		t.Fatalf("no per-file record for %s: mtime %v, oldest %v", c.Manifest.SkewFile, haveMTime, haveOldest)
	}
	wantSkew := time.Duration(c.Manifest.SkewDays) * 24 * time.Hour
	if got := time.Duration(mtime - oldest); got != wantSkew {
		t.Errorf("%s is skewed by %v, manifest pins %d days", c.Manifest.SkewFile, got, c.Manifest.SkewDays)
	}

	if res.Coverage.MaxFileSkewDays() < c.Manifest.SkewDays {
		t.Errorf("MaxFileSkew is %d days; %s alone is skewed by %d",
			res.Coverage.MaxFileSkewDays(), c.Manifest.SkewFile, c.Manifest.SkewDays)
	}
	worst := time.Duration(m.MTimes[res.Coverage.MaxSkewFile] - m.Oldest[res.Coverage.MaxSkewFile])
	if worst != res.Coverage.MaxFileSkew {
		t.Errorf("MaxSkewFile %s is skewed by %v, MaxFileSkew reports %v",
			res.Coverage.MaxSkewFile, worst, res.Coverage.MaxFileSkew)
	}
}

// A resumed read only sees the bytes past the mark, so the file's own oldest
// record has to survive from the previous pass.
func TestPerFileSkewSurvivesAResumedRead(t *testing.T) {
	c := corpus(t)
	s := newStore(t, c.Root)
	mustUpdate(t, s)

	appendRecord(t, c.Path(fixtures.FileSkew),
		grownRecord(fixtures.SessSkew, "33333333-0000-4000-8000-000000000009",
			"2026-08-12T20:00:00.000Z", "appended long after the June content"))

	res := mustUpdate(t, s)
	if res.FilesAppended != 1 {
		t.Fatalf("resumed %d files, want 1", res.FilesAppended)
	}
	m, ok := s.loadMeta()
	if !ok {
		t.Fatal("metadata did not load")
	}
	want, err := time.Parse(time.RFC3339, c.Manifest.SkewContentTS)
	if err != nil {
		t.Fatalf("manifest skew content ts: %v", err)
	}
	if got := m.Oldest[c.Manifest.SkewFile]; got != want.UnixNano() {
		t.Errorf("oldest record for %s is %s after a resume, want %s",
			c.Manifest.SkewFile, time.Unix(0, got).UTC(), want)
	}
}

func TestUnknownRecordTypesAreCountedNotDropped(t *testing.T) {
	c := corpus(t)
	s := newStore(t, c.Root)
	res := mustUpdate(t, s)

	for typ, want := range c.Manifest.UnknownTypes {
		if got := res.Tally.Unknown[typ]; got != want {
			t.Errorf("tally for unknown type %s = %d, want %d", typ, got, want)
		}
	}
	if len(res.Tally.Unknown) != len(c.Manifest.UnknownTypes) {
		t.Errorf("tallied %d unknown types, manifest plants %d", len(res.Tally.Unknown), len(c.Manifest.UnknownTypes))
	}
}

// A transcript being appended to ends mid-record. The mark must stop before that
// line, or the record is skipped for good once it is complete.
func TestMarkStopsShortOfATornTrailingLine(t *testing.T) {
	c := corpus(t)
	s := newStore(t, c.Root)
	mustUpdate(t, s)

	path := c.Path(fixtures.FileRemoteless)
	complete := grownRecord(fixtures.SessRemoteless, "eeeeeeee-0000-4000-8000-00000000000a",
		"2026-08-12T21:00:00.000Z", "written in two halves")
	torn := complete[:len(complete)/2]
	appendRecord(t, path, torn)

	res := mustUpdate(t, s)
	if res.TurnsAdded != 0 {
		t.Errorf("a torn line produced %d turns", res.TurnsAdded)
	}
	marks, ok := s.loadCursor()
	if !ok {
		t.Fatal("cursor did not parse")
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if marks[fixtures.FileRemoteless] >= fi.Size() {
		t.Fatalf("mark %d is at or past the torn line at %d; the record would never be read",
			marks[fixtures.FileRemoteless], fi.Size())
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	rest := strings.TrimSuffix(string(body), torn+"\n")
	if err := os.WriteFile(path, []byte(rest), 0o644); err != nil {
		t.Fatalf("rewrite %s: %v", path, err)
	}
	appendRecord(t, path, complete)

	res = mustUpdate(t, s)
	if res.TurnsAdded != 1 {
		t.Fatalf("completing the record added %d turns, want 1", res.TurnsAdded)
	}
	found := false
	for _, turn := range mustTurns(t, s) {
		if turn.Text == "written in two halves" {
			found = true
		}
	}
	if !found {
		t.Error("the completed record never reached the archive")
	}
}

// TestListFailsWhenADirectoryCannotBeTraversed is the walk callback's non-
// vanished error path: a directory that exists but cannot be opened is a
// corpus-unreadable failure, not a silently empty listing.
func TestListFailsWhenADirectoryCannotBeTraversed(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root, which ignores the file mode")
	}
	root := t.TempDir()
	locked := filepath.Join(root, "-p-locked")
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(locked, "x.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatalf("chmod %s: %v", locked, err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	s := newStore(t, root)
	if _, err := s.list(); err == nil {
		t.Error("list succeeded over a directory it cannot open")
	}
	if _, err := s.Update(); err == nil {
		t.Error("Update succeeded though list cannot walk the corpus")
	}
}

// TestListToleratesACorpusRootThatDoesNotExist is the first-run shape: nothing
// has written under ~/.claude/projects yet, and that is an empty corpus, not
// a walk failure.
func TestListToleratesACorpusRootThatDoesNotExist(t *testing.T) {
	root := filepath.Join(t.TempDir(), "never-created")
	s := newStore(t, root)

	sources, err := s.list()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(sources) != 0 {
		t.Errorf("list found %d sources under a root that does not exist", len(sources))
	}

	res := mustUpdate(t, s)
	if res.FilesSeen != 0 {
		t.Errorf("FilesSeen = %d over a missing root, want 0", res.FilesSeen)
	}
}

// TestStatReportsAPermissionFailureDifferentlyFromAVanishedFile covers the
// other half of stat()'s error handling: a file that vanished between the
// walk and the stat is ENOENT, but one a directory permission now hides is a
// different failure and must not be filed as Vanished.
func TestStatReportsAPermissionFailureDifferentlyFromAVanishedFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root, which ignores the file mode")
	}
	const sess = "5d0b7c46-8d05-4e93-a712-00000000000f"
	root := tinyCorpus(t, map[string]string{
		"-p-locked/" + sess + ".jsonl": grownRecord(sess, "5d0b7c46-0000-4000-8000-000000000001",
			"2026-08-09T10:00:00.000Z", "alpha"),
	})
	locked := filepath.Join(root, "-p-locked")

	s := newStore(t, root)
	s.onListed = func([]string) {
		if err := os.Chmod(locked, 0o000); err != nil {
			t.Fatalf("chmod %s: %v", locked, err)
		}
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	res := mustUpdate(t, s)
	rel := "-p-locked/" + sess + ".jsonl"
	if !slices.Contains(res.Unreadable, rel) {
		t.Fatalf("Unreadable = %v, want it to name %s", res.Unreadable, rel)
	}
	if len(res.Vanished) != 0 {
		t.Errorf("Vanished = %v; a directory permission failure is not a vanished file", res.Vanished)
	}
}

// TestUpdateRebuildsWhenATierFileIsDeletedEntirely is archiveIntact's other
// corruption shape beyond truncation: the file is gone outright, which stat
// itself reports rather than a size mismatch.
func TestUpdateRebuildsWhenATierFileIsDeletedEntirely(t *testing.T) {
	c := corpus(t)
	s := newStore(t, c.Root)
	mustUpdate(t, s)
	before := len(mustTurns(t, s))

	if err := os.Remove(s.TierPath(schema.TierConversation)); err != nil {
		t.Fatalf("remove the conversation tier: %v", err)
	}

	res := mustUpdate(t, s)
	if !res.Rebuilt {
		t.Error("a deleted tier file did not force a rebuild")
	}
	if got := len(mustTurns(t, s)); got != before {
		t.Errorf("archive holds %d turns after the rebuild, held %d before", got, before)
	}
}

// TestArchiveIntactAcceptsATierNeverWritten is the continue branch archiveIntact
// takes for a tier a meta legitimately never recorded any bytes for: absent
// and zero-length agree, so it is not treated as corruption.
func TestArchiveIntactAcceptsATierNeverWritten(t *testing.T) {
	s := newStore(t, t.TempDir())
	if err := os.WriteFile(s.CursorPath(), []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.MetaPath(), []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.writeChecksums(); err != nil {
		t.Fatalf("writeChecksums: %v", err)
	}
	if !s.archiveIntact(meta{}) {
		t.Error("archiveIntact rejected a meta naming no tiers when no tier files exist either")
	}
}

// TestArchiveIntactDetectsACursorChangedBehindItsChecksum is sidecarAgrees'
// mismatch branch: the cursor still parses, so this is not the corrupt-cursor
// rebuild path — only its checksum disagrees with what is on disk now.
func TestArchiveIntactDetectsACursorChangedBehindItsChecksum(t *testing.T) {
	c := corpus(t)
	s := newStore(t, c.Root)
	mustUpdate(t, s)

	data, err := os.ReadFile(s.CursorPath())
	if err != nil {
		t.Fatalf("read the cursor: %v", err)
	}
	if err := os.WriteFile(s.CursorPath(), append(data, '\n'), 0o644); err != nil {
		t.Fatalf("append a blank line to the cursor: %v", err)
	}
	if _, ok := s.loadCursor(); !ok {
		t.Fatal("the cursor no longer parses; this test needs it to, to isolate the checksum mismatch")
	}

	m, ok := s.loadMeta()
	if !ok {
		t.Fatal("metadata did not load")
	}
	if s.archiveIntact(m) {
		t.Error("archiveIntact accepted a cursor whose bytes no longer match its recorded checksum")
	}
}

// TestCommitSkipsContentBoundsForAnUnparseableTimestamp is the one entry field
// commit does not trust blindly: a turn whose TS does not parse must not move
// either content boundary, rather than panic or silently normalize it.
func TestCommitSkipsContentBoundsForAnUnparseableTimestamp(t *testing.T) {
	s := newStore(t, t.TempDir())
	entries := []entry{{Turn: schema.Turn{Session: "s", UUID: "u", TS: "not-a-timestamp", Text: "x"}}}
	now := marksets{mark: map[string]int64{}, mtime: map[string]int64{}, oldest: map[string]int64{}}

	res, err := s.commit(Result{}, Coverage{}, meta{}, entries, now, true, map[schema.Tier]bool{schema.TierConversation: true})
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if !res.Coverage.ContentFrom.IsZero() || !res.Coverage.ContentTo.IsZero() {
		t.Errorf("ContentFrom=%s ContentTo=%s, want both zero for an entry whose timestamp does not parse",
			res.Coverage.ContentFrom, res.Coverage.ContentTo)
	}
}

// TestCommitStopsAtTheFirstWriteFailureAndWritesNothingAfterIt pins the
// documented write order: a failure at meta must leave the cursor and
// checksums untouched, or a partial write could claim turns the archive does
// not hold.
func TestCommitStopsAtTheFirstWriteFailureAndWritesNothingAfterIt(t *testing.T) {
	s := newStore(t, t.TempDir())
	if err := os.MkdirAll(s.MetaPath(), 0o755); err != nil {
		t.Fatal(err)
	}
	now := marksets{mark: map[string]int64{}, mtime: map[string]int64{}, oldest: map[string]int64{}}

	if _, err := s.commit(Result{}, Coverage{}, meta{}, nil, now, true, map[schema.Tier]bool{}); err == nil {
		t.Fatal("commit succeeded writing metadata over a directory")
	}
	if _, err := os.Stat(s.CursorPath()); err == nil {
		t.Error("the cursor was written though the metadata write failed first")
	}
	if _, err := os.Stat(s.ChecksumsPath()); err == nil {
		t.Error("the checksums were written though the metadata write failed first")
	}
}

// TestCommitStopsAtAWriteCursorFailure is the middle write in that order: a
// cursor failure must still leave the checksums file unwritten.
func TestCommitStopsAtAWriteCursorFailure(t *testing.T) {
	s := newStore(t, t.TempDir())
	if err := os.MkdirAll(s.CursorPath(), 0o755); err != nil {
		t.Fatal(err)
	}
	now := marksets{mark: map[string]int64{}, mtime: map[string]int64{}, oldest: map[string]int64{}}

	if _, err := s.commit(Result{}, Coverage{}, meta{}, nil, now, true, map[schema.Tier]bool{}); err == nil {
		t.Fatal("commit succeeded writing the cursor over a directory")
	}
	if _, err := os.Stat(s.ChecksumsPath()); err == nil {
		t.Error("the checksums were written though the cursor write failed")
	}
}

// TestCommitPropagatesAWriteChecksumsFailure is the last write in the order:
// its own failure must still surface as commit's error.
func TestCommitPropagatesAWriteChecksumsFailure(t *testing.T) {
	s := newStore(t, t.TempDir())
	if err := os.MkdirAll(s.ChecksumsPath(), 0o755); err != nil {
		t.Fatal(err)
	}
	now := marksets{mark: map[string]int64{}, mtime: map[string]int64{}, oldest: map[string]int64{}}

	if _, err := s.commit(Result{}, Coverage{}, meta{}, nil, now, true, map[schema.Tier]bool{}); err == nil {
		t.Fatal("commit succeeded writing the checksums over a directory")
	}
}

// TestSameMetaDetectsADifferingTiersMap is the one field sameMeta compares by
// deep equality rather than by value: two metas that agree on every scalar
// but disagree on a tier's recorded state are not the same.
func TestSameMetaDetectsADifferingTiersMap(t *testing.T) {
	a := meta{Version: formatVersion, Tiers: map[string]tierState{"conversation": {Bytes: 10, Turns: 1, Checksum: "x"}}}
	b := meta{Version: formatVersion, Tiers: map[string]tierState{"conversation": {Bytes: 20, Turns: 1, Checksum: "x"}}}
	if sameMeta(a, b) {
		t.Error("sameMeta reported two metas with different tier byte counts as the same")
	}
	c := a
	c.Tiers = map[string]tierState{"conversation": {Bytes: 10, Turns: 1, Checksum: "x"}}
	if !sameMeta(a, c) {
		t.Error("sameMeta reported two identical metas as different")
	}
}

func TestOpenRefusesWithoutTheInjectedFunctions(t *testing.T) {
	if _, err := Open(Options{Dir: t.TempDir(), Root: t.TempDir(), Resolve: stubResolve}); err == nil {
		t.Error("Open accepted a nil strip function")
	}
	if _, err := Open(Options{Dir: t.TempDir(), Root: t.TempDir(), Provider: claudeCodeStub{}}); err == nil {
		t.Error("Open accepted a nil repo resolver")
	}
}

// headDecoder is the shape a provider has when it files sessions by date: the
// session identity arrives in the file's first record and in no other, so a
// decoder that never sees that record cannot say whose session the rest of the
// file is.
type headDecoder struct {
	session string
}

func (d *headDecoder) Turns(rec jsonl.Record) ([]schema.Turn, bool) {
	if id := rec.SessionID(); id != "" {
		d.session = id
	}
	turns, ok := stubStrip(rec)
	if !ok {
		return nil, false
	}
	for i := range turns {
		turns[i].Session = d.session
	}
	return turns, true
}

// A resumed read starts at a byte cursor, so the identifying first record is
// behind it. The head is replayed into the decoder for context and the turns
// it yields are dropped: they were archived by the pass that first read the
// file, and keeping them would be a duplicate that only dedup catches.
func TestResumedReadPrimesTheDecoderWithoutRearchivingTheHead(t *testing.T) {
	const (
		session = "5d0b7c46-8d05-4e93-a712-00000000000f"
		rel     = "2026/08/09/rollout.jsonl"
	)
	// The head carries no uuid, so nothing dedups a second copy of it away:
	// the count below reports a re-archived head instead of hiding it.
	root := tinyCorpus(t, map[string]string{
		rel: strings.Join([]string{
			grownRecord(session, "", "2026-08-09T10:00:00.000Z", "head"),
			grownRecord("", "5d0b7c46-0000-4000-8000-000000000001", "2026-08-09T10:00:01.000Z", "alpha"),
		}, "\n"),
	})
	provider := stubbedProvider{
		agent: schema.AgentCodex,
		root:  root,
		head:  true,
		decode: func(string) Decoder {
			return &headDecoder{}
		},
	}
	s := providerStore(t, t.TempDir(), provider)
	if got := mustUpdate(t, s).TurnsAdded; got != 2 {
		t.Fatalf("the cold pass archived %d turns, want the head and the record after it", got)
	}

	appendRecord(t, filepath.Join(root, rel),
		grownRecord("", "5d0b7c46-0000-4000-8000-000000000002", "2026-08-09T10:00:02.000Z", "bravo"))
	res := mustUpdate(t, s)
	if res.FilesAppended != 1 {
		t.Fatalf("the second pass appended %d files and read %d whole; it must resume from the mark",
			res.FilesAppended, res.FilesWhole)
	}
	if res.TurnsAdded != 1 {
		t.Errorf("the resumed pass archived %d turns, want only the appended record; the primed head must not be archived again",
			res.TurnsAdded)
	}

	heads, bravo := 0, schema.Turn{}
	for _, turn := range mustTurns(t, s) {
		switch turn.Text {
		case "head":
			heads++
		case "bravo":
			bravo = turn
		}
	}
	if heads != 1 {
		t.Errorf("the archive holds %d copies of the head record's turn, want 1", heads)
	}
	if bravo.Session != session {
		t.Errorf("the appended record was archived under session %q, want %q from the file's head",
			bravo.Session, session)
	}
}

// NeedsHead is the gate that keeps a resumed read from opening and decoding a
// file's head all over again: a provider that answers false must never see
// its first record a second time once the read resumes past it.
func TestNeedsHeadFalseKeepsAResumedReadFromPrimingTheHead(t *testing.T) {
	const (
		session  = "5d0b7c46-8d05-4e93-a712-00000000000f"
		headUUID = "5d0b7c46-0000-4000-8000-00000000000a"
		rel      = "-p/" + session + ".jsonl"
	)
	root := tinyCorpus(t, map[string]string{
		rel: grownRecord(session, headUUID, "2026-08-09T10:00:00.000Z", "head"),
	})

	var mu sync.Mutex
	var sawHead bool
	provider := stubbedProvider{
		agent: schema.AgentCodex,
		root:  root,
		head:  false,
		decode: func(string) Decoder {
			return stripDecoder(func(rec jsonl.Record) ([]schema.Turn, bool) {
				if rec.UUID() == headUUID {
					mu.Lock()
					sawHead = true
					mu.Unlock()
				}
				return stubStrip(rec)
			})
		},
	}
	s := providerStore(t, t.TempDir(), provider)
	mustUpdate(t, s)

	mu.Lock()
	sawHead = false
	mu.Unlock()

	appendRecord(t, filepath.Join(root, rel),
		grownRecord(session, "5d0b7c46-0000-4000-8000-00000000000b", "2026-08-09T10:00:01.000Z", "alpha"))
	res := mustUpdate(t, s)
	if res.FilesAppended != 1 {
		t.Fatalf("the second pass appended %d files, want 1; it must resume rather than reread the whole file", res.FilesAppended)
	}

	mu.Lock()
	defer mu.Unlock()
	if sawHead {
		t.Error("the resumed read handed its decoder the head record even though NeedsHead is false")
	}
}

// relDecoder records which relative path it was built for, so a test can pin
// that Decoder is called per file rather than once for the whole store.
type relDecoder struct {
	rel string
}

func (d *relDecoder) Turns(rec jsonl.Record) ([]schema.Turn, bool) { return stubStrip(rec) }

// The per-file-decoder claim is otherwise unasserted: nothing pins that
// Decoder is called with each file's own relative path, or that two files
// don't end up sharing one decoder instance.
func TestUpdateBuildsADistinctDecoderPerFileNamedByItsRelativePath(t *testing.T) {
	const (
		sessA = "5d0b7c46-8d05-4e93-a712-00000000000a"
		sessB = "5d0b7c46-8d05-4e93-a712-00000000000b"
	)
	relA := "-p/" + sessA + ".jsonl"
	relB := "-p/" + sessB + ".jsonl"
	root := tinyCorpus(t, map[string]string{
		relA: grownRecord(sessA, "5d0b7c46-0000-4000-8000-00000000000a", "2026-08-09T10:00:00.000Z", "alpha"),
		relB: grownRecord(sessB, "5d0b7c46-0000-4000-8000-00000000000b", "2026-08-09T10:00:01.000Z", "bravo"),
	})

	var mu sync.Mutex
	var made []*relDecoder
	provider := stubbedProvider{
		agent: schema.AgentCodex,
		root:  root,
		decode: func(rel string) Decoder {
			d := &relDecoder{rel: rel}
			mu.Lock()
			made = append(made, d)
			mu.Unlock()
			return d
		},
	}
	s := providerStore(t, t.TempDir(), provider)
	mustUpdate(t, s)

	if len(made) != 2 {
		t.Fatalf("Decoder was called %d times, want one per file", len(made))
	}
	if made[0] == made[1] {
		t.Fatal("both files were decoded through the same decoder instance, want a distinct one per file")
	}
	gotRels := map[string]bool{made[0].rel: true, made[1].rel: true}
	if !gotRels[relA] || !gotRels[relB] {
		t.Errorf("decoders were built for %v, want %q and %q", gotRels, relA, relB)
	}
}

// A provider that reads the project identity out of the transcript knows more
// than a walk up from the cwd can, so the cwd resolver must not overwrite it.
func TestDecodedRepoIdentitySurvivesTheCWDResolver(t *testing.T) {
	const session = "5d0b7c46-8d05-4e93-a712-00000000000f"
	root := tinyCorpus(t, map[string]string{
		"-p/" + session + ".jsonl": strings.Join([]string{
			grownRecord(session, "5d0b7c46-0000-4000-8000-000000000001", "2026-08-09T10:00:00.000Z", "known"),
			grownRecord(session, "5d0b7c46-0000-4000-8000-000000000002", "2026-08-09T10:00:01.000Z", "unknown"),
		}, "\n"),
	})
	s := providerStore(t, t.TempDir(), stubbedProvider{
		agent: schema.AgentCodex,
		root:  root,
		decode: func(string) Decoder {
			return stripDecoder(func(rec jsonl.Record) ([]schema.Turn, bool) {
				turns, ok := stubStrip(rec)
				if !ok {
					return nil, false
				}
				for i := range turns {
					if turns[i].Text == "known" {
						turns[i].Repo = "decoded/identity"
					}
				}
				return turns, true
			})
		},
	})
	mustUpdate(t, s)

	want := map[string]string{"known": "decoded/identity", "unknown": stubResolve("/nowhere/normal")}
	for _, turn := range mustTurns(t, s) {
		if got := turn.Repo; got != want[turn.Text] {
			t.Errorf("turn %q carries repo %q, want %q", turn.Text, got, want[turn.Text])
		}
	}
}

// A provider owns its own layout, so what counts as a transcript is its answer
// and not a hardcoded extension: a sidecar beside a session file is not one.
func TestTheWalkAsksTheProviderWhichPathsAreTranscripts(t *testing.T) {
	const session = "5d0b7c46-8d05-4e93-a712-00000000000f"
	root := tinyCorpus(t, map[string]string{
		"2026/08/09/" + session + ".jsonl":       grownRecord(session, "5d0b7c46-0000-4000-8000-000000000001", "2026-08-09T10:00:00.000Z", "kept"),
		"2026/08/09/" + session + ".index.jsonl": grownRecord(session, "5d0b7c46-0000-4000-8000-000000000002", "2026-08-09T10:00:01.000Z", "skipped"),
	})
	s := providerStore(t, t.TempDir(), transcriptFilter{
		stubbedProvider: stubbedProvider{agent: schema.AgentCodex, root: root},
	})
	res := mustUpdate(t, s)

	if res.FilesSeen != 1 {
		t.Errorf("the walk saw %d files, want only the one the provider claims", res.FilesSeen)
	}
	if holds(mustTurns(t, s), "skipped") {
		t.Error("a path the provider does not call a transcript was archived anyway")
	}
}

// transcriptFilter is a provider whose layout keeps a sidecar next to each
// session file under the same extension.
type transcriptFilter struct {
	stubbedProvider
}

func (transcriptFilter) IsTranscript(rel string) bool {
	return strings.HasSuffix(rel, ".jsonl") && !strings.HasSuffix(rel, ".index.jsonl")
}

// frozenScratch stands in for the corpus's scratch root, which is a temporary
// directory and so differs on every run. It reaches the archive through every
// turn's cwd, and freezing it is what makes a digest over the archive bytes a
// constant a test can pin rather than a number recomputed from whatever the
// code happens to do today.
const frozenScratch = "/frozen/scratch"

func frozenCorpus(t *testing.T) string {
	t.Helper()
	c := corpus(t)
	root := filepath.Join(t.TempDir(), "frozen")
	from, to := []byte(c.Scratch), []byte(frozenScratch)
	err := filepath.WalkDir(c.Root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(c.Root, p)
		if err != nil {
			return err
		}
		target := filepath.Join(root, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, bytes.ReplaceAll(data, from, to), 0o644)
	})
	if err != nil {
		t.Fatalf("freeze the corpus: %v", err)
	}
	return root
}

// frozenTierDigests is what the fixture corpus frames to. The frames are not
// allowed to move on their own: an archive that no longer matches its own
// metadata rebuilds from a session store cleanup may already have emptied.
//
// Only the corpus growing may move these, and re-recording them is only honest
// once that has been shown to be the cause. The multi-tier record added to the
// needle session moved all three, because tieredStrip emits one turn per tier
// from every record; with that one record taken back out, the previous digests
// still held, which is what separates a corpus that grew from a frame that
// shifted.
var frozenTierDigests = map[schema.Tier]string{
	schema.TierConversation: "a06d04a1106b27876259a3f8210706e4d9feb7855f142dfdefd645fe38ba9b5d",
	schema.TierInvocation:   "affd2a5de8dcd5aef9d95bea36377cedc7f3f263534958c4f7c908684793408f",
	schema.TierResult:       "699caa6b58714f65ab4d0828a245430160edb463520c15ed08bfa0cee4cbe5af",
}

func TestFrozenCorpusFramesToTheRecordedBytes(t *testing.T) {
	s := storeWith(t, frozenCorpus(t), "", tieredStrip)
	mustUpdate(t, s)

	for _, tier := range tierFiles {
		sum := sha256.Sum256(tierBytes(t, s, tier))
		if got := hex.EncodeToString(sum[:]); got != frozenTierDigests[tier] {
			t.Errorf("%s tier digest = %s, want %s", tier, got, frozenTierDigests[tier])
		}
	}
}

// The archive's bytes are a function of the corpus, and the one input to them
// that is not a file on disk is the injected repo resolver: it shells out to
// git, so anything it answers inconsistently — under load, or between two
// processes — moves the archive without the corpus moving. Two cold builds in
// one run are what hold it to a fixed answer.
func TestTwoColdBuildsWithTheRealResolverAgree(t *testing.T) {
	c := corpus(t)
	build := func() *Store {
		s, err := Open(Options{Dir: t.TempDir(), Root: c.Root, Provider: claudeCodeStub{}, Resolve: repo.New().Repo})
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		mustUpdate(t, s)
		return s
	}

	first, second := build(), build()
	if !bytes.Equal(archived(t, first), archived(t, second)) {
		t.Error("two cold builds of one corpus produced different archive bytes")
	}
}
