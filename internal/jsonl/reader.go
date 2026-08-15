package jsonl

import (
	"bufio"
	"bytes"
	"io"
	"os"

	"github.com/mayberuk/recall/internal/fperr"
)

// readBuffer is sized so an ordinary record is one ReadSlice with no copy.
// Transcript files reach hundreds of megabytes, so the reader streams and never
// holds more than one record plus this buffer.
const readBuffer = 256 << 10

// Line is one physical line of a JSONL file.
//
// Bytes aliases the reader's internal buffer and is only valid until the next
// call to Next; a caller that keeps it must copy. Offset and Length locate the
// record in the file and are what the pinned hit schema carries.
type Line struct {
	Offset int64
	Length int
	Bytes  []byte
}

// Reader streams a JSONL file one line at a time, from the start or from a byte
// offset a previous run recorded.
type Reader struct {
	name string
	f    *os.File
	br   *bufio.Reader
	buf  []byte
	line Line
	off  int64
	err  error
}

// Open streams path from its first byte.
func Open(path string) (*Reader, error) { return OpenAt(path, 0) }

// OpenAt streams path from offset, which is the archive's per-file byte cursor.
// A cursor past the end of the file yields no lines rather than an error: a file
// that shrank is re-read from zero by the caller, the only direction that cannot
// lose a turn.
func OpenAt(path string, offset int64) (*Reader, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fperr.New(fperr.SourceVanished, "no such transcript: %s", path)
		}
		return nil, fperr.New(fperr.CorpusUnreadable, "cannot open %s: %v", path, err)
	}
	if offset > 0 {
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			_ = f.Close()
			return nil, fperr.New(fperr.CorpusUnreadable, "cannot seek %s to %d: %v", path, offset, err)
		}
	}
	return newReader(path, f, bufio.NewReaderSize(f, readBuffer), offset), nil
}

// NewReader streams r, reporting offsets as if r began at base. It exists so
// tests and in-memory callers exercise the same line splitting as a file.
func NewReader(name string, r io.Reader, base int64) *Reader {
	return newReader(name, nil, bufio.NewReaderSize(r, readBuffer), base)
}

func newReader(name string, f *os.File, br *bufio.Reader, base int64) *Reader {
	return &Reader{name: name, f: f, br: br, off: base}
}

// Next advances to the next non-empty line, reporting false at end of file or on
// the first read error. Blank lines are skipped; their bytes still count toward
// Offset.
func (r *Reader) Next() bool {
	for {
		ok, empty := r.readLine()
		if !ok {
			return false
		}
		if !empty {
			return true
		}
	}
}

func (r *Reader) readLine() (ok, empty bool) {
	if r.err != nil {
		return false, false
	}
	start := r.off

	chunk, err := r.br.ReadSlice('\n')
	r.off += int64(len(chunk))
	if err == nil {
		body := trimEOL(chunk)
		r.line = Line{Offset: start, Length: len(body), Bytes: body}
		return true, len(body) == 0
	}

	// bufio.Scanner's 64 KB token cap would silently truncate here, and a single
	// record carries up to 679 KB of tool result. Growing without a cap is the
	// only option that cannot invent a false negative.
	r.buf = append(r.buf[:0], chunk...)
	for err == bufio.ErrBufferFull {
		chunk, err = r.br.ReadSlice('\n')
		r.off += int64(len(chunk))
		r.buf = append(r.buf, chunk...)
	}
	switch err {
	case nil:
	case io.EOF:
		if len(r.buf) == 0 {
			return false, false
		}
	default:
		r.err = fperr.New(fperr.CorpusUnreadable, "cannot read %s at offset %d: %v", r.name, start, err)
		return false, false
	}
	body := trimEOL(r.buf)
	r.line = Line{Offset: start, Length: len(body), Bytes: body}
	return true, len(body) == 0
}

func trimEOL(b []byte) []byte {
	b = bytes.TrimSuffix(b, []byte("\n"))
	return bytes.TrimSuffix(b, []byte("\r"))
}

// Line returns the line Next just advanced to.
func (r *Reader) Line() Line { return r.line }

// Record parses the current line. ok is false for a line that is not a JSON
// object — a transcript being appended to can end mid-line, which is an expected
// state rather than a corruption to report.
func (r *Reader) Record() (Record, bool) { return Parse(r.line) }

// Offset is the byte position just past the last line Next returned, which is
// the cursor to resume from. It advances past skipped blank lines too, so
// resuming from it never re-reads and never skips.
func (r *Reader) Offset() int64 { return r.off }

// Err reports the read error that stopped Next, if any. End of file is not one.
func (r *Reader) Err() error { return r.err }

// Close releases the underlying file. Safe on a Reader built by NewReader.
func (r *Reader) Close() error {
	if r.f == nil {
		return nil
	}
	return r.f.Close()
}
