package archive

import (
	"encoding/binary"
	"os"
	"testing"
	"time"

	"github.com/mayberuk/recall/internal/schema"
)

func TestLoadMetaRejectsInvalidJSON(t *testing.T) {
	s := newStore(t, t.TempDir())
	if err := os.WriteFile(s.MetaPath(), []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.loadMeta(); ok {
		t.Error("loadMeta accepted a file that is not JSON")
	}
}

// TestLoadMetaRejectsAStaleFormatVersion is the rebuild trigger for a format
// change: a meta.json this build did not write is treated the same as none.
func TestLoadMetaRejectsAStaleFormatVersion(t *testing.T) {
	s := newStore(t, t.TempDir())
	if err := os.WriteFile(s.MetaPath(), []byte(`{"version": 1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.loadMeta(); ok {
		t.Errorf("loadMeta accepted version 1 against formatVersion %d", formatVersion)
	}
}

func TestCoverageFailsWithoutMetadata(t *testing.T) {
	s := newStore(t, t.TempDir())
	if _, err := s.Coverage(); err == nil {
		t.Error("Coverage succeeded with no metadata on disk")
	}
}

func TestStampOfTheZeroTimeIsEmpty(t *testing.T) {
	if got := stamp(time.Time{}); got != "" {
		t.Errorf("stamp(zero time) = %q, want empty", got)
	}
}

func TestUnstampOfAnEmptyStringIsTheZeroTime(t *testing.T) {
	if got := unstamp(""); !got.IsZero() {
		t.Errorf("unstamp(\"\") = %s, want the zero time", got)
	}
}

func TestUnstampOfAnUnparseableStringIsTheZeroTime(t *testing.T) {
	if got := unstamp("not a timestamp"); !got.IsZero() {
		t.Errorf("unstamp of garbage = %s, want the zero time", got)
	}
}

// TestFramesStopAtATruncatedSeqVarint is num()'s failure branch: a buffer that
// ends mid-varint must stop the walk rather than read past the end.
func TestFramesStopAtATruncatedSeqVarint(t *testing.T) {
	e := entry{Turn: schema.Turn{Session: "s", UUID: "u", TS: "2026-08-09T10:00:00.000Z", Text: "x"}, Seq: 300}
	full := appendEntry(nil, e)

	// Seq is a multi-byte varint at 300 (0xac 0x02); cut the buffer so only the
	// first continuation byte survives.
	cut := len(full) - 1
	b := append([]byte(tierMagic), full[:cut]...)

	f, ok := openFrames(b)
	if !ok {
		t.Fatal("openFrames rejected a well-formed header")
	}
	if _, good := f.next(); good {
		t.Fatal("next() reported success over a truncated Seq varint")
	}
	if !f.done() {
		t.Error("a truncated frame did not mark the walk done")
	}
}

// TestTurnsSkipsATierAlreadyReadThroughItsFile covers the read[file] dedup in
// Turns: two selectors mapping to the same file must not double the result.
func TestTurnsSkipsATierAlreadyReadThroughItsFile(t *testing.T) {
	s := newStore(t, tinyCorpus(t, map[string]string{
		"-p/5d0b7c46-8d05-4e93-a712-00000000000f.jsonl": grownRecord(
			"5d0b7c46-8d05-4e93-a712-00000000000f", "5d0b7c46-0000-4000-8000-000000000001",
			"2026-08-09T10:00:00.000Z", "alpha"),
	}))
	mustUpdate(t, s)

	once, err := s.Turns(schema.TierConversation)
	if err != nil {
		t.Fatalf("Turns(conversation): %v", err)
	}
	twice, err := s.Turns(schema.TierConversation, schema.TierConversation)
	if err != nil {
		t.Fatalf("Turns(conversation, conversation): %v", err)
	}
	if len(twice) != len(once) {
		t.Errorf("naming the same tier twice returned %d turns, want %d", len(twice), len(once))
	}
}

// TestEncodeTierDropsAnAdjacentByteIdenticalEntry is the guard against a
// whole-file re-read adding a second copy: the same entry appearing twice in
// a row after sorting is one turn, not two.
func TestEncodeTierDropsAnAdjacentByteIdenticalEntry(t *testing.T) {
	e := entry{Turn: schema.Turn{Session: "s", UUID: "u", TS: "2026-08-09T10:00:00.000Z", Text: "repeated"}, Seq: 0}

	blob, kept := encodeTier([]entry{e, e})
	if kept != 1 {
		t.Fatalf("encodeTier kept %d entries for two identical ones, want 1", kept)
	}
	frames, ok := openFrames(blob)
	if !ok {
		t.Fatal("openFrames rejected the encoded blob")
	}
	var got []entry
	for !frames.done() {
		next, good := frames.next()
		if !good {
			t.Fatal("frames.next() failed decoding the encoded blob")
		}
		got = append(got, next)
	}
	if len(got) != 1 {
		t.Fatalf("decoded %d entries from the blob, want 1", len(got))
	}
}

// TestEncodeTierKeepsTwoEntriesThatDifferOnlyInSeq is the other half of the
// adjacent-duplicate rule: two turns from the same record are not the same
// entry, so the position they were stripped at must survive the dedup.
func TestEncodeTierKeepsTwoEntriesThatDifferOnlyInSeq(t *testing.T) {
	base := entry{Turn: schema.Turn{Session: "s", UUID: "u", TS: "2026-08-09T10:00:00.000Z", Text: "thinking then prose"}}
	first := base
	first.Seq = 0
	second := base
	second.Seq = 1

	_, kept := encodeTier([]entry{first, second})
	if kept != 2 {
		t.Errorf("encodeTier kept %d entries that differ only in Seq, want 2", kept)
	}
}

func TestWriteMetaFailsWhenTheMetaPathIsADirectory(t *testing.T) {
	s := newStore(t, t.TempDir())
	if err := os.MkdirAll(s.MetaPath(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := s.writeMeta(meta{Version: formatVersion}); err == nil {
		t.Error("writeMeta succeeded writing over a directory")
	}
}

func TestChecksumsRejectsAMalformedLine(t *testing.T) {
	s := newStore(t, t.TempDir())
	if err := os.WriteFile(s.CursorPath(), []byte("cursor\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.MetaPath(), []byte("meta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.writeChecksums(); err != nil {
		t.Fatalf("writeChecksums: %v", err)
	}

	if err := os.WriteFile(s.ChecksumsPath(), []byte("not-a-valid-checksum-line\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.checksums(); ok {
		t.Error("checksums accepted a line with no valid digest")
	}
}

func TestWriteChecksumsFailsWhenASidecarFileIsMissing(t *testing.T) {
	s := newStore(t, t.TempDir())
	if err := os.WriteFile(s.MetaPath(), []byte("meta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The cursor is absent, so digesting the sidecar set must fail before any
	// bytes are written.
	if err := s.writeChecksums(); err == nil {
		t.Error("writeChecksums succeeded with a missing sidecar file")
	}
}

// TestSeq300EncodesAsMoreThanOneByte is a sanity check the truncation test
// above depends on: Seq 300 must actually take more than one byte, or cutting
// one byte would not produce a truncated varint at all.
func TestSeq300EncodesAsMoreThanOneByte(t *testing.T) {
	var buf []byte
	buf = binary.AppendUvarint(buf, 300)
	if len(buf) < 2 {
		t.Fatalf("uvarint(300) is %d bytes, want at least 2 for this test to cut mid-varint", len(buf))
	}
}
