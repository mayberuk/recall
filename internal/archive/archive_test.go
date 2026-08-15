package archive

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// clearHomeEnv isolates Dir and DefaultRoot from whatever environment the test
// binary itself runs under, so each case starts from exactly the variables it
// sets.
func clearHomeEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{"RECALL_HOME", "XDG_DATA_HOME", "CLAUDE_PROJECTS_DIR"} {
		t.Setenv(name, "")
	}
}

func TestDirPrefersRecallHome(t *testing.T) {
	clearHomeEnv(t)
	t.Setenv("RECALL_HOME", "/custom/recall-home")
	t.Setenv("XDG_DATA_HOME", "/should/not/be/used")

	got, err := Dir()
	if err != nil {
		t.Fatalf("Dir: %v", err)
	}
	if want := "/custom/recall-home"; got != want {
		t.Errorf("Dir() = %q, want %q", got, want)
	}
}

func TestDirFallsBackToXDGDataHome(t *testing.T) {
	clearHomeEnv(t)
	t.Setenv("XDG_DATA_HOME", "/custom/xdg")

	got, err := Dir()
	if err != nil {
		t.Fatalf("Dir: %v", err)
	}
	if want := filepath.Join("/custom/xdg", "recall"); got != want {
		t.Errorf("Dir() = %q, want %q", got, want)
	}
}

func TestDirFallsBackToHomeLocalShare(t *testing.T) {
	clearHomeEnv(t)
	t.Setenv("HOME", "/custom/home")

	got, err := Dir()
	if err != nil {
		t.Fatalf("Dir: %v", err)
	}
	if want := filepath.Join("/custom/home", ".local", "share", "recall"); got != want {
		t.Errorf("Dir() = %q, want %q", got, want)
	}
}

// TestDirFailsWithoutAHomeDirectory is the failure that matters once neither
// override is set: os.UserHomeDir on unix reads only $HOME, so clearing it is
// the deterministic way to make that lookup fail.
func TestDirFailsWithoutAHomeDirectory(t *testing.T) {
	if os.Getenv("HOME") == "" {
		t.Skip("HOME is already unset in this environment")
	}
	clearHomeEnv(t)
	t.Setenv("HOME", "")

	if _, err := Dir(); err == nil {
		t.Error("Dir succeeded with no RECALL_HOME, XDG_DATA_HOME, or HOME")
	}
}

func TestDefaultRootPrefersClaudeProjectsDir(t *testing.T) {
	clearHomeEnv(t)
	t.Setenv("CLAUDE_PROJECTS_DIR", "/custom/projects")

	got, err := DefaultRoot()
	if err != nil {
		t.Fatalf("DefaultRoot: %v", err)
	}
	if want := "/custom/projects"; got != want {
		t.Errorf("DefaultRoot() = %q, want %q", got, want)
	}
}

func TestDefaultRootFallsBackToHomeClaudeProjects(t *testing.T) {
	clearHomeEnv(t)
	t.Setenv("HOME", "/custom/home")

	got, err := DefaultRoot()
	if err != nil {
		t.Fatalf("DefaultRoot: %v", err)
	}
	if want := filepath.Join("/custom/home", ".claude", "projects"); got != want {
		t.Errorf("DefaultRoot() = %q, want %q", got, want)
	}
}

func TestDefaultRootFailsWithoutAHomeDirectory(t *testing.T) {
	if os.Getenv("HOME") == "" {
		t.Skip("HOME is already unset in this environment")
	}
	clearHomeEnv(t)
	t.Setenv("HOME", "")

	if _, err := DefaultRoot(); err == nil {
		t.Error("DefaultRoot succeeded with no CLAUDE_PROJECTS_DIR or HOME")
	}
}

// TestOpenDerivesDirAndRootWhenUnset covers the branch every other test in
// this package bypasses by always passing Dir and Root explicitly: Open must
// still fall through to Dir() and DefaultRoot() when Options leaves them empty.
func TestOpenDerivesDirAndRootWhenUnset(t *testing.T) {
	clearHomeEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	s, err := Open(Options{Strip: stubStrip, Resolve: stubResolve})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if want := filepath.Join(home, ".local", "share", "recall"); s.Dir() != want {
		t.Errorf("Dir() = %q, want %q", s.Dir(), want)
	}
	if want := filepath.Join(home, ".claude", "projects"); s.Root() != want {
		t.Errorf("Root() = %q, want %q", s.Root(), want)
	}
	if _, err := os.Stat(s.Dir()); err != nil {
		t.Errorf("Open did not create the derived directory: %v", err)
	}
}

// TestOpenFailsWhenTheArchiveDirectoryCannotBeCreated is the error path
// MkdirAll takes when a path component is a file rather than a directory.
func TestOpenFailsWhenTheArchiveDirectoryCannotBeCreated(t *testing.T) {
	base := t.TempDir()
	blocker := filepath.Join(base, "blocker")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Open(Options{Dir: filepath.Join(blocker, "archive"), Root: t.TempDir(), Strip: stubStrip, Resolve: stubResolve})
	if err == nil {
		t.Error("Open succeeded creating a directory under a plain file")
	}
}

// TestOpenPropagatesADirResolutionFailure is Open's error path when it must
// derive the archive directory itself and that derivation fails, rather than
// only when the caller passed a bad Dir directly.
func TestOpenPropagatesADirResolutionFailure(t *testing.T) {
	if os.Getenv("HOME") == "" {
		t.Skip("HOME is already unset in this environment")
	}
	clearHomeEnv(t)
	t.Setenv("HOME", "")

	if _, err := Open(Options{Root: t.TempDir(), Strip: stubStrip, Resolve: stubResolve}); err == nil {
		t.Error("Open succeeded deriving Dir with no RECALL_HOME, XDG_DATA_HOME, or HOME")
	}
}

// TestOpenPropagatesARootResolutionFailure is the same failure on the other
// derived path: Root is left unset and DefaultRoot cannot resolve one either.
func TestOpenPropagatesARootResolutionFailure(t *testing.T) {
	if os.Getenv("HOME") == "" {
		t.Skip("HOME is already unset in this environment")
	}
	clearHomeEnv(t)
	t.Setenv("HOME", "")

	if _, err := Open(Options{Dir: t.TempDir(), Strip: stubStrip, Resolve: stubResolve}); err == nil {
		t.Error("Open succeeded deriving Root with no CLAUDE_PROJECTS_DIR or HOME")
	}
}

func TestWrittenAtIsZeroBeforeAnyUpdate(t *testing.T) {
	s := newStore(t, t.TempDir())
	if got := s.WrittenAt(); !got.IsZero() {
		t.Errorf("WrittenAt() = %s before any update, want the zero time", got)
	}
}

func TestWrittenAtMatchesTheMetadataFileMtime(t *testing.T) {
	s := newStore(t, tinyCorpus(t, map[string]string{
		"-p/5d0b7c46-8d05-4e93-a712-00000000000f.jsonl": grownRecord(
			"5d0b7c46-8d05-4e93-a712-00000000000f", "5d0b7c46-0000-4000-8000-000000000001",
			"2026-08-09T10:00:00.000Z", "alpha"),
	}))
	mustUpdate(t, s)

	fi, err := os.Stat(s.MetaPath())
	if err != nil {
		t.Fatalf("stat the metadata: %v", err)
	}
	if got := s.WrittenAt(); !got.Equal(fi.ModTime()) {
		t.Errorf("WrittenAt() = %s, want the metadata file's mtime %s", got, fi.ModTime())
	}
	if got := s.WrittenAt(); time.Since(got) > time.Minute {
		t.Errorf("WrittenAt() = %s, want a time close to now", got)
	}
}
