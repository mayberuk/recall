package archive

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mayberuk/recall/internal/atomicfile"
	"github.com/mayberuk/recall/internal/fperr"
	"github.com/mayberuk/recall/internal/schema"
)

// tierMagic heads every tier file. A file whose framing this build does not
// understand is rebuilt rather than misread: the frames carry no self-describing
// structure, so a stale format decodes into plausible nonsense.
const tierMagic = "recall-turns-2\n"

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
	return m, true
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
type frames struct {
	b   []byte
	off int
	bad bool
}

func openFrames(b []byte) (*frames, bool) {
	if len(b) < len(tierMagic) || string(b[:len(tierMagic)]) != tierMagic {
		return nil, false
	}
	return &frames{b: b, off: len(tierMagic)}, true
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
	return string(f.b[start:f.off])
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

func (f *frames) next() (entry, bool) {
	var e entry
	e.Session = f.str()
	e.UUID = f.str()
	e.TS = f.str()
	e.Tier = schema.Tier(f.str())
	e.Author = schema.Author(f.str())
	e.Agent = f.str()
	e.Repo = f.str()
	e.Branch = f.str()
	e.CWD = f.str()
	e.Text = f.str()
	e.Seq = f.num()
	return e, !f.bad
}

// readTier decodes one tier file. ok is false for a file this build cannot frame
// or one that ends mid-record; the entries recovered before that point are still
// returned, because the archive outlives the raw files and discarding it on a
// read error would destroy the only copy.
func (s *Store) readTier(tier schema.Tier) ([]entry, bool) {
	data, err := os.ReadFile(s.TierPath(tier))
	if err != nil {
		return nil, os.IsNotExist(err)
	}
	f, ok := openFrames(data)
	if !ok {
		return nil, false
	}
	var out []entry
	for !f.done() {
		e, good := f.next()
		if !good {
			return out, false
		}
		out = append(out, e)
	}
	return out, true
}

func (s *Store) loadEntries() ([]entry, bool) {
	var out []entry
	clean := true
	for _, tier := range tierFiles {
		entries, ok := s.readTier(tier)
		out = append(out, entries...)
		clean = clean && ok
	}
	return out, clean
}

// Turns reads the archived turns of the given tiers, or every tier when none is
// named. A default `find` asks for the conversation tier alone and so never
// touches the result tier, which is four fifths of the bytes.
func (s *Store) Turns(want ...schema.Tier) ([]schema.Turn, error) {
	sel := want
	if len(sel) == 0 {
		sel = tierFiles
	}
	var turns []schema.Turn
	read := map[schema.Tier]bool{}
	for _, tier := range sel {
		file := fileFor(tier)
		if read[file] {
			continue
		}
		read[file] = true
		entries, ok := s.readTier(file)
		if !ok {
			return nil, fperr.New(fperr.BadArchive,
				"archive at %s did not read cleanly; run `recall doctor`", s.TierPath(file))
		}
		for _, e := range entries {
			turns = append(turns, e.Turn)
		}
	}
	return turns, nil
}

// encodeTier frames one tier's turns. Adjacent identical records are dropped: a
// record with no uuid has no dedup key, and re-reading its file would otherwise
// add a second copy on every whole-file pass.
func encodeTier(entries []entry) ([]byte, int) {
	out := make([]byte, 0, len(tierMagic)+64*len(entries))
	out = append(out, tierMagic...)
	var scratch, prev []byte
	kept := 0
	for _, e := range entries {
		scratch = appendEntry(scratch[:0], e)
		if bytes.Equal(prev, scratch) {
			continue
		}
		out = append(out, scratch...)
		prev = append(prev[:0], scratch...)
		kept++
	}
	return out, kept
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
