package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/mayberuk/recall/internal/archive"
	"github.com/mayberuk/recall/internal/fperr"
	"github.com/mayberuk/recall/internal/jsonl"
	"github.com/mayberuk/recall/internal/render"
	"github.com/mayberuk/recall/internal/repo"
	"github.com/mayberuk/recall/internal/schema"
	"github.com/mayberuk/recall/internal/strip"
)

func newDoctorCmd() (*flag.FlagSet, *Globals) {
	fs := newFlags("doctor")
	g := NewGlobals()
	g.Bind(fs)
	return fs, g
}

func init() {
	Register("doctor", func(args []string) error { return doctor(args, os.Stdout, os.Stderr) })
	fs, _ := newDoctorCmd()
	Describe("doctor", "", "check the archive's integrity and what the corpus looks like", fs,
		"recall doctor",
		"recall doctor --json",
		"recall doctor --provider all")
}

func doctor(args []string, out, errOut io.Writer) error {
	fs, g := newDoctorCmd()
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	if err := g.Check(); err != nil {
		return err
	}

	sel, err := archive.Select()
	if err != nil {
		return err
	}
	if sel.Fallback {
		fmt.Fprintf(errOut, "recall: %s\n", sel.Reason)
	}
	if len(sel.Agents) == 0 {
		return fperr.New(fperr.CorpusUnreadable, "no agent has a session store to read (%s)", sel.Reason)
	}

	// One resolver, shared across every store this run opens: a checkout
	// resolves to the same repo identity regardless of which agent's session
	// named it, so nothing is lost by sharing the cache, and a fresh resolver
	// per store would just pay for the same git reads twice.
	resolver := repo.New()

	// Labelled only when there is more than one block to tell apart — the
	// default, single-agent run prints exactly what it always has.
	label := len(sel.Agents) > 1

	var totalProblems int
	var anyFailed bool
	for _, agent := range sel.Agents {
		provider, err := freshProviderFor(agent)
		if err != nil {
			return err
		}
		store, err := archive.Open(archive.Options{
			Provider: provider,
			Resolve:  resolver.Repo,
			Force:    true,
		})
		if err != nil {
			return err
		}
		view, err := doctorReport(store, provider)
		if err != nil {
			return err
		}
		if label {
			fmt.Fprintf(out, "%-11s%s\n", "agent", agent)
		}
		if err := emit(out, g, view.Text(), view); err != nil {
			return err
		}
		if !view.OK {
			anyFailed = true
			totalProblems += len(view.Problems)
		}
	}
	if anyFailed {
		return fperr.New(fperr.BadArchive, "archive integrity check failed: %d %s",
			totalProblems, plural(totalProblems, "problem", "problems"))
	}
	return nil
}

// freshProviderFor constructs a provider that has decoded nothing yet. Doctor
// builds one per run rather than reaching for the registry's own instance:
// the registry's is a process-lifetime singleton, and reusing it here would
// accumulate one run's counts into the next.
func freshProviderFor(agent schema.Agent) (archive.Provider, error) {
	switch agent {
	case schema.AgentClaudeCode:
		return strip.ClaudeCode(), nil
	case schema.AgentCodex:
		return strip.Codex(), nil
	default:
		return nil, fperr.New(fperr.Internal, "doctor: no provider constructor for agent %q", agent)
	}
}

// doctorReport is one store's whole doctor result.
func doctorReport(store *archive.Store, provider archive.Provider) (render.Doctor, error) {
	// Force is what makes the authorship counts below mean anything: an
	// incremental pass hands the provider only the records that changed, so
	// TypedLabelsMissing would read false on a corpus that had lost the field.
	//
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
		var err error
		found, err = store.Verify()
		if err != nil {
			return render.Doctor{}, err
		}
		checked = true
	}

	res, err := store.Update()
	if err != nil {
		return render.Doctor{}, err
	}
	rep, err := store.Verify()
	if err != nil {
		return render.Doctor{}, err
	}
	obs := observationFor(res, provider)

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

		// Line, malformed and untyped counts come from the archive's own tally
		// rather than the provider's: a malformed line never reaches a decoder,
		// and a line the reader could not parse is exactly what doctor exists
		// to surface. Unknown-type counts do not: the archive's tally judges a
		// type against Claude Code's own catalog, which every Codex envelope
		// type would fail, so those come from the provider's own observation.
		Lines:        res.Tally.Lines,
		Malformed:    res.Tally.Malformed,
		Untyped:      res.Tally.Untyped,
		UnknownTotal: obs.UnknownTotal,
		UnknownTypes: obs.UnknownTypes,
		Collapsed:    res.Collapsed,

		HumanShaped:        obs.HumanShaped,
		Typed:              obs.Typed,
		CommandArgs:        obs.CommandArgs,
		TypedLabelsMissing: obs.TypedLabelsMissing,

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
	if checked && !intact(found) {
		for _, p := range found.Problems {
			view.Problems = append([]string{"as found, before this run refreshed the store: " + p}, view.Problems...)
		}
	}
	view.Warnings = warningsOf(view)
	view.Warnings = append(view.Warnings, obs.ExtraWarnings...)
	if upgraded {
		view.Warnings = append(view.Warnings,
			"this store predates the integrity checksums, so it could not be checked as found; this run wrote them")
	}
	if checked && !intact(found) && intact(rep) {
		view.Warnings = append(view.Warnings,
			"this run rebuilt the store from the transcripts, so a second `recall doctor` will read clean; "+
				"anything the raw files no longer cover was already lost before it ran")
	}

	return view, nil
}

// providerObservation is what a provider saw, generalised to the fields
// render.Doctor already carries for any agent.
type providerObservation struct {
	HumanShaped        int
	Typed              int
	CommandArgs        int
	TypedLabelsMissing bool
	UnknownTotal       int
	UnknownTypes       []render.TypeCount
	ExtraWarnings      []string
}

func observationFor(res archive.Result, provider archive.Provider) providerObservation {
	switch p := provider.(type) {
	case *strip.ClaudeCodeProvider:
		obs := p.Observation()
		return providerObservation{
			HumanShaped:        obs.HumanShapedMain,
			Typed:              obs.Typed,
			CommandArgs:        obs.CommandArgs,
			TypedLabelsMissing: obs.TypedLabelsMissing(),
			UnknownTotal:       res.Tally.UnknownTotal(),
			UnknownTypes:       typeCounts(res.Tally.UnknownCounts()),
		}
	case *strip.CodexProvider:
		obs := p.Observation()
		total, types := codexUnknownTypes(obs)
		return providerObservation{
			UnknownTotal:  total,
			UnknownTypes:  types,
			ExtraWarnings: codexWarnings(obs),
		}
	default:
		return providerObservation{
			UnknownTotal: res.Tally.UnknownTotal(),
			UnknownTypes: typeCounts(res.Tally.UnknownCounts()),
		}
	}
}

// codexUnknownTypes folds a Codex observation's two unknown vocabularies —
// envelope types and response_item payload types — into the one list
// render.Doctor reports. A payload type is prefixed so a reader is never
// left guessing which vocabulary an unfamiliar name belongs to.
func codexUnknownTypes(obs strip.CodexObservation) (int, []render.TypeCount) {
	var total int
	out := make([]render.TypeCount, 0, len(obs.Tally.Unknown)+len(obs.UnknownPayloads))
	for _, c := range obs.Tally.UnknownCounts() {
		out = append(out, render.TypeCount{Type: c.Type, Count: c.Count})
		total += c.Count
	}
	payloadTypes := make([]string, 0, len(obs.UnknownPayloads))
	for typ := range obs.UnknownPayloads {
		payloadTypes = append(payloadTypes, typ)
	}
	sort.Strings(payloadTypes)
	for _, typ := range payloadTypes {
		n := obs.UnknownPayloads[typ]
		out = append(out, render.TypeCount{Type: "payload:" + typ, Count: n})
		total += n
	}
	return total, out
}

// codexWarnings states what a Codex rollout store left unread rather than
// silently dropping it: a compressed rollout, a compacted record's replaced
// history, and an event_msg record are each excluded on purpose, and doctor
// exists to say so rather than let the exclusion go unreported.
func codexWarnings(obs strip.CodexObservation) []string {
	var out []string
	if obs.Compressed > 0 {
		out = append(out, fmt.Sprintf(
			"%d rollout(s) have been compressed to .jsonl.zst and were not read", obs.Compressed))
	}
	if obs.Replaced > 0 {
		out = append(out, fmt.Sprintf(
			"%d replacement_history record(s) were skipped because they were already archived from their own earlier records", obs.Replaced))
	}
	if obs.Telemetry > 0 {
		out = append(out, fmt.Sprintf(
			"%d event_msg record(s) were dropped because they duplicate a response_item already archived", obs.Telemetry))
	}
	return out
}

func typeCounts(in []jsonl.TypeCount) []render.TypeCount {
	out := make([]render.TypeCount, len(in))
	for i, c := range in {
		out[i] = render.TypeCount{Type: c.Type, Count: c.Count}
	}
	return out
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
