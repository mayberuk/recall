package archive

import (
	"os"
	"testing"
)

func TestParseCursorAcceptsTheMarkListFormat(t *testing.T) {
	in := []byte("-scratch-normal/a.jsonl:0\n-scratch-normal/b.jsonl:4096\nweird:name.jsonl:12\n")
	marks, ok := parseCursor(in)
	if !ok {
		t.Fatalf("parseCursor rejected a well-formed mark list")
	}
	want := map[string]int64{
		"-scratch-normal/a.jsonl": 0,
		"-scratch-normal/b.jsonl": 4096,
		"weird:name.jsonl":        12,
	}
	if len(marks) != len(want) {
		t.Fatalf("marks = %v, want %v", marks, want)
	}
	for rel, size := range want {
		if marks[rel] != size {
			t.Errorf("mark for %s = %d, want %d", rel, marks[rel], size)
		}
	}
	if got := string(formatCursor(marks)); got != string(in) {
		t.Errorf("formatCursor round trip = %q, want %q", got, string(in))
	}
}

func TestParseCursorRejectsAnythingMalformed(t *testing.T) {
	cases := map[string]string{
		"no separator":      "a.jsonl 12\n",
		"empty path":        ":12\n",
		"empty length":      "a.jsonl:\n",
		"non numeric":       "a.jsonl:many\n",
		"negative length":   "a.jsonl:-1\n",
		"duplicate path":    "a.jsonl:1\na.jsonl:2\n",
		"one bad line only": "a.jsonl:1\nb.jsonl:oops\nc.jsonl:3\n",
		"binary noise":      "\x00\x01\x02\n",
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			marks, ok := parseCursor([]byte(in))
			if ok {
				t.Fatalf("parseCursor accepted %q", in)
			}
			if marks != nil {
				t.Fatalf("parseCursor returned %v for %q; a partial mark set keeps a mark for a file whose real mark is unknown", marks, in)
			}
		})
	}
}

// An unparseable cursor means "no marks known". The rule is fail-closed: every
// file is re-read whole, nothing is skipped, and dedup keeps the archive from
// growing a second copy of what it already holds.
func TestUnparseableCursorRereadsEveryFile(t *testing.T) {
	c := corpus(t)
	s := newStore(t, c.Root)

	first := mustUpdate(t, s)
	if first.FilesWhole != first.FilesSeen {
		t.Fatalf("cold pass read %d of %d files whole", first.FilesWhole, first.FilesSeen)
	}
	before := len(mustTurns(t, s))

	// One bad line among good ones. Salvaging the rest would keep a mark for
	// every other file and skip it, which is the failure this rule prevents.
	good, err := os.ReadFile(s.CursorPath())
	if err != nil {
		t.Fatalf("read the cursor: %v", err)
	}
	if err := os.WriteFile(s.CursorPath(), append(good, []byte("this is not a mark\n")...), 0o644); err != nil {
		t.Fatalf("corrupt the cursor: %v", err)
	}

	second := mustUpdate(t, s)
	if second.FilesSkipped != 0 {
		t.Errorf("skipped %d files on an unparseable cursor; want 0", second.FilesSkipped)
	}
	if second.FilesWhole != second.FilesSeen {
		t.Errorf("re-read %d of %d files whole; want all of them", second.FilesWhole, second.FilesSeen)
	}
	if second.TurnsAdded != 0 {
		t.Errorf("re-reading added %d turns; uuid dedup should absorb every one", second.TurnsAdded)
	}
	if after := len(mustTurns(t, s)); after != before {
		t.Errorf("archive holds %d turns after a re-read, held %d before", after, before)
	}
}

func TestMissingCursorMeansNoMarksKnown(t *testing.T) {
	c := corpus(t)
	s := newStore(t, c.Root)
	mustUpdate(t, s)

	if err := os.Remove(s.CursorPath()); err != nil {
		t.Fatalf("remove the cursor: %v", err)
	}
	res := mustUpdate(t, s)
	if res.FilesSkipped != 0 || res.FilesWhole != res.FilesSeen {
		t.Fatalf("missing cursor skipped %d and read %d of %d whole; want 0 and all",
			res.FilesSkipped, res.FilesWhole, res.FilesSeen)
	}
}
