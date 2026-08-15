package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/mayberuk/recall/internal/archive"
	"github.com/mayberuk/recall/internal/fperr"
	"github.com/mayberuk/recall/internal/render"
	"github.com/mayberuk/recall/internal/repo"
	"github.com/mayberuk/recall/internal/strip"
)

func newDoctorCmd() (*flag.FlagSet, *Globals) {
	fs := newFlags("doctor")
	g := NewGlobals()
	g.Bind(fs)
	return fs, g
}

func init() {
	Register("doctor", func(args []string) error { return doctor(args, os.Stdout) })
	fs, _ := newDoctorCmd()
	Describe("doctor", "", "check the archive's integrity and what the corpus looks like", fs,
		"recall doctor",
		"recall doctor --json")
}

func doctor(args []string, out io.Writer) error {
	fs, g := newDoctorCmd()
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	if err := g.Check(); err != nil {
		return err
	}

	// Force is what makes the authorship counts below mean anything: an
	// incremental pass hands strip only the records that changed, so
	// TypedLabelsMissing would read false on a corpus that had lost the field.
	stripper := strip.New()
	store, err := archive.Open(archive.Options{
		Strip:   stripper.Strip,
		Resolve: repo.New().Repo,
		Force:   true,
	})
	if err != nil {
		return err
	}
	// Verified before the refresh, because Force rewrites the metadata and the
	// checksums: verifying afterwards would check this run's repair rather than
	// the store as found, and corruption that repairs itself unreported is the
	// silent-misstatement case doctor exists to catch.
	//
	// Skipped when there is nothing to check against — no store yet, or a store
	// written before the checksums existed. Neither is corruption, and alarming
	// on an upgrade trains a reader to ignore the alarm that matters.
	var found archive.Report
	checked, upgraded := false, false
	switch {
	case !exists(store.MetaPath()):
	case !exists(store.ChecksumsPath()):
		upgraded = true
	default:
		found, err = store.Verify()
		if err != nil {
			return err
		}
		checked = true
	}

	res, err := store.Update()
	if err != nil {
		return err
	}
	rep, err := store.Verify()
	if err != nil {
		return err
	}
	obs := stripper.Observation()

	view := render.Doctor{
		Verb:     "doctor",
		Dir:      rep.Dir,
		Root:     store.Root(),
		OK:       intact(rep) && (!checked || intact(found)),
		FoundOK:  intact(found),
		AfterOK:  intact(rep),
		Checked:  checked,
		MetaOK:   rep.MetaOK,
		CursorOK: rep.CursorOK,
		Turns:    rep.Turns,
		Sessions: rep.Sessions,

		LiveFrom:    render.Day(rep.Coverage.LiveFrom),
		ContentFrom: render.Day(rep.Coverage.ContentFrom),
		ContentTo:   render.Day(rep.Coverage.ContentTo),
		SkewDays:    rep.Coverage.MaxFileSkewDays(),
		SkewFile:    rep.Coverage.MaxSkewFile,

		Files:      res.FilesSeen,
		Vanished:   res.Vanished,
		Unreadable: res.Unreadable,

		// Record counts come from the archive's own tally rather than strip's:
		// a malformed line never reaches Strip, and a line the reader could not
		// parse is exactly what doctor exists to surface.
		Lines:        res.Tally.Lines,
		Malformed:    res.Tally.Malformed,
		Untyped:      res.Tally.Untyped,
		UnknownTotal: res.Tally.UnknownTotal(),
		Collapsed:    res.Collapsed,

		HumanShaped:        obs.HumanShapedMain,
		Typed:              obs.Typed,
		CommandArgs:        obs.CommandArgs,
		TypedLabelsMissing: obs.TypedLabelsMissing(),

		Problems: rep.Problems,
	}
	for _, t := range rep.Tiers {
		view.Bytes += t.Bytes
		view.Tiers = append(view.Tiers, render.TierIntegrity{
			Tier:     string(t.Tier),
			Path:     t.Path,
			OK:       t.Checksum == t.Expected,
			Bytes:    t.Bytes,
			Turns:    t.Turns,
			Checksum: t.Checksum,
		})
	}
	for _, u := range res.Tally.UnknownCounts() {
		view.UnknownTypes = append(view.UnknownTypes, render.TypeCount{Type: u.Type, Count: u.Count})
	}
	if checked && !intact(found) {
		for _, p := range found.Problems {
			view.Problems = append([]string{"as found, before this run refreshed the store: " + p}, view.Problems...)
		}
	}
	view.Warnings = warningsOf(view)
	if upgraded {
		view.Warnings = append(view.Warnings,
			"this store predates the integrity checksums, so it could not be checked as found; this run wrote them")
	}
	if checked && !intact(found) && intact(rep) {
		view.Warnings = append(view.Warnings,
			"this run rebuilt the store from the transcripts, so a second `recall doctor` will read clean; "+
				"anything the raw files no longer cover was already lost before it ran")
	}

	if err := emit(out, g, view.Text(), view); err != nil {
		return err
	}
	if !view.OK {
		return fperr.New(fperr.BadArchive, "archive integrity check failed: %d %s",
			len(view.Problems), plural(len(view.Problems), "problem", "problems"))
	}
	return nil
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// intact requires every component the store can corrupt independently, not just
// the aggregate: meta.json carries the tier checksums, so a report that trusted
// only the tier comparison would be checking a corrupt file against itself.
func intact(rep archive.Report) bool {
	if !rep.OK || !rep.MetaOK || !rep.CursorOK || len(rep.Tiers) == 0 {
		return false
	}
	for _, t := range rep.Tiers {
		if t.Checksum != t.Expected {
			return false
		}
	}
	return true
}

func warningsOf(d render.Doctor) []string {
	var out []string
	if d.TypedLabelsMissing {
		out = append(out, render.TypedLabelsWarning)
	}
	if d.UnknownTotal > 0 {
		out = append(out, fmt.Sprintf("%d records carry a type this build has never seen; they are archived and searchable, but nothing interprets their fields", d.UnknownTotal))
	}
	if d.Malformed > 0 {
		out = append(out, fmt.Sprintf("%d lines did not parse as JSON; the next update re-reads from the last good line", d.Malformed))
	}
	if n := len(d.Vanished); n > 0 {
		out = append(out, fmt.Sprintf("%d transcripts disappeared between stat and open — Claude Code's cleanup deletes at startup, and whatever they held is gone unless the archive already had it", n))
	}
	if n := len(d.Unreadable); n > 0 {
		out = append(out, fmt.Sprintf("%d transcripts could not be read", n))
	}
	return out
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
