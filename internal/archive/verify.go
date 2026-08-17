package archive

import (
	"fmt"
	"os"

	"github.com/mayberuk/recall/internal/schema"
)

// TierReport is one tier file's integrity result.
type TierReport struct {
	Tier     schema.Tier
	Path     string
	Bytes    int64
	Turns    int
	Checksum string
	Expected string
}

// Report is what `recall doctor` prints. Once the raw transcripts have aged out
// there is nothing left to reconcile the archive against, so a corrupt or
// truncated tier has to be detectable rather than merely wrong.
type Report struct {
	Dir      string
	OK       bool
	Tiers    []TierReport
	MetaOK   bool
	CursorOK bool
	Turns    int
	Sessions int
	Coverage Coverage
	Problems []string
}

// Verify recomputes every tier's checksum, walks its frames, and checks the
// record counts against the metadata.
func (s *Store) Verify() (Report, error) {
	rep := Report{Dir: s.dir}

	want, sidecarOK := s.checksums()
	if !sidecarOK {
		rep.Problems = append(rep.Problems, "checksums are missing or malformed; the store cannot show its own integrity")
	}
	for _, name := range sidecarNames {
		got, present := s.digestOf(name)
		covered := sidecarOK && present && got == want[name]
		if name == metaName {
			rep.MetaOK = covered
		} else {
			rep.CursorOK = covered
		}
		if sidecarOK && !covered {
			rep.Problems = append(rep.Problems, fmt.Sprintf("%s does not match its recorded checksum", name))
		}
	}

	m, ok := s.loadMeta()
	if !ok {
		rep.Problems = append(rep.Problems, "archive metadata is missing or unreadable")
		return rep, nil
	}
	rep.Coverage = coverageOf(m)

	sessions := map[string]bool{}
	for _, tier := range tierFiles {
		want := m.Tiers[string(tier)]
		tr := TierReport{Tier: tier, Path: s.TierPath(tier), Expected: want.Checksum}

		blob, err := os.ReadFile(tr.Path)
		if err != nil {
			if os.IsNotExist(err) && want.Bytes == 0 {
				rep.Tiers = append(rep.Tiers, tr)
				continue
			}
			rep.Problems = append(rep.Problems, fmt.Sprintf("%s tier is unreadable: %v", tier, err))
			rep.Tiers = append(rep.Tiers, tr)
			continue
		}
		tr.Bytes = int64(len(blob))
		tr.Checksum = checksum(blob)
		if tr.Checksum != tr.Expected {
			rep.Problems = append(rep.Problems, fmt.Sprintf("%s tier checksum does not match the metadata", tier))
		}
		if tr.Bytes != want.Bytes {
			rep.Problems = append(rep.Problems, fmt.Sprintf("%s tier is %d bytes, metadata says %d", tier, tr.Bytes, want.Bytes))
		}

		entries, clean := s.readTier(tier, want.Turns, nil)
		tr.Turns = len(entries)
		for _, e := range entries {
			sessions[e.Session] = true
		}
		switch {
		case !clean:
			rep.Problems = append(rep.Problems, fmt.Sprintf("%s tier does not frame cleanly; it ends mid-record", tier))
		case tr.Turns != want.Turns:
			rep.Problems = append(rep.Problems, fmt.Sprintf("%s tier holds %d turns, metadata says %d", tier, tr.Turns, want.Turns))
		}
		rep.Turns += tr.Turns
		rep.Tiers = append(rep.Tiers, tr)
	}
	rep.Sessions = len(sessions)

	if rep.Turns != m.Turns {
		rep.Problems = append(rep.Problems, fmt.Sprintf("archive holds %d turns, metadata says %d", rep.Turns, m.Turns))
	}
	if _, parses := s.loadCursor(); !parses {
		rep.Problems = append(rep.Problems, "cursor does not parse; the next update re-reads every file")
	}

	rep.OK = len(rep.Problems) == 0
	return rep, nil
}
