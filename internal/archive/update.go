package archive

import (
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/mayberuk/recall/internal/atomicfile"
	"github.com/mayberuk/recall/internal/fperr"
	"github.com/mayberuk/recall/internal/jsonl"
	"github.com/mayberuk/recall/internal/schema"
)

type action int

const (
	actSkip action = iota
	actResume
	actWhole
)

type source struct {
	rel   string
	path  string
	size  int64
	mtime int64
}

type job struct {
	src  source
	from int64
}

// marksets is everything the store remembers per source file: where to resume,
// the mtime that resume decision is checked against, and the file's own oldest
// record, which the per-file skew is measured from.
type marksets struct {
	mark   map[string]int64
	mtime  map[string]int64
	oldest map[string]int64
}

func newMarksets() marksets {
	return marksets{mark: map[string]int64{}, mtime: map[string]int64{}, oldest: map[string]int64{}}
}

// plan is the freshness rule. Unchanged skips; grown resumes from the mark; new,
// shrank, or touched without growing is re-read whole. Every unknown lands on
// actWhole, which costs a pass and cannot lose a turn.
func plan(src source, marks, mtimes map[string]int64) action {
	mark, known := marks[src.rel]
	if !known {
		return actWhole
	}
	switch {
	case src.size < mark:
		return actWhole
	case src.size > mark:
		return actResume
	}
	prev, ok := mtimes[src.rel]
	if !ok || prev != src.mtime {
		return actWhole
	}
	return actSkip
}

// list finds every transcript under the root, in a fixed order.
func (s *Store) list() ([]source, error) {
	var out []source
	err := filepath.WalkDir(s.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if d.IsDir() || filepath.Ext(path) != ".jsonl" {
			return nil
		}
		rel, rerr := filepath.Rel(s.root, path)
		if rerr != nil {
			return rerr
		}
		out = append(out, source{rel: filepath.ToSlash(rel), path: path})
		return nil
	})
	if err != nil {
		return nil, fperr.New(fperr.CorpusUnreadable, "cannot walk %s: %v", s.root, err)
	}
	slices.SortFunc(out, func(a, b source) int { return strings.Compare(a.rel, b.rel) })
	return out, nil
}

// Update brings the archive level with the corpus and reports what it did.
func (s *Store) Update() (Result, error) {
	var res Result

	prev, metaOK := s.loadMeta()
	marks, cursorOK := s.loadCursor()
	was := marksets{mark: marks, mtime: prev.MTimes, oldest: prev.Oldest}
	if s.force || !cursorOK || !metaOK || !s.archiveIntact(prev) {
		was = marksets{}
		res.Rebuilt = true
	}

	sources, err := s.list()
	if err != nil {
		return res, err
	}
	res.FilesSeen = len(sources)
	if s.onListed != nil {
		paths := make([]string, len(sources))
		for i, src := range sources {
			paths[i] = src.path
		}
		s.onListed(paths)
	}

	live, cov := s.stat(sources, &res)
	jobs, now := split(live, was)
	res.FilesSkipped = len(now.mark)

	rewrite := res.Rebuilt || len(jobs) > 0
	var entries []entry
	seen := map[string]bool{}
	if rewrite {
		var ok bool
		entries, ok = s.loadEntries()
		if !ok {
			// A partial load means the marks describe turns the archive may not
			// hold, so every mark is re-derived from zero.
			res.Rebuilt = true
			was = marksets{}
			jobs, now = split(live, was)
			res.FilesSkipped = len(now.mark)
		}
		for _, e := range entries {
			if e.UUID != "" {
				seen[dedupKey(e)] = false
			}
		}
	}

	held := len(entries)
	touched := map[schema.Tier]bool{}
	for i, rr := range s.readAll(jobs) {
		j := jobs[i]
		res.Tally.Merge(rr.tally)
		res.RecordsRead += rr.records
		if rr.err != nil {
			var fe *fperr.Error
			if errors.As(rr.err, &fe) && fe.Code == fperr.SourceVanished {
				res.Vanished = append(res.Vanished, j.src.rel)
				continue
			}
			res.Unreadable = append(res.Unreadable, j.src.rel)
		}
		if j.from == 0 {
			res.FilesWhole++
		} else {
			res.FilesAppended++
		}
		var added, collapsed int
		entries, added, collapsed = merge(entries, rr.entries, seen, touched)
		res.TurnsAdded += added
		res.Collapsed += collapsed
		now.mark[j.src.rel] = rr.mark
		now.mtime[j.src.rel] = j.src.mtime
		// A resumed read only saw the bytes past the mark, so it cannot know the
		// file's oldest record; the earlier answer still can.
		oldest := rr.oldest
		if j.from > 0 {
			if o, ok := was.oldest[j.src.rel]; ok && (oldest == 0 || o < oldest) {
				oldest = o
			}
		}
		if oldest != 0 {
			now.oldest[j.src.rel] = oldest
		}
	}
	cov.MaxFileSkew, cov.MaxSkewFile = skew(now)

	// Repo resolution runs here, after the parallel reads, so the injected
	// resolver is only ever called from one goroutine: it shells out to git.
	repos := map[string]string{}
	for i := held; i < len(entries); i++ {
		entries[i].Repo = s.repo(entries[i].CWD, repos)
	}

	return s.commit(res, cov, prev, entries, now, rewrite, touched)
}

// archiveIntact is the corruption check the unchanged-corpus path can afford: a
// truncated tier file has a size the metadata does not agree with. Recomputing
// the checksums belongs to Verify, which `recall doctor` runs.
func (s *Store) archiveIntact(m meta) bool {
	if !s.sidecarAgrees() {
		return false
	}
	for _, tier := range tierFiles {
		want := m.Tiers[string(tier)].Bytes
		fi, err := os.Stat(s.TierPath(tier))
		if err != nil {
			if !os.IsNotExist(err) || want != 0 {
				return false
			}
			continue
		}
		if fi.Size() != want {
			return false
		}
	}
	return true
}

// sidecarAgrees checks the two files the tier checksums cannot cover. It hashes
// 350 KB, not the 260 MB of turns, so the unchanged-corpus path can afford it;
// a mismatch rebuilds rather than trusting a store that cannot show its own
// integrity.
func (s *Store) sidecarAgrees() bool {
	want, ok := s.checksums()
	if !ok {
		return false
	}
	for _, name := range sidecarNames {
		got, present := s.digestOf(name)
		if !present || got != want[name] {
			return false
		}
	}
	return true
}

func (s *Store) stat(sources []source, res *Result) ([]source, Coverage) {
	var cov Coverage
	live := make([]source, 0, len(sources))
	for _, src := range sources {
		fi, err := os.Stat(src.path)
		if err != nil {
			if os.IsNotExist(err) {
				res.Vanished = append(res.Vanished, src.rel)
				continue
			}
			res.Unreadable = append(res.Unreadable, src.rel)
			continue
		}
		src.size = fi.Size()
		src.mtime = fi.ModTime().UnixNano()
		if cov.LiveFrom.IsZero() || fi.ModTime().Before(cov.LiveFrom) {
			cov.LiveFrom = fi.ModTime()
		}
		cov.LiveFiles++
		live = append(live, src)
	}
	return live, cov
}

func split(live []source, prev marksets) ([]job, marksets) {
	var jobs []job
	now := newMarksets()
	for _, src := range live {
		switch plan(src, prev.mark, prev.mtime) {
		case actSkip:
			now.mark[src.rel] = src.size
			now.mtime[src.rel] = src.mtime
			if o, ok := prev.oldest[src.rel]; ok {
				now.oldest[src.rel] = o
			}
		case actResume:
			jobs = append(jobs, job{src: src, from: prev.mark[src.rel]})
		case actWhole:
			jobs = append(jobs, job{src: src, from: 0})
		}
	}
	return jobs, now
}

// skew is the largest gap between a live file's mtime and its own oldest
// record, with the file that carries it. Keys are visited in order so a tie
// resolves the same way on every run; the result is written to the metadata.
func skew(now marksets) (time.Duration, string) {
	var widest time.Duration
	var file string
	for _, rel := range slices.Sorted(maps.Keys(now.mtime)) {
		oldest, ok := now.oldest[rel]
		if !ok {
			continue
		}
		if gap := time.Duration(now.mtime[rel] - oldest); gap > widest {
			widest, file = gap, rel
		}
	}
	return widest, file
}

type readResult struct {
	entries []entry
	mark    int64
	oldest  int64
	records int
	tally   jsonl.Tally
	err     error
}

// readAll strips the files in parallel and returns their results in job order.
// The corpus is 1.29 GB and stripping it is CPU-bound, so this is the lever that
// keeps the cold pass inside its gate; the merge below is sequential, which is
// what keeps the archive bytes a function of the corpus and not of scheduling.
func (s *Store) readAll(jobs []job) []readResult {
	out := make([]readResult, len(jobs))
	n := s.workers
	if n <= 0 {
		n = min(runtime.GOMAXPROCS(0), 8)
	}
	if n > len(jobs) {
		n = len(jobs)
	}
	if n <= 1 {
		for i, j := range jobs {
			out[i] = s.read(j.src, j.from)
		}
		return out
	}

	next := make(chan int)
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				out[i] = s.read(jobs[i].src, jobs[i].from)
			}
		}()
	}
	for i := range jobs {
		next <- i
	}
	close(next)
	wg.Wait()
	return out
}

// read strips one file from offset. The returned mark is where the next run
// resumes: it stops short of a trailing malformed line, because a transcript
// being appended to ends mid-record and advancing past that line would skip the
// record for good once it is complete.
func (s *Store) read(src source, from int64) readResult {
	if s.onStatted != nil {
		s.onStatted(src.path)
	}
	res := readResult{mark: from}
	r, err := jsonl.OpenAt(src.path, from)
	if err != nil {
		res.err = err
		return res
	}
	defer func() { _ = r.Close() }()

	trailingBad := int64(-1)
	for r.Next() {
		line := r.Line()
		rec, ok := r.Record()
		res.mark = r.Offset()
		if !ok {
			res.tally.ObserveMalformed()
			trailingBad = line.Offset
			continue
		}
		trailingBad = -1
		res.tally.Observe(rec)
		res.records++

		turns, keep := s.strip(rec)
		if !keep {
			continue
		}
		for i, t := range turns {
			res.entries = append(res.entries, entry{Turn: t, Seq: i})
			if when, terr := time.Parse(time.RFC3339, t.TS); terr == nil {
				if n := when.UnixNano(); res.oldest == 0 || n < res.oldest {
					res.oldest = n
				}
			}
		}
	}
	if trailingBad >= 0 {
		res.mark = trailingBad
	}
	res.err = r.Err()
	return res
}

// dedupKey identifies one record inside one session. Session is part of the key
// because a fork keeps the record uuid and writes a new sessionId: 3,402 uuids
// carry more than one session over the real store, and keying on uuid alone
// deletes those turns from every session but the first one walked, which is the
// silent false negative the dedup rule exists to avoid causing.
func dedupKey(e entry) string { return e.Session + "\x00" + e.UUID }

// merge folds one file's turns into the archive, dropping any record already
// held for that session: 9,473 uuids appear in more than one file because
// resumed sessions carry prior records forward. A record's turns are contiguous
// and numbered from zero, so Seq == 0 is where one record's turns begin, and a
// record is kept or skipped whole — otherwise a record yielding a conversation
// turn and an invocation turn would lose the second.
// seen holds three states, which is what makes the collapsed count mean the same
// thing on a cold build and on a forced re-read: absent is new, present-and-false
// is held from the archive but not yet met in this run, present-and-true is met
// in this run. Only a second sighting inside one run is a duplicate copy; meeting
// an already-archived record again is the same copy, not another one.
func merge(dst, src []entry, seen map[string]bool, touched map[schema.Tier]bool) (out []entry, added, collapsed int) {
	skip := false
	for _, e := range src {
		if e.Seq == 0 {
			skip = false
			if e.UUID != "" {
				key := dedupKey(e)
				metThisRun, held := seen[key]
				switch {
				case metThisRun:
					collapsed++
					skip = true
				case held:
					skip = true
					seen[key] = true
				default:
					seen[key] = true
				}
			}
		}
		if skip {
			continue
		}
		dst = append(dst, e)
		touched[fileFor(e.Tier)] = true
		added++
	}
	return dst, added, collapsed
}

// repo memoizes the injected resolver, which shells out to git. 144 distinct cwd
// values carry ~266,000 records between them.
func (s *Store) repo(cwd string, cache map[string]string) string {
	if v, ok := cache[cwd]; ok {
		return v
	}
	v := s.resolve(cwd)
	cache[cwd] = v
	return v
}

// commit sorts, writes, and reports. The write order is load-bearing: archive,
// then metadata, then cursor. A crash before the cursor lands leaves marks that
// under-report what was read, so the next run re-reads and dedup absorbs it; a
// cursor written first would claim turns the archive does not hold.
func (s *Store) commit(res Result, cov Coverage, prev meta, entries []entry, now marksets, rewrite bool, touched map[schema.Tier]bool) (Result, error) {
	next := prev
	if rewrite {
		parts := map[schema.Tier][]entry{}
		sessions := map[string]bool{}
		for _, e := range entries {
			file := fileFor(e.Tier)
			parts[file] = append(parts[file], e)
			sessions[e.Session] = true
			t, terr := time.Parse(time.RFC3339, e.TS)
			if terr != nil {
				continue
			}
			if cov.ContentFrom.IsZero() || t.Before(cov.ContentFrom) {
				cov.ContentFrom = t
			}
			if t.After(cov.ContentTo) {
				cov.ContentTo = t
			}
		}

		next.Tiers = map[string]tierState{}
		total := 0
		for _, tier := range tierFiles {
			state, err := s.writeTier(tier, parts[tier], prev, res.Rebuilt || touched[tier])
			if err != nil {
				return res, err
			}
			next.Tiers[string(tier)] = state
			total += state.Turns
		}
		next.Turns = total
		next.Sessions = len(sessions)
		next.ContentFrom = stamp(cov.ContentFrom)
		next.ContentTo = stamp(cov.ContentTo)
		res.Wrote = true
	} else {
		cov.ContentFrom = unstamp(next.ContentFrom)
		cov.ContentTo = unstamp(next.ContentTo)
	}
	cov.Turns = next.Turns
	cov.Sessions = next.Sessions
	res.Coverage = cov

	next.Version = formatVersion
	next.LiveFrom = stamp(cov.LiveFrom)
	next.LiveFiles = cov.LiveFiles
	next.MaxFileSkew = int64(cov.MaxFileSkew)
	next.MaxSkewFile = cov.MaxSkewFile
	next.MTimes = now.mtime
	next.Oldest = now.oldest

	if !res.Wrote && sameMeta(prev, next) {
		return res, nil
	}
	if err := s.writeMeta(next); err != nil {
		return res, err
	}
	if err := s.writeCursor(now.mark); err != nil {
		return res, err
	}
	if err := s.writeChecksums(); err != nil {
		return res, err
	}
	res.Wrote = true
	return res, nil
}

// writeTier re-frames one tier only when that tier gained turns. A tier nobody
// added to is byte-identical to what is already on disk, and the result tier is
// 208 MB of it.
func (s *Store) writeTier(tier schema.Tier, entries []entry, prev meta, changed bool) (tierState, error) {
	if !changed {
		if state, ok := prev.Tiers[string(tier)]; ok {
			if fi, err := os.Stat(s.TierPath(tier)); err == nil && fi.Size() == state.Bytes {
				return state, nil
			}
		}
	}
	slices.SortFunc(entries, compare)
	blob, kept := encodeTier(entries)
	if err := atomicfile.Write(s.TierPath(tier), blob); err != nil {
		return tierState{}, fperr.New(fperr.AtomicWriteFailed, "cannot write the %s tier: %v", tier, err)
	}
	return tierState{Checksum: checksum(blob), Bytes: int64(len(blob)), Turns: kept}, nil
}

func sameMeta(a, b meta) bool {
	if a.Version != b.Version || a.LiveFrom != b.LiveFrom || a.LiveFiles != b.LiveFiles ||
		a.MaxFileSkew != b.MaxFileSkew || a.MaxSkewFile != b.MaxSkewFile ||
		!maps.Equal(a.Tiers, b.Tiers) {
		return false
	}
	return maps.Equal(a.MTimes, b.MTimes) && maps.Equal(a.Oldest, b.Oldest)
}
