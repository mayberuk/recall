// Package archive is recall's durable store: the stripped conversation, the
// per-file byte cursors that make an update incremental, and the two coverage
// boundaries.
//
// It owns the corpus walk because it owns the cursors, but it is built beside
// internal/strip and internal/repo rather than after them, so it takes both as
// injected functions. Nothing here ever writes under ~/.claude/projects.
package archive

import (
	"os"
	"path/filepath"
	"time"

	"github.com/mayberuk/recall/internal/fperr"
	"github.com/mayberuk/recall/internal/jsonl"
	"github.com/mayberuk/recall/internal/schema"
)

const (
	formatVersion = 2

	cursorName    = "cursor"
	metaName      = "meta.json"
	checksumsName = "checksums"
)

// Options configures a Store. Strip and Resolve are injected because archive is
// built in parallel with the packages that provide them; both are required, so a
// missing one fails at Open rather than silently producing turns with no repo.
//
// Strip is called from several goroutines at once and must be safe for that.
// Resolve is not: it runs after the reads, on one goroutine, because resolving a
// repo identity starts a git process.
type Options struct {
	Dir     string
	Root    string
	Strip   func(jsonl.Record) ([]schema.Turn, bool)
	Resolve func(cwd string) string

	// Force re-reads every source file regardless of its cursor mark. `doctor`
	// uses it to re-derive the tally over the whole corpus.
	Force bool

	// Workers caps the parallel reads. Zero picks a default from GOMAXPROCS.
	Workers int
}

// Store is one archive directory: the compressed turns, the cursor, and the
// metadata that carries the integrity checksum.
type Store struct {
	dir     string
	root    string
	strip   func(jsonl.Record) ([]schema.Turn, bool)
	resolve func(cwd string) string
	force   bool
	workers int

	// onListed and onStatted reproduce the cleanup race in tests. Claude Code
	// deletes transcripts at startup, so a file can vanish between the walk and
	// the stat, or between the stat and the open; neither window is reachable
	// from outside the package by timing alone.
	onListed  func(paths []string)
	onStatted func(path string)
}

// Dir is where the archive lives: $RECALL_HOME, else $XDG_DATA_HOME/recall,
// else ~/.local/share/recall. Deliberately a data directory and not a cache —
// once the raw transcripts age out, this is the only copy of what was said.
func Dir() (string, error) {
	if d := os.Getenv("RECALL_HOME"); d != "" {
		return d, nil
	}
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return filepath.Join(d, "recall"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fperr.New(fperr.BadArchive, "cannot locate the home directory: %v", err)
	}
	return filepath.Join(home, ".local", "share", "recall"), nil
}

// DefaultRoot is Claude Code's session store, which recall only ever reads.
func DefaultRoot() (string, error) {
	if d := os.Getenv("CLAUDE_PROJECTS_DIR"); d != "" {
		return d, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fperr.New(fperr.CorpusUnreadable, "cannot locate the home directory: %v", err)
	}
	return filepath.Join(home, ".claude", "projects"), nil
}

// Open prepares the archive directory, creating it if it is absent.
func Open(opt Options) (*Store, error) {
	if opt.Strip == nil {
		return nil, fperr.New(fperr.Internal, "archive: no strip function was injected")
	}
	if opt.Resolve == nil {
		return nil, fperr.New(fperr.Internal, "archive: no repo resolver was injected")
	}
	dir := opt.Dir
	if dir == "" {
		d, err := Dir()
		if err != nil {
			return nil, err
		}
		dir = d
	}
	root := opt.Root
	if root == "" {
		r, err := DefaultRoot()
		if err != nil {
			return nil, err
		}
		root = r
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fperr.New(fperr.AtomicWriteFailed, "cannot create %s: %v", dir, err)
	}
	return &Store{
		dir:     dir,
		root:    root,
		strip:   opt.Strip,
		resolve: opt.Resolve,
		force:   opt.Force,
		workers: opt.Workers,
	}, nil
}

// Dir is the archive directory.
func (s *Store) Dir() string { return s.dir }

// Root is the corpus the Store walks.
func (s *Store) Root() string { return s.root }

// TierPath is the file holding one tier's turns. They are stored uncompressed
// and framed so a reader slices records in place: a query pays for the tiers it
// searches and runs no decoder over the ones it does not.
func (s *Store) TierPath(tier schema.Tier) string {
	return filepath.Join(s.dir, string(fileFor(tier))+".turns")
}

// CursorPath is the per-file mark list.
func (s *Store) CursorPath() string { return filepath.Join(s.dir, cursorName) }

// MetaPath is the metadata file carrying the tier checksums.
func (s *Store) MetaPath() string { return filepath.Join(s.dir, metaName) }

// ChecksumsPath covers the two files that carry no digest of their own. A file
// cannot hold its own checksum, so meta.json and the cursor are digested here
// and meta.json in turn digests the tier files; corrupting this sidecar makes it
// disagree with both files it names, which is the same detection either way.
func (s *Store) ChecksumsPath() string { return filepath.Join(s.dir, checksumsName) }

// Coverage is what the archive can honestly claim to cover. The two boundaries
// are different numbers and conflating them is a lie about coverage: cleanup
// deletes by file mtime, so LiveFrom is the oldest mtime still on disk — what
// cleanup deletes next — while ContentFrom is the oldest date the words reach.
//
// MaxFileSkew is a separate, per-file measurement and is not a boundary. It is
// how far one file's mtime runs ahead of its own oldest record, which reaches 55
// days on the real corpus because a resumed session is written long after it was
// started. It is a `recall doctor` diagnostic; the footer wants the boundaries.
type Coverage struct {
	LiveFrom    time.Time
	LiveFiles   int
	ContentFrom time.Time
	ContentTo   time.Time
	Turns       int
	Sessions    int
	MaxFileSkew time.Duration
	MaxSkewFile string
}

// ReachesBeforeLive reports whether the archive holds words older than the
// oldest raw file, which is the only claim the coverage footer can make about
// the two boundaries. Comparing them by subtraction says nothing: they are
// minima over different sets.
func (c Coverage) ReachesBeforeLive() bool {
	if c.ContentFrom.IsZero() || c.LiveFrom.IsZero() {
		return false
	}
	return c.ContentFrom.Before(c.LiveFrom)
}

// MaxFileSkewDays is MaxFileSkew in whole days.
func (c Coverage) MaxFileSkewDays() int { return int(c.MaxFileSkew / (24 * time.Hour)) }

// Result is what one Update did. Vanished and Unreadable are reported rather
// than folded into a count: ENOENT is otherwise indistinguishable from "never
// existed", which is a silently incomplete archive.
type Result struct {
	FilesSeen     int
	FilesSkipped  int
	FilesAppended int
	FilesWhole    int
	Vanished      []string
	Unreadable    []string

	RecordsRead int
	TurnsAdded  int

	// Collapsed counts records dropped because their (session, uuid) was already
	// held. It is a per-run figure: `doctor` re-reads the whole corpus with Force
	// and so gets the corpus-wide count, which is otherwise invisible downstream
	// because ingest has already collapsed the copies.
	Collapsed int

	// Rebuilt is set when the cursor or the archive did not load and every file
	// was therefore re-read.
	Rebuilt bool
	Wrote   bool

	Tally    jsonl.Tally
	Coverage Coverage
}

// WrittenAt is when the archive was last written, read from the metadata file's
// mtime rather than from a field inside it: adding a field would change the
// format and force every existing archive to rebuild, and the file is rewritten
// on every update anyway.
func (s *Store) WrittenAt() time.Time {
	fi, err := os.Stat(s.MetaPath())
	if err != nil {
		return time.Time{}
	}
	return fi.ModTime()
}
