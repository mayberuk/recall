package archive

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/mayberuk/recall/internal/schema"
)

func TestVerifyPassesOnACleanArchive(t *testing.T) {
	c := corpus(t)
	s := newStore(t, c.Root)
	res := mustUpdate(t, s)

	rep, err := s.Verify()
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !rep.OK {
		t.Fatalf("Verify reported problems on a clean archive: %v", rep.Problems)
	}
	if len(rep.Tiers) != len(tierFiles) {
		t.Fatalf("Verify covered %d tiers, the archive has %d", len(rep.Tiers), len(tierFiles))
	}
	for _, tr := range rep.Tiers {
		if tr.Checksum != tr.Expected || tr.Checksum == "" {
			t.Errorf("%s tier checksum %q does not match the recorded %q", tr.Tier, tr.Checksum, tr.Expected)
		}
	}
	if rep.Turns != res.Coverage.Turns {
		t.Errorf("Verify counted %d turns, the update recorded %d", rep.Turns, res.Coverage.Turns)
	}
	if rep.Sessions != res.Coverage.Sessions {
		t.Errorf("Verify counted %d sessions, the update recorded %d", rep.Sessions, res.Coverage.Sessions)
	}
}

func TestVerifyDetectsACorruptedArchive(t *testing.T) {
	c := corpus(t)
	s := newStore(t, c.Root)
	mustUpdate(t, s)

	blob := tierBytes(t, s, schema.TierConversation)
	blob[len(blob)/2] ^= 0xff
	if err := os.WriteFile(s.TierPath(schema.TierConversation), blob, 0o644); err != nil {
		t.Fatalf("corrupt the archive: %v", err)
	}

	rep, err := s.Verify()
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if rep.OK {
		t.Fatal("Verify passed an archive with a flipped byte")
	}
	if !hasProblem(rep, "checksum") {
		t.Errorf("problems %v never mention the checksum", rep.Problems)
	}
}

func TestVerifyDetectsATruncatedArchive(t *testing.T) {
	c := corpus(t)
	s := newStore(t, c.Root)
	mustUpdate(t, s)

	blob := tierBytes(t, s, schema.TierConversation)
	if err := os.WriteFile(s.TierPath(schema.TierConversation), blob[:len(blob)/2], 0o644); err != nil {
		t.Fatalf("truncate the archive: %v", err)
	}

	rep, err := s.Verify()
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if rep.OK {
		t.Fatal("Verify passed a truncated archive")
	}
}

// meta.json carries the tier checksums and both coverage boundaries. Before the
// sidecar it was the one file a corrupt copy of which still reported ok.
func TestVerifyDetectsCorruptMetadata(t *testing.T) {
	c := corpus(t)
	s := newStore(t, c.Root)
	res := mustUpdate(t, s)

	// The corruption has to leave meta.json valid JSON and leave every field
	// something else cross-checks alone, or the test cannot tell the checksum
	// from the parser. A live-file count that quietly gained ten is the shape
	// that matters: it parses, it misstates coverage, and nothing else notices.
	data, err := os.ReadFile(s.MetaPath())
	if err != nil {
		t.Fatalf("read the metadata: %v", err)
	}
	key := []byte(`"live_files": `)
	at := bytes.Index(data, key) + len(key)
	if at < len(key) || data[at] < '0' || data[at] > '8' {
		t.Fatalf("cannot find a live_files digit to alter in %s", s.MetaPath())
	}
	data[at]++
	if err := os.WriteFile(s.MetaPath(), data, 0o644); err != nil {
		t.Fatalf("corrupt the metadata: %v", err)
	}
	if m, ok := s.loadMeta(); !ok {
		t.Fatal("the corrupted metadata no longer parses, so this proves nothing about the checksum")
	} else if m.LiveFiles == res.Coverage.LiveFiles {
		t.Fatal("the corruption did not change what the metadata claims")
	}

	rep, err := s.Verify()
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if rep.OK || rep.MetaOK {
		t.Fatalf("Verify passed corrupt metadata (OK=%v MetaOK=%v): %v", rep.OK, rep.MetaOK, rep.Problems)
	}
}

func TestVerifyDetectsACorruptCursor(t *testing.T) {
	c := corpus(t)
	s := newStore(t, c.Root)
	mustUpdate(t, s)

	data, err := os.ReadFile(s.CursorPath())
	if err != nil {
		t.Fatalf("read the cursor: %v", err)
	}
	if err := os.WriteFile(s.CursorPath(), append(data, []byte("extra.jsonl:7\n")...), 0o644); err != nil {
		t.Fatalf("corrupt the cursor: %v", err)
	}

	rep, err := s.Verify()
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if rep.OK || rep.CursorOK {
		t.Fatalf("Verify passed a rewritten cursor (OK=%v CursorOK=%v): %v", rep.OK, rep.CursorOK, rep.Problems)
	}
}

func TestVerifyReportsMissingChecksums(t *testing.T) {
	c := corpus(t)
	s := newStore(t, c.Root)
	mustUpdate(t, s)

	if err := os.Remove(s.ChecksumsPath()); err != nil {
		t.Fatalf("remove the checksums: %v", err)
	}
	rep, err := s.Verify()
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if rep.OK {
		t.Fatal("Verify passed a store that cannot show its own integrity")
	}

	// A store whose integrity cannot be shown is rebuilt rather than trusted.
	res := mustUpdate(t, s)
	if !res.Rebuilt {
		t.Error("a missing checksums file did not force a rebuild")
	}
	after, err := s.Verify()
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !after.OK {
		t.Errorf("Verify still reports problems after the rebuild: %v", after.Problems)
	}
}

func TestVerifyReportsMissingMetadata(t *testing.T) {
	c := corpus(t)
	s := newStore(t, c.Root)
	mustUpdate(t, s)

	if err := os.Remove(s.MetaPath()); err != nil {
		t.Fatalf("remove the metadata: %v", err)
	}
	rep, err := s.Verify()
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if rep.OK {
		t.Fatal("Verify passed an archive with no metadata")
	}
}

// A truncated archive must not cost turns whose source files are still there:
// the next update notices, re-reads everything, and Verify goes clean again.
func TestUpdateRebuildsAfterTruncation(t *testing.T) {
	c := corpus(t)
	s := newStore(t, c.Root)
	mustUpdate(t, s)
	before := len(mustTurns(t, s))

	blob := tierBytes(t, s, schema.TierConversation)
	if err := os.WriteFile(s.TierPath(schema.TierConversation), blob[:len(blob)/2], 0o644); err != nil {
		t.Fatalf("truncate the archive: %v", err)
	}

	res := mustUpdate(t, s)
	if !res.Rebuilt {
		t.Error("a truncated archive did not force a rebuild")
	}
	if after := len(mustTurns(t, s)); after != before {
		t.Errorf("archive holds %d turns after the rebuild, held %d before", after, before)
	}
	rep, err := s.Verify()
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !rep.OK {
		t.Errorf("Verify still reports problems after the rebuild: %v", rep.Problems)
	}
}

// TestVerifyReportsADeletedTierAsUnreadable is the corruption Verify exists to
// catch once the raw transcripts have aged out: a tier the metadata says
// exists but the filesystem does not have is not silently "0 turns", it is a
// named problem.
func TestVerifyReportsADeletedTierAsUnreadable(t *testing.T) {
	c := corpus(t)
	s := newStore(t, c.Root)
	mustUpdate(t, s)

	if err := os.Remove(s.TierPath(schema.TierConversation)); err != nil {
		t.Fatalf("remove the conversation tier: %v", err)
	}

	rep, err := s.Verify()
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if rep.OK {
		t.Fatal("Verify passed a store missing a tier file")
	}
	if !hasProblem(rep, "is unreadable") {
		t.Errorf("problems %v never say the tier is unreadable", rep.Problems)
	}
}

// TestVerifyAcceptsATierRecordedAsNeverWritten is the other side of that
// check: metadata that honestly records zero bytes for a tier it never wrote
// is not corruption, so its absence must not be reported as unreadable.
func TestVerifyAcceptsATierRecordedAsNeverWritten(t *testing.T) {
	s := newStore(t, t.TempDir())
	m := meta{
		Version: formatVersion,
		Tiers: map[string]tierState{
			string(schema.TierConversation): {},
			string(schema.TierInvocation):   {},
			string(schema.TierResult):       {},
		},
	}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.MetaPath(), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.CursorPath(), []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.writeChecksums(); err != nil {
		t.Fatalf("writeChecksums: %v", err)
	}

	rep, err := s.Verify()
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if hasProblem(rep, "is unreadable") {
		t.Errorf("problems %v flag a tier metadata never claimed any bytes for", rep.Problems)
	}
}

// TestVerifyDetectsATurnCountThatDisagreesWithTheTierFile isolates the last
// per-tier cross-check: bytes and checksum still agree, only the recorded
// turn count does not match what the file actually frames.
func TestVerifyDetectsATurnCountThatDisagreesWithTheTierFile(t *testing.T) {
	c := corpus(t)
	s := newStore(t, c.Root)
	mustUpdate(t, s)

	m, ok := s.loadMeta()
	if !ok {
		t.Fatal("metadata did not load")
	}
	state := m.Tiers[string(schema.TierConversation)]
	state.Turns++
	m.Tiers[string(schema.TierConversation)] = state
	if err := s.writeMeta(m); err != nil {
		t.Fatalf("writeMeta: %v", err)
	}
	if err := s.writeChecksums(); err != nil {
		t.Fatalf("writeChecksums: %v", err)
	}

	rep, err := s.Verify()
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if rep.OK {
		t.Fatal("Verify passed metadata whose turn count disagrees with the tier file")
	}
	if !hasProblem(rep, "tier holds") {
		t.Errorf("problems %v never mention the turn count mismatch", rep.Problems)
	}
}

// TestVerifyReportsAnUnparseableCursor is the last check Verify runs: an
// unreadable checksum sidecar is a different failure from a cursor whose own
// mark-list syntax has broken, and doctor needs to name this one too.
func TestVerifyReportsAnUnparseableCursor(t *testing.T) {
	c := corpus(t)
	s := newStore(t, c.Root)
	mustUpdate(t, s)

	good, err := os.ReadFile(s.CursorPath())
	if err != nil {
		t.Fatalf("read the cursor: %v", err)
	}
	if err := os.WriteFile(s.CursorPath(), append(good, []byte("this is not a mark\n")...), 0o644); err != nil {
		t.Fatalf("corrupt the cursor: %v", err)
	}
	if err := s.writeChecksums(); err != nil {
		t.Fatalf("writeChecksums: %v", err)
	}

	rep, err := s.Verify()
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !hasProblem(rep, "does not parse") {
		t.Errorf("problems %v never say the cursor does not parse", rep.Problems)
	}
}

func hasProblem(rep Report, substr string) bool {
	for _, p := range rep.Problems {
		if strings.Contains(p, substr) {
			return true
		}
	}
	return false
}
