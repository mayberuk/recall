package jsonl

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mayberuk/recall/internal/fperr"
)

func writeTemp(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "transcript.jsonl")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func collect(t *testing.T, r *Reader) []Line {
	t.Helper()
	var out []Line
	for r.Next() {
		l := r.Line()
		out = append(out, Line{Offset: l.Offset, Length: l.Length, Bytes: append([]byte(nil), l.Bytes...)})
	}
	if err := r.Err(); err != nil {
		t.Fatalf("read error: %v", err)
	}
	return out
}

func TestReaderOffsetsAndLengths(t *testing.T) {
	// Offsets and lengths are counted by hand from the literal below, not from a
	// previous run: they are what the pinned hit schema carries.
	tests := []struct {
		name string
		body string
		want []Line
	}{
		{
			name: "three lines",
			body: "{\"a\":1}\n{\"bb\":2}\n{\"c\":3}\n",
			want: []Line{{0, 7, nil}, {8, 8, nil}, {17, 7, nil}},
		},
		{
			name: "no trailing newline",
			body: "{\"a\":1}\n{\"b\":2}",
			want: []Line{{0, 7, nil}, {8, 7, nil}},
		},
		{
			name: "blank lines skipped but counted in offsets",
			body: "{\"a\":1}\n\n\n{\"b\":2}\n",
			want: []Line{{0, 7, nil}, {10, 7, nil}},
		},
		{
			name: "carriage returns trimmed",
			body: "{\"a\":1}\r\n{\"b\":2}\r\n",
			want: []Line{{0, 7, nil}, {9, 7, nil}},
		},
		{
			name: "empty file",
			body: "",
			want: nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r, err := Open(writeTemp(t, tc.body))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = r.Close() }()
			got := collect(t, r)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d lines, want %d", len(got), len(tc.want))
			}
			for i := range got {
				if got[i].Offset != tc.want[i].Offset || got[i].Length != tc.want[i].Length {
					t.Errorf("line %d: got offset=%d length=%d, want offset=%d length=%d",
						i, got[i].Offset, got[i].Length, tc.want[i].Offset, tc.want[i].Length)
				}
				if len(got[i].Bytes) != got[i].Length {
					t.Errorf("line %d: Bytes is %d long but Length says %d",
						i, len(got[i].Bytes), got[i].Length)
				}
			}
			if r.Offset() != int64(len(tc.body)) {
				t.Errorf("final cursor %d, want %d (the whole file)", r.Offset(), len(tc.body))
			}
		})
	}
}

// A single Claude Code record carries up to 679 KB of tool result, and
// bufio.Scanner's 64 KB token cap would truncate it without saying so.
func TestReaderLongLine(t *testing.T) {
	const payload = 1 << 20
	body := `{"type":"user","text":"` + strings.Repeat("z", payload) + `"}` + "\n" +
		`{"type":"user","text":"after"}` + "\n"
	wantFirst := len(body) - len(`{"type":"user","text":"after"}`) - 2

	r, err := Open(writeTemp(t, body))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()

	lines := collect(t, r)
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2", len(lines))
	}
	if lines[0].Length != wantFirst {
		t.Errorf("long line length %d, want %d — a truncating reader loses the tail silently",
			lines[0].Length, wantFirst)
	}
	if got := strings.Count(string(lines[0].Bytes), "z"); got != payload {
		t.Errorf("kept %d payload bytes, want %d", got, payload)
	}
	rec, ok := Parse(lines[0])
	if !ok {
		t.Fatal("the long line did not parse, so it was truncated mid-JSON")
	}
	if got := len(rec.Get("text").String()); got != payload {
		t.Errorf("extracted %d payload bytes, want %d", got, payload)
	}
}

func TestReaderResumeFromOffset(t *testing.T) {
	body := "{\"n\":1}\n{\"n\":2}\n{\"n\":3}\n{\"n\":4}\n"

	full, err := Open(writeTemp(t, body))
	if err != nil {
		t.Fatal(err)
	}
	path := full.name
	defer func() { _ = full.Close() }()
	all := collect(t, full)

	for stop := 0; stop < len(all); stop++ {
		part, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i <= stop; i++ {
			if !part.Next() {
				t.Fatalf("ran out of lines at %d", i)
			}
		}
		cursor := part.Offset()
		_ = part.Close()

		// The archive stores this as a byte-length mark, so it must be the exact
		// count of bytes consumed, terminator included — not the end of the body.
		wantCursor := int64(len(body))
		if stop+1 < len(all) {
			wantCursor = all[stop+1].Offset
		}
		if cursor != wantCursor {
			t.Errorf("cursor after line %d = %d, want %d", stop, cursor, wantCursor)
		}

		rest, err := OpenAt(path, cursor)
		if err != nil {
			t.Fatal(err)
		}
		got := collect(t, rest)
		_ = rest.Close()

		want := all[stop+1:]
		if len(got) != len(want) {
			t.Fatalf("resume at %d: got %d lines, want %d", cursor, len(got), len(want))
		}
		for i := range got {
			if got[i].Offset != want[i].Offset || string(got[i].Bytes) != string(want[i].Bytes) {
				t.Errorf("resume at %d line %d: got offset=%d %q, want offset=%d %q",
					cursor, i, got[i].Offset, got[i].Bytes, want[i].Offset, want[i].Bytes)
			}
		}
	}
}

func TestReaderResumePastEnd(t *testing.T) {
	path := writeTemp(t, "{\"n\":1}\n")
	r, err := OpenAt(path, 4096)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	if got := collect(t, r); len(got) != 0 {
		t.Fatalf("got %d lines past the end, want 0", len(got))
	}
}

func TestOpenMissingFileIsReported(t *testing.T) {
	_, err := Open(filepath.Join(t.TempDir(), "gone.jsonl"))
	if err == nil {
		t.Fatal("a transcript deleted between stat and open must be reported, not skipped")
	}
	var fe *fperr.Error
	if !errors.As(err, &fe) || fe.Code != fperr.SourceVanished {
		t.Errorf("error = %v, want code %s — a vanished file is a distinct case from an unreadable one", err, fperr.SourceVanished)
	}
}

// TestOpenReportsAnUnreadablePathDifferentlyFromAMissingOne is the other half
// of Open's error split: an invalid path fails for a reason that is not "no
// such file", so it must not be reported as one.
func TestOpenReportsAnUnreadablePathDifferentlyFromAMissingOne(t *testing.T) {
	_, err := Open(filepath.Join(t.TempDir(), "bad\x00name.jsonl"))
	if err == nil {
		t.Fatal("a path containing a NUL byte was accepted")
	}
	var fe *fperr.Error
	if !errors.As(err, &fe) || fe.Code != fperr.CorpusUnreadable {
		t.Errorf("error = %v, want code %s", err, fperr.CorpusUnreadable)
	}
}

// TestCloseOnAnInMemoryReaderIsANoOp is the branch a Reader built by
// NewReader takes: there is no underlying file to release.
func TestCloseOnAnInMemoryReaderIsANoOp(t *testing.T) {
	r := NewReader("mem", strings.NewReader(`{"a":1}`+"\n"), 0)
	if err := r.Close(); err != nil {
		t.Errorf("Close() on an in-memory reader = %v, want nil", err)
	}
}
