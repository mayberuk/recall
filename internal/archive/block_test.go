package archive

import (
	"encoding/binary"
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/mayberuk/recall/internal/schema"
)

// shrinkBlocks cuts a block down to a size a test fixture can span several of.
// Every test here that means to reach the concurrent decode has to call it: at
// the shipping 256 KB a fixture cheap enough to build in a test is one block, the
// decode runs on the caller's goroutine, and the test proves nothing about the
// path it was written for.
func shrinkBlocks(t *testing.T, to int) {
	t.Helper()
	was := blockBytes
	blockBytes = to
	t.Cleanup(func() { blockBytes = was })
}

// blockFixture builds entries whose framed size spans many blocks, with text
// distinct per entry so a decode that mixed two turns up is visible.
func blockFixture(n int) []entry {
	out := make([]entry, n)
	for i := range out {
		out[i] = entry{Turn: schema.Turn{
			Session: fmt.Sprintf("session-%02d", i/7),
			UUID:    fmt.Sprintf("uuid-%05d", i),
			TS:      fmt.Sprintf("2026-08-09T10:%02d:%02d.000Z", i/60%60, i%60),
			Tier:    schema.TierConversation,
			Author:  schema.AuthorAssistant,
			Repo:    "example/repo",
			Branch:  "main",
			CWD:     "/checkout",
			Text:    fmt.Sprintf("turn %05d %s", i, strings.Repeat("payload ", 24)),
		}, Seq: i % 3}
	}
	return out
}

// decodeSequentially is the walk decodeBlocks replaces, for a test to compare
// against. It is the reference here for the same reason foldReference is the
// reference for the assembly: holding a decode to a faster decode lets a
// misreading they share pass both.
func decodeSequentially(t *testing.T, blob []byte) []schema.Turn {
	t.Helper()
	f, _, ok := openFrames(blob)
	if !ok {
		t.Fatal("openFrames rejected a blob encodeTier had just written")
	}
	var out []schema.Turn
	for !f.done() {
		out = append(out, schema.Turn{})
		f.turn(&out[len(out)-1])
		f.num()
		if f.bad {
			t.Fatal("the sequential walk hit a bad frame")
		}
	}
	return out
}

func TestBlockDecodeAgreesWithTheSequentialWalk(t *testing.T) {
	shrinkBlocks(t, 1024)
	entries := blockFixture(400)
	blob, kept := encodeTier(entries)
	if kept != len(entries) {
		t.Fatalf("encodeTier kept %d of %d entries", kept, len(entries))
	}

	_, marks, ok := openFrames(blob)
	if !ok {
		t.Fatal("openFrames rejected the encoded blob")
	}
	if len(marks) < 4 {
		t.Fatalf("the fixture framed into %d blocks, too few to decode concurrently", len(marks))
	}

	got, ok := decodeBlocks(blob, marks, nil)
	if !ok {
		t.Fatal("decodeBlocks declined a table it had just been given by openFrames")
	}
	want := decodeSequentially(t, blob)
	if len(got) != len(want) {
		t.Fatalf("the concurrent decode returned %d turns, the sequential walk %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("turn %d decoded as %+v, the sequential walk read %+v", i, got[i], want[i])
		}
	}
}

// TestBlockTableOffsetsAreFrameBoundaries is the property the whole scheme rests
// on: every offset names the first byte of a turn, and the turn counts add up to
// what was written.
func TestBlockTableOffsetsAreFrameBoundaries(t *testing.T) {
	shrinkBlocks(t, 512)
	entries := blockFixture(200)
	blob, kept := encodeTier(entries)

	_, marks, ok := openFrames(blob)
	if !ok {
		t.Fatal("openFrames rejected the encoded blob")
	}
	total := 0
	for i, m := range marks {
		end := len(blob)
		if i+1 < len(marks) {
			end = marks[i+1].off
		}
		f := frames{b: blob[:end], off: m.off}
		for range m.turns {
			var turn schema.Turn
			f.turn(&turn)
			f.num()
		}
		if f.bad {
			t.Fatalf("block %d did not frame from its own offset", i)
		}
		if f.off != end {
			t.Fatalf("block %d declared %d turns but ended at %d, not %d", i, m.turns, f.off, end)
		}
		total += m.turns
	}
	if total != kept {
		t.Errorf("the block table covers %d turns, the file holds %d", total, kept)
	}
}

// TestBlockDecodeDeclinesAnOffsetInsideAFrame is the safety net that makes the
// offsets safe to trust. A table pointing mid-record must be refused rather than
// decoded into plausible nonsense, and the caller must still be able to read the
// file the slow way.
func TestBlockDecodeDeclinesAnOffsetInsideAFrame(t *testing.T) {
	shrinkBlocks(t, 512)
	blob, _ := encodeTier(blockFixture(200))
	_, marks, ok := openFrames(blob)
	if !ok || len(marks) < 3 {
		t.Fatalf("the fixture framed into %d blocks, too few for this test", len(marks))
	}

	// One byte into the frame is never a frame boundary: it lands inside the
	// leading length varint of the record the block was supposed to start on.
	marks[1].off++
	if _, ok := decodeBlocks(blob, marks, nil); ok {
		t.Fatal("decodeBlocks accepted an offset that is not a frame boundary")
	}
	if got := decodeSequentially(t, blob); len(got) != 200 {
		t.Errorf("the sequential walk recovered %d turns after the table was refused, want 200", len(got))
	}
}

// TestBlockDecodeDeclinesAWrongTurnCount covers the other half of the check: an
// offset table that frames cleanly but promises the wrong number of turns per
// block would place turns at the wrong indices.
func TestBlockDecodeDeclinesAWrongTurnCount(t *testing.T) {
	shrinkBlocks(t, 512)
	blob, _ := encodeTier(blockFixture(200))
	_, marks, ok := openFrames(blob)
	if !ok || len(marks) < 3 {
		t.Fatalf("the fixture framed into %d blocks, too few for this test", len(marks))
	}

	marks[1].turns++
	if _, ok := decodeBlocks(blob, marks, nil); ok {
		t.Fatal("decodeBlocks accepted a block claiming more turns than it frames")
	}
}

// TestBlockDecodeDeclinesABlockThatStopsShort is the case only the
// exact-consumption check catches. A block claiming fewer turns than it holds
// frames every one of them cleanly and simply stops early, so nothing about the
// framing is wrong — the turns it skipped would silently go missing, and every
// turn after them would land at the wrong index.
func TestBlockDecodeDeclinesABlockThatStopsShort(t *testing.T) {
	shrinkBlocks(t, 512)
	blob, _ := encodeTier(blockFixture(200))
	_, marks, ok := openFrames(blob)
	if !ok || len(marks) < 3 {
		t.Fatalf("the fixture framed into %d blocks, too few for this test", len(marks))
	}

	marks[1].turns--
	if _, ok := decodeBlocks(blob, marks, nil); ok {
		t.Fatal("decodeBlocks accepted a block that leaves framed turns unread")
	}
}

// TestBlockDecodeLeavesTheCallersTurnsAloneWhenItDeclines matters because Turns
// decodes three tiers into one slice: a declining second tier must not truncate
// the first tier's turns already in it.
func TestBlockDecodeLeavesTheCallersTurnsAloneWhenItDeclines(t *testing.T) {
	shrinkBlocks(t, 512)
	blob, _ := encodeTier(blockFixture(200))
	_, marks, _ := openFrames(blob)
	marks[1].off++

	held := []schema.Turn{{UUID: "already-here"}, {UUID: "and-here"}}
	got, ok := decodeBlocks(blob, marks, held)
	if ok {
		t.Fatal("decodeBlocks accepted a broken table")
	}
	if len(got) != len(held) || got[0].UUID != "already-here" || got[1].UUID != "and-here" {
		t.Fatalf("a declined decode returned %d turns %+v, want the 2 it was handed", len(got), got)
	}
}

func TestOpenFramesRejectsACorruptBlockTable(t *testing.T) {
	body := appendEntry(nil, entry{Turn: schema.Turn{Session: "s", UUID: "u", Text: "x"}})
	head := func(marks ...block) []byte {
		b := append([]byte(tierMagic), byte(len(marks)))
		for _, m := range marks {
			b = binary.AppendUvarint(b, uint64(m.off))
			b = binary.AppendUvarint(b, uint64(m.turns))
		}
		return append(b, body...)
	}

	for _, tc := range []struct {
		name string
		blob []byte
	}{
		{"a block starting past the end of the file", head(block{off: 1 << 20, turns: 1})},
		{"a first block that does not start at the body", head(block{off: 3, turns: 1})},
		{"offsets that do not increase", head(block{off: 0, turns: 1}, block{off: 0, turns: 1})},
		{"a block holding no turns", head(block{off: 0, turns: 0})},
		{"a count with no table behind it", append([]byte(tierMagic), 9)},
		{"a truncated magic", []byte(tierMagic[:4])},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, ok := openFrames(tc.blob); ok {
				t.Error("openFrames accepted it")
			}
		})
	}
}

// allocsPerDecode is testing.AllocsPerRun without the one thing that makes it
// useless here: that function pins GOMAXPROCS to 1 for its duration, which takes
// the worker count below two and measures the sequential walk under the name of
// the concurrent one. TestLoadingATierDoesNotAllocatePerTurn uses AllocsPerRun
// and so covers the sequential decode; this covers the other.
func allocsPerDecode(runs int, fn func()) float64 {
	fn()
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for range runs {
		fn()
	}
	runtime.ReadMemStats(&after)
	return float64(after.Mallocs-before.Mallocs) / float64(runs)
}

// TestTheConcurrentDecodeDoesNotAllocatePerTurn is the same property the
// sequential decode is held to, stated the same way: the concurrent decode costs
// a worker's worth of setup and a table of blocks, and neither of those grows
// with the corpus. Ten times the turns must not cost ten times the allocations.
//
// It is a comparison between two sizes rather than a ceiling because a ceiling
// would have to be read off a run of this code, which proves only that the code
// does what it does.
func TestTheConcurrentDecodeDoesNotAllocatePerTurn(t *testing.T) {
	shrinkBlocks(t, 4096)
	measure := func(turns int) float64 {
		blob, _ := encodeTier(blockFixture(turns))
		_, marks, ok := openFrames(blob)
		if !ok || len(marks) < 2 {
			t.Fatalf("%d turns framed into %d blocks, too few to decode concurrently", turns, len(marks))
		}
		return allocsPerDecode(5, func() {
			if _, ok := decodeBlocks(blob, marks, nil); !ok {
				t.Fatal("decodeBlocks declined its own table")
			}
		})
	}

	const few, many = 400, 4000
	fewAllocs, manyAllocs := measure(few), measure(many)

	// One allocation per hundred turns is far below the per-turn or per-field
	// decode this rules out, and far above the flat cost this implementation
	// should hold at.
	budget := float64(many-few) / 100
	if extra := manyAllocs - fewAllocs; extra > budget {
		t.Errorf("%d more turns cost %.0f more allocations (budget %.0f) — the concurrent decode is allocating per turn",
			many-few, extra, budget)
	}
	t.Logf("%d turns: %.0f allocations · %d turns: %.0f allocations", few, fewAllocs, many, manyAllocs)
}

// TestTurnsReadsEveryTierThroughTheConcurrentDecode drives the whole read path
// rather than decodeBlocks alone, because the thing Turns does that a direct call
// does not is decode three tiers into one growing slice.
func TestTurnsReadsEveryTierThroughTheConcurrentDecode(t *testing.T) {
	shrinkBlocks(t, 1024)
	s := newStore(t, corpus(t).Root)
	mustUpdate(t, s)

	want := mustTurns(t, s)
	if len(want) == 0 {
		t.Fatal("the fixture corpus stripped to nothing")
	}

	// Re-read with the block table refused, which is the sequential walk, and the
	// two must agree turn for turn.
	blockBytes = 1 << 30
	again := mustTurns(t, s)
	if len(again) != len(want) {
		t.Fatalf("the concurrent decode returned %d turns, the sequential walk %d", len(want), len(again))
	}
	for i := range want {
		if want[i] != again[i] {
			t.Fatalf("turn %d differs: concurrent %+v, sequential %+v", i, want[i], again[i])
		}
	}
}
