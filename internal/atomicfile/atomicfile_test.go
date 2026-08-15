package atomicfile

import (
	"errors"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"

	"github.com/mayberuk/recall/internal/fperr"
)

func TestWriteReplacesContents(t *testing.T) {
	p := filepath.Join(t.TempDir(), "archive.bin")
	if err := Write(p, []byte("first")); err != nil {
		t.Fatal(err)
	}
	if err := Write(p, []byte("second")); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "second" {
		t.Errorf("contents = %q, want %q", got, "second")
	}
}

func TestWritePreservesMode(t *testing.T) {
	p := filepath.Join(t.TempDir(), "cursor")
	if err := WriteMode(p, []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Write(p, []byte("b")); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Errorf("mode = %04o, want 0600", got)
	}
}

func TestWriteLeavesNoTempBehind(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "archive.bin")
	if err := Write(p, []byte("x")); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "archive.bin" {
		t.Errorf("directory holds %v, want only archive.bin", entries)
	}
}

// The archive cannot be reconciled against source once Claude Code's cleanup has
// deleted the raw transcripts, so a failed write must leave the previous archive
// exactly as it was.
func TestFailedWriteLeavesOriginalIntact(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root ignores directory write permission, so a read-only directory does not force this failure")
	}

	dir := t.TempDir()
	p := filepath.Join(dir, "archive.bin")
	const original = "the only surviving copy"
	if err := Write(p, []byte(original)); err != nil {
		t.Fatal(err)
	}

	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	err := Write(p, []byte("replacement that must never land"))
	if err == nil {
		t.Fatal("write into an unwritable directory reported success")
	}
	var coded *fperr.Error
	if !errors.As(err, &coded) || coded.Code != fperr.AtomicWriteFailed {
		t.Errorf("error = %v, want code %s", err, fperr.AtomicWriteFailed)
	}

	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != original {
		t.Errorf("contents = %q, want the original %q", got, original)
	}
}

func TestWriteJSONFailureLeavesOriginalIntact(t *testing.T) {
	p := filepath.Join(t.TempDir(), "manifest.json")
	if err := WriteJSON(p, map[string]int{"turns": 1}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}

	if err := WriteJSON(p, map[string]any{"ch": make(chan int)}); err == nil {
		t.Fatal("encoding an unencodable value reported success")
	}

	after, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Errorf("contents = %q, want unchanged %q", after, before)
	}
}

func TestMarshalIsDeterministicAndUnescaped(t *testing.T) {
	v := map[string]string{"b": "2", "a": "<&>"}
	first, err := Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Marshal(map[string]string{"a": "<&>", "b": "2"})
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Errorf("same value encoded two ways: %q vs %q", first, second)
	}
	want := "{\n  \"a\": \"<&>\",\n  \"b\": \"2\"\n}\n"
	if string(first) != want {
		t.Errorf("Marshal = %q, want %q", first, want)
	}
}

func TestMarshalCompactIsOneLine(t *testing.T) {
	got, err := MarshalCompact(map[string]string{"a": "1"})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "{\"a\":\"1\"}\n" {
		t.Errorf("MarshalCompact = %q", got)
	}
}

func TestMarshalCompactReportsAnUnencodableValueRatherThanPanicking(t *testing.T) {
	_, err := MarshalCompact(map[string]any{"ch": make(chan int)})
	if err == nil {
		t.Fatal("a channel cannot be JSON-encoded, so this must fail rather than silently drop the field")
	}
	var coded *fperr.Error
	if !errors.As(err, &coded) || coded.Code != fperr.AtomicWriteFailed {
		t.Errorf("error = %v, want code %s", err, fperr.AtomicWriteFailed)
	}
}

// TestWriteCleansUpTheTempFileWhenTheWriteItselfFails covers the cleanup
// closure directly: a low RLIMIT_FSIZE makes the write syscall fail with
// EFBIG, the same shape as a full disk, without needing one. SIGXFSZ has to
// be ignored first or the process is killed rather than handed an error, and
// the limit and signal disposition are both restored on cleanup so they
// don't leak into the rest of the suite.
func TestWriteCleansUpTheTempFileWhenTheWriteItselfFails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("RLIMIT_FSIZE is a POSIX limit; Windows has no equivalent write-size rlimit")
	}

	var before syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_FSIZE, &before); err != nil {
		t.Fatalf("Getrlimit: %v", err)
	}
	signal.Ignore(syscall.SIGXFSZ)
	if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &syscall.Rlimit{Cur: 4, Max: before.Max}); err != nil {
		t.Skipf("cannot lower RLIMIT_FSIZE in this environment: %v", err)
	}
	t.Cleanup(func() {
		_ = syscall.Setrlimit(syscall.RLIMIT_FSIZE, &before)
		signal.Reset(syscall.SIGXFSZ)
	})

	dir := t.TempDir()
	p := filepath.Join(dir, "archive.bin")
	err := Write(p, []byte("far more than the four bytes the limit allows"))
	if err == nil {
		t.Fatal("a write past RLIMIT_FSIZE reported success")
	}
	var coded *fperr.Error
	if !errors.As(err, &coded) || coded.Code != fperr.AtomicWriteFailed {
		t.Errorf("error = %v, want code %s", err, fperr.AtomicWriteFailed)
	}

	entries, readErr := os.ReadDir(dir)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Errorf("directory holds %v after a failed write; the temp file was not cleaned up", entries)
	}
}

// TestWriteFailsWhenTheTargetIsAnExistingDirectory reaches the rename error
// path: renaming a file over a non-empty directory is refused by the
// filesystem, the same failure shape as the target directory disappearing
// between the temp file's creation and the rename.
func TestWriteFailsWhenTheTargetIsAnExistingDirectory(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "archive.bin")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "keep"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := Write(target, []byte("replacement"))
	if err == nil {
		t.Fatal("renaming a file over a non-empty directory reported success")
	}
	var coded *fperr.Error
	if !errors.As(err, &coded) || coded.Code != fperr.AtomicWriteFailed {
		t.Errorf("error = %v, want code %s", err, fperr.AtomicWriteFailed)
	}

	entries, readErr := os.ReadDir(dir)
	if readErr != nil {
		t.Fatal(readErr)
	}
	for _, e := range entries {
		if e.Name() != "archive.bin" {
			t.Errorf("directory holds an unexpected entry %q; the temp file was not cleaned up", e.Name())
		}
	}
}
