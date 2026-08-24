package archive

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/mayberuk/recall/internal/atomicfile"
	"github.com/mayberuk/recall/internal/fperr"
	"github.com/mayberuk/recall/internal/schema"
)

// tierMagic heads every tier file. A file whose framing this build does not
// understand is rebuilt rather than misread: the frames carry no self-describing
// structure, so a stale format decodes into plausible nonsense.
const tierMagic = "recall-turns-3\n"

// blockBytes is roughly how much framed text one block covers. Varint frames are
// not splittable, so a decoder cannot start anywhere; a block offset is a place
// the encoder promises it may.
//
// The size trades header bytes and per-block setup against how evenly the blocks
// divide among however many cores the reader turns out to have — a number the
// encoder does not know. At 256 KB the largest tier here cuts into ~300 blocks,
// which balances across anything from 2 to 64 workers and costs about 1.5 KB of
// header.
//
// Tests shrink it to reach the concurrent decode on a fixture small enough to
// build in a test, which is the only way that path runs under `go test`; nothing
// else writes it.
var blockBytes = 256 << 10

// block is one place a decoder may start, and how many turns it will find from
// there. Offsets are held relative to the end of the header while encoding,
// because the header's own length depends on how many blocks there turn out to
// be, and absolutised by openFrames.
//
// These live in the tier file rather than beside it in meta.json. A tier that
// gained no turns is not rewritten while meta.json is rewritten on every update,
// so the two files go out of step by design, and an offset table that can
// disagree with the bytes it describes is the one failure this has to rule out.
// In the file it travels with them.
type block struct {
	off   int
	turns int
}

// tierFiles is the partition, and the order the files are written and read in.
var tierFiles = []schema.Tier{schema.TierConversation, schema.TierInvocation, schema.TierResult}

// fileFor places a turn. A tier this build does not know goes in the
// conversation file, which is the one every search reads: an unrecognised tier
// is then over-searched rather than silently dropped.
func fileFor(tier schema.Tier) schema.Tier {
	switch tier {
	case schema.TierInvocation, schema.TierResult:
		return tier
	}
	return schema.TierConversation
}

// entry is one archived turn. Seq is the turn's position within the record it
// was stripped from, which is what keeps a record's thinking and its prose in
// the order they were written after the global sort.
type entry struct {
	schema.Turn
	Seq int
}

// tierState is one tier file's integrity record. Every tier is covered, because
// once the raw transcripts age out a truncated one cannot be reconciled against
// anything.
type tierState struct {
	Checksum string `json:"checksum"`
	Bytes    int64  `json:"bytes"`
	Turns    int    `json:"turns"`
}

type meta struct {
	Version     int                  `json:"version"`
	Tiers       map[string]tierState `json:"tiers"`
	Turns       int                  `json:"turns"`
	Sessions    int                  `json:"sessions"`
	ContentFrom string               `json:"content_from"`
	ContentTo   string               `json:"content_to"`
	LiveFrom    string               `json:"live_from"`
	LiveFiles   int                  `json:"live_files"`
	MaxFileSkew int64                `json:"max_file_skew"`
	MaxSkewFile string               `json:"max_skew_file"`

	// MTimes carries the mtime each marked file had when it was last read. The
	// cursor's pinned line format has room for a length and nothing else, and
	// "mtime moved without growth" cannot be detected without the previous
	// value. A file missing from this map is re-read whole.
	MTimes map[string]int64 `json:"mtimes"`

	// Oldest is each file's own oldest record timestamp, which is what the
	// per-file skew is measured against. A resumed read only sees the new bytes,
	// so this is carried forward rather than recomputed.
	Oldest map[string]int64 `json:"oldest"`
}

// compare is the archive's total order. It is a function of a turn's own fields
// only, so a cold rebuild and a sequence of incremental updates sort to the same
// bytes — which is what acceptance case a9 asserts.
func compare(a, b entry) int {
	if c := strings.Compare(a.TS, b.TS); c != 0 {
		return c
	}
	if c := strings.Compare(a.Session, b.Session); c != 0 {
		return c
	}
	if c := strings.Compare(a.UUID, b.UUID); c != 0 {
		return c
	}
	if a.Seq != b.Seq {
		if a.Seq < b.Seq {
			return -1
		}
		return 1
	}
	if c := strings.Compare(string(a.Tier), string(b.Tier)); c != 0 {
		return c
	}
	if c := strings.Compare(string(a.Author), string(b.Author)); c != 0 {
		return c
	}
	if c := strings.Compare(a.Agent, b.Agent); c != 0 {
		return c
	}
	if c := strings.Compare(a.Repo, b.Repo); c != 0 {
		return c
	}
	if c := strings.Compare(a.Branch, b.Branch); c != 0 {
		return c
	}
	if c := strings.Compare(a.CWD, b.CWD); c != 0 {
		return c
	}
	return strings.Compare(a.Text, b.Text)
}

func (s *Store) loadMeta() (meta, bool) {
	data, err := os.ReadFile(s.MetaPath())
	if err != nil {
		return meta{}, false
	}
	var m meta
	if err := json.Unmarshal(data, &m); err != nil || m.Version != formatVersion {
		return meta{}, false
	}
	s.recordTurnHints(m)
	return m, true
}

func (s *Store) recordTurnHints(m meta) {
	if s.turnHints == nil {
		s.turnHints = make(map[schema.Tier]int, len(tierFiles))
	}
	for _, tier := range tierFiles {
		s.turnHints[tier] = m.Tiers[string(tier)].Turns
	}
}

// turnHint sizes one tier's decode. It reads the metadata only if no pass has
// reported the counts yet, which for `find` is the --no-update case: the refresh
// reads the metadata anyway, and parsing it twice cost 1.5 ms of a search.
func (s *Store) turnHint(tier schema.Tier) int {
	if s.turnHints == nil {
		s.loadMeta()
	}
	return s.turnHints[tier]
}

func appendStr(dst []byte, s string) []byte {
	dst = binary.AppendUvarint(dst, uint64(len(s)))
	return append(dst, s...)
}

func appendEntry(dst []byte, e entry) []byte {
	dst = appendStr(dst, e.Session)
	dst = appendStr(dst, e.UUID)
	dst = appendStr(dst, e.TS)
	dst = appendStr(dst, string(e.Tier))
	dst = appendStr(dst, string(e.Author))
	dst = appendStr(dst, e.Agent)
	dst = appendStr(dst, e.Repo)
	dst = appendStr(dst, e.Branch)
	dst = appendStr(dst, e.CWD)
	dst = appendStr(dst, e.Text)
	return binary.AppendUvarint(dst, uint64(e.Seq))
}

// frames walks a tier file's records. Every length is bounds-checked against the
// buffer, so a truncated or corrupt file stops the walk instead of reading past
// the end or inventing a field.
//
// A decoded field is a view into b, not a copy of it. That is worth 340,000
// allocations on a conversation-tier load and 1.7 million on all three, but it
// makes b immutable for as long as any turn decoded from it is reachable: b is
// one whole tier file read in a single call, nothing writes to it after the read,
// and the strings pointing into it keep it alive. A caller that mutated b, or
// handed it to something that might, would rewrite turns already returned.
type frames struct {
	b   []byte
	off int
	bad bool
}

// openFrames reads the magic and the block table, and returns a walk positioned
// at the first turn.
//
// Every offset is checked here — in bounds, strictly increasing, and starting
// exactly where the header ends — so a decoder that trusts the table has already
// had it validated against the file's own length. What that cannot prove is that
// an offset lands on a frame boundary, and decodeBlocks holds each block to
// finishing exactly on the next one for that.
func openFrames(b []byte) (*frames, []block, bool) {
	if len(b) < len(tierMagic) || string(b[:len(tierMagic)]) != tierMagic {
		return nil, nil, false
	}
	f := &frames{b: b, off: len(tierMagic)}

	// Two bytes is the least a block can be framed in, so a count past that is a
	// corrupt header and not an allocation to attempt.
	n := f.num()
	if f.bad || n > len(b) {
		return nil, nil, false
	}
	marks := make([]block, n)
	for i := range marks {
		marks[i] = block{off: f.num(), turns: f.num()}
		if f.bad {
			return nil, nil, false
		}
	}

	head := f.off
	for i := range marks {
		marks[i].off += head
		if marks[i].turns <= 0 || marks[i].off >= len(b) {
			return nil, nil, false
		}
		if i > 0 && marks[i].off <= marks[i-1].off {
			return nil, nil, false
		}
	}
	if len(marks) > 0 && marks[0].off != head {
		return nil, nil, false
	}
	return f, marks, true
}

func (f *frames) done() bool { return f.bad || f.off >= len(f.b) }

func (f *frames) str() string {
	n, w := binary.Uvarint(f.b[f.off:])
	if w <= 0 || n > uint64(len(f.b)-f.off-w) {
		f.bad = true
		return ""
	}
	start := f.off + w
	f.off = start + int(n)
	if n == 0 {
		// unsafe.String needs a pointer it may not form at the end of the buffer,
		// and an empty field is common: Agent and Branch are absent more often
		// than they are present.
		return ""
	}
	return unsafe.String(&f.b[start], int(n))
}

func (f *frames) num() int {
	n, w := binary.Uvarint(f.b[f.off:])
	if w <= 0 {
		f.bad = true
		return 0
	}
	f.off += w
	return int(n)
}

// turn decodes one turn's fields in place. Filling a slot the caller already
// owns is what lets a tier load append into a preallocated slice without a
// second copy of every field.
func (f *frames) turn(t *schema.Turn) {
	t.Session = f.str()
	t.UUID = f.str()
	t.TS = f.str()
	t.Tier = schema.Tier(f.str())
	t.Author = schema.Author(f.str())
	t.Agent = f.str()
	t.Repo = f.str()
	t.Branch = f.str()
	t.CWD = f.str()
	t.Text = f.str()
}

func (f *frames) next() (entry, bool) {
	var e entry
	f.turn(&e.Turn)
	e.Seq = f.num()
	return e, !f.bad
}

// readTier decodes one tier file. ok is false for a file this build cannot frame
// or one that ends mid-record; the entries recovered before that point are still
// returned, because the archive outlives the raw files and discarding it on a
// read error would destroy the only copy.
//
// hint is how many turns the file is expected to hold, for sizing the result. It
// is a hint and not a contract — a wrong answer costs a regrowth and nothing
// else — so a caller without the metadata to hand passes zero. Verify is what
// holds the recorded count to account.
func (s *Store) readTier(tier schema.Tier, hint int, dst []entry) ([]entry, bool) {
	if dst == nil {
		dst = make([]entry, 0, hint)
	}
	data, err := readWhole(s.TierPath(tier))
	if err != nil {
		return dst, os.IsNotExist(err)
	}
	f, _, ok := openFrames(data)
	if !ok {
		return dst, false
	}
	for !f.done() {
		dst = append(dst, entry{})
		e := &dst[len(dst)-1]
		f.turn(&e.Turn)
		e.Seq = f.num()
		if f.bad {
			return dst[:len(dst)-1], false
		}
		e.Origin = s.agent
	}
	return dst, true
}

// decodeBlocks decodes every block at once, each worker taking a contiguous run
// of them and filling the slice of dst those blocks were promised to cover.
//
// ok is false when the table did not describe the file, and then nothing has been
// decoded: dst comes back at the length it arrived with, for the caller to walk
// sequentially instead. Two things have to hold for a block, and neither is a
// property of the offsets alone — it yields exactly the turns it declared, and it
// ends exactly where the next block starts. An offset landing mid-frame fails one
// of them, which is what keeps a table that disagrees with the bytes from
// decoding into plausible nonsense.
func decodeBlocks(b []byte, marks []block, dst []schema.Turn) ([]schema.Turn, bool) {
	workers := min(runtime.GOMAXPROCS(0), len(marks))
	if workers < 2 {
		return dst, false
	}

	total := 0
	for _, m := range marks {
		total += m.turns
	}
	base := len(dst)
	if cap(dst)-base >= total {
		dst = dst[:base+total]
	} else {
		grown := make([]schema.Turn, base+total)
		copy(grown, dst)
		dst = grown
	}

	// Each worker takes a contiguous run of blocks, so where its turns land is a
	// running total over the runs already handed out — which is why no per-block
	// index table is built: this load is on the path of every search, and a slice
	// per tier is a slice a search pays for.
	var failed atomic.Bool
	per := len(marks) / workers
	var wg sync.WaitGroup
	for w, at := 0, base; w < workers; w++ {
		lo := w * per
		hi := lo + per
		if w == workers-1 {
			hi = len(marks)
		}
		start := at
		for i := lo; i < hi; i++ {
			at += marks[i].turns
		}

		wg.Add(1)
		go func() {
			defer wg.Done()
			for i, slot := lo, start; i < hi; i++ {
				end := len(b)
				if i+1 < len(marks) {
					end = marks[i+1].off
				}
				// Bounding the walk at the next block's offset is what stops a
				// block that under-reads from running on into turns another worker
				// is already writing.
				f := frames{b: b[:end], off: marks[i].off}
				turns := dst[slot : slot+marks[i].turns]
				slot += marks[i].turns
				for k := range turns {
					if f.done() {
						failed.Store(true)
						return
					}
					f.turn(&turns[k])
					f.num()
					if f.bad {
						failed.Store(true)
						return
					}
				}
				if f.off != end {
					failed.Store(true)
					return
				}
			}
		}()
	}
	wg.Wait()

	if failed.Load() {
		return dst[:base], false
	}
	return dst, true
}

// readTurns decodes one tier file straight into turns. The update path needs the
// per-record sequence number and a search does not, so a search never pays for
// the entry-to-Turn copy that carrying it would cost.
func (s *Store) readTurns(tier schema.Tier, dst []schema.Turn) ([]schema.Turn, bool) {
	data, err := readWhole(s.TierPath(tier))
	if err != nil {
		return dst, os.IsNotExist(err)
	}
	f, marks, ok := openFrames(data)
	if !ok {
		return dst, false
	}
	// The frames carry no agent: a tier file belongs to one store, so framing
	// it would write the same short string a few hundred thousand times. The
	// store stamps it back on afterwards instead.
	base := len(dst)
	if out, ok := decodeBlocks(data, marks, dst); ok {
		return s.stampOrigin(out, base), true
	}
	for !f.done() {
		dst = append(dst, schema.Turn{})
		f.turn(&dst[len(dst)-1])
		f.num() // Seq, framed per turn and unread here
		if f.bad {
			return s.stampOrigin(dst[:len(dst)-1], base), false
		}
	}
	return s.stampOrigin(dst, base), true
}

func (s *Store) stampOrigin(turns []schema.Turn, from int) []schema.Turn {
	for i := from; i < len(turns); i++ {
		turns[i].Origin = s.agent
	}
	return turns
}

// Turns reads the archived turns of the given tiers, or every tier when none is
// named. A default `find` asks for the conversation tier alone and so never
// touches the result tier, which is four fifths of the bytes.
//
// The returned turns hold strings that point into the tier files' buffers rather
// than copies of them; see frames for what that makes those buffers.
func (s *Store) Turns(want ...schema.Tier) ([]schema.Turn, error) {
	sel := want
	if len(sel) == 0 {
		sel = tierFiles
	}
	files := make([]schema.Tier, 0, len(tierFiles))
	read := map[schema.Tier]bool{}
	total := 0
	for _, tier := range sel {
		file := fileFor(tier)
		if read[file] {
			continue
		}
		read[file] = true
		files = append(files, file)
		total += s.turnHint(file)
	}

	turns := make([]schema.Turn, 0, total)
	for _, file := range files {
		var ok bool
		turns, ok = s.readTurns(file, turns)
		if !ok {
			return nil, fperr.New(fperr.BadArchive,
				"archive at %s did not read cleanly; run `recall doctor`", s.TierPath(file))
		}
	}
	return turns, nil
}

func (s *Store) loadEntries() ([]entry, bool) {
	total := 0
	for _, tier := range tierFiles {
		total += s.turnHint(tier)
	}
	out := make([]entry, 0, total)
	clean := true
	for _, tier := range tierFiles {
		var ok bool
		out, ok = s.readTier(tier, 0, out)
		clean = clean && ok
	}
	return out, clean
}

// encodeTier frames one tier's turns. Adjacent identical records are dropped: a
// record with no uuid has no dedup key, and re-reading its file would otherwise
// add a second copy on every whole-file pass.
func encodeTier(entries []entry) ([]byte, int) {
	body := make([]byte, 0, 64*len(entries))
	var marks []block
	var scratch, prev []byte
	kept, open := 0, block{}
	for _, e := range entries {
		scratch = appendEntry(scratch[:0], e)
		if bytes.Equal(prev, scratch) {
			continue
		}
		// A block closes before the turn that would overrun it rather than after,
		// so every offset recorded is the first byte of a frame.
		if open.turns > 0 && len(body)-open.off >= blockBytes {
			marks = append(marks, open)
			open = block{off: len(body)}
		}
		body = append(body, scratch...)
		prev = append(prev[:0], scratch...)
		kept++
		open.turns++
	}
	if open.turns > 0 {
		marks = append(marks, open)
	}

	out := make([]byte, 0, len(tierMagic)+binary.MaxVarintLen64*(1+2*len(marks))+len(body))
	out = append(out, tierMagic...)
	out = binary.AppendUvarint(out, uint64(len(marks)))
	for _, m := range marks {
		out = binary.AppendUvarint(out, uint64(m.off))
		out = binary.AppendUvarint(out, uint64(m.turns))
	}
	return append(out, body...), kept
}

func checksum(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func stamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func unstamp(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

func (s *Store) writeMeta(m meta) error {
	if err := atomicfile.WriteJSON(s.MetaPath(), m); err != nil {
		return fperr.New(fperr.AtomicWriteFailed, "cannot write the archive metadata: %v", err)
	}
	s.recordTurnHints(m)
	return nil
}

// sidecarNames are the files the checksums sidecar covers, in the order it lists
// them, which is what makes it byte-identical between two runs of one corpus.
var sidecarNames = []string{cursorName, metaName}

func (s *Store) writeChecksums() error {
	var b strings.Builder
	for _, name := range sidecarNames {
		data, err := os.ReadFile(filepath.Join(s.dir, name))
		if err != nil {
			return fperr.New(fperr.AtomicWriteFailed, "cannot digest %s: %v", name, err)
		}
		b.WriteString(checksum(data))
		b.WriteString("  ")
		b.WriteString(name)
		b.WriteByte('\n')
	}
	if err := atomicfile.Write(s.ChecksumsPath(), []byte(b.String())); err != nil {
		return fperr.New(fperr.AtomicWriteFailed, "cannot write the checksums: %v", err)
	}
	return nil
}

// checksums reads the sidecar. ok is false when it is absent or malformed, both
// of which mean the store's integrity cannot be shown and the next update
// rebuilds rather than trusting it.
func (s *Store) checksums() (map[string]string, bool) {
	data, err := os.ReadFile(s.ChecksumsPath())
	if err != nil {
		return nil, false
	}
	out := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" {
			continue
		}
		digest, name, found := strings.Cut(line, "  ")
		if !found || len(digest) != 64 || name == "" {
			return nil, false
		}
		out[name] = digest
	}
	return out, len(out) == len(sidecarNames)
}

// digestOf hashes one of the store's own files. ok is false when it is missing.
func (s *Store) digestOf(name string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(s.dir, name))
	if err != nil {
		return "", false
	}
	return checksum(data), true
}

// Coverage reads the two boundaries the last update recorded.
func (s *Store) Coverage() (Coverage, error) {
	m, ok := s.loadMeta()
	if !ok {
		return Coverage{}, fperr.New(fperr.BadArchive, "no readable archive metadata at %s", s.MetaPath())
	}
	return coverageOf(m), nil
}

func coverageOf(m meta) Coverage {
	return Coverage{
		LiveFrom:    unstamp(m.LiveFrom),
		LiveFiles:   m.LiveFiles,
		ContentFrom: unstamp(m.ContentFrom),
		ContentTo:   unstamp(m.ContentTo),
		Turns:       m.Turns,
		Sessions:    m.Sessions,
		MaxFileSkew: time.Duration(m.MaxFileSkew),
		MaxSkewFile: m.MaxSkewFile,
	}
}
