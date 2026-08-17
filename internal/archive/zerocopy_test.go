package archive

import (
	"strings"
	"testing"
	"unsafe"

	"github.com/mayberuk/recall/internal/schema"
)

// TestDecodedFieldsPointIntoTheTierFileBuffer states the premise the tier files'
// immutability rests on. Without it the invariant documented on frames would be
// vacuous — nothing would break if a caller wrote to the buffer — and the next
// reader of that comment would have no way to tell whether it still applies.
func TestDecodedFieldsPointIntoTheTierFileBuffer(t *testing.T) {
	want := entry{Turn: schema.Turn{
		Session: "s1",
		UUID:    "u1",
		TS:      "2026-01-01T00:00:00Z",
		Tier:    schema.TierConversation,
		Author:  schema.AuthorHuman,
		Agent:   "",
		Repo:    "example/repo",
		Branch:  "main",
		CWD:     "/checkout",
		Text:    "the quick brown fox",
	}}
	blob, kept := encodeTier([]entry{want})
	if kept != 1 {
		t.Fatalf("encodeTier kept %d entries, want 1", kept)
	}

	f, _, ok := openFrames(blob)
	if !ok {
		t.Fatal("openFrames rejected a blob it had just written")
	}
	got, good := f.next()
	if !good {
		t.Fatal("the entry did not decode")
	}
	if got != want {
		t.Fatalf("decoded %+v, want %+v", got, want)
	}

	base := uintptr(unsafe.Pointer(unsafe.SliceData(blob)))
	end := base + uintptr(len(blob))
	for _, field := range []struct {
		name  string
		value string
	}{
		{"Session", got.Session},
		{"UUID", got.UUID},
		{"TS", got.TS},
		{"Tier", string(got.Tier)},
		{"Author", string(got.Author)},
		{"Repo", got.Repo},
		{"Branch", got.Branch},
		{"CWD", got.CWD},
		{"Text", got.Text},
	} {
		at := uintptr(unsafe.Pointer(unsafe.StringData(field.value)))
		if at < base || at >= end {
			t.Errorf("%s was copied out of the tier buffer; the decode is no longer zero-copy, "+
				"so the immutability rule documented on frames now costs something and protects nothing",
				field.name)
		}
	}

	// An empty field cannot be a view: there is no byte to point at, and at the
	// end of the buffer there is no address to form either.
	if got.Agent != "" {
		t.Fatalf("Agent decoded as %q, want empty", got.Agent)
	}
}

// TestLoadingATierDoesNotAllocatePerTurn is the load cost this package is held
// to, stated as the property rather than as a number: reading ten times as many
// turns must not cost ten times as many allocations. Copying ten fields per turn
// out of the buffer is 340,000 allocations on the conversation tier of a real
// store, and it fails nowhere else — the output is identical, only slower.
//
// Comparing two sizes rather than asserting a ceiling is what keeps the test
// honest. A ceiling would have to be read off a run of this code, which proves
// only that the code does what it does; the fixed cost of opening three files and
// parsing the metadata is then indistinguishable from a per-turn cost.
func TestLoadingATierDoesNotAllocatePerTurn(t *testing.T) {
	const small, large = 200, 2000

	fewAllocs, fewTurns := loadAllocs(t, small)
	manyAllocs, manyTurns := loadAllocs(t, large)
	if manyTurns <= fewTurns {
		t.Fatalf("the larger corpus archived %d turns against the smaller one's %d", manyTurns, fewTurns)
	}

	extraTurns := manyTurns - fewTurns
	extraAllocs := manyAllocs - fewAllocs
	// A per-field decode costs ten allocations per turn, a per-turn decode one.
	// One per hundred turns is far below either and far above the zero this
	// implementation should reach, so the test names the regression rather than
	// the implementation.
	budget := float64(extraTurns) / 100
	if extraAllocs > budget {
		t.Errorf("%d more turns cost %.0f more allocations (budget %.0f) — "+
			"the decode is allocating per turn or per field, and that scales with the corpus",
			extraTurns, extraAllocs, budget)
	}
	t.Logf("%d turns: %.0f allocations · %d turns: %.0f allocations",
		fewTurns, fewAllocs, manyTurns, manyAllocs)
}

func loadAllocs(t *testing.T, records int) (allocs float64, turns int) {
	t.Helper()
	root := tinyCorpus(t, map[string]string{"proj/one.jsonl": manyTurns(records)})
	s := newStore(t, root)
	mustUpdate(t, s)
	turns = len(mustTurns(t, s))
	if turns < records {
		t.Fatalf("the store holds %d turns from %d records", turns, records)
	}
	// AllocsPerRun fixes GOMAXPROCS to 1 for the duration and averages several
	// runs, so the count is the steady-state one rather than the first read's.
	return testing.AllocsPerRun(5, func() {
		if _, err := s.Turns(); err != nil {
			t.Fatalf("Turns: %v", err)
		}
	}), turns
}

// manyTurns writes n user records, each with text long enough that a per-field
// copy is unmistakable in an allocation count.
func manyTurns(n int) string {
	var b strings.Builder
	for i := range n {
		b.WriteString(`{"type":"user","uuid":"u`)
		b.WriteString(string(rune('a' + i%26)))
		b.WriteString(itoa(i))
		b.WriteString(`","sessionId":"s1","timestamp":"2026-01-01T00:00:0`)
		b.WriteString(itoa(i % 10))
		b.WriteString(`Z","cwd":"/checkout","gitBranch":"main","message":{"content":"`)
		b.WriteString(strings.Repeat("value return path walk ", 8))
		b.WriteString(`"}}`)
		b.WriteByte('\n')
	}
	return b.String()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
