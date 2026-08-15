package render

import (
	"fmt"
	"strings"
)

// TypeCount is one record type this build does not recognise, and how often it
// appeared. Reported rather than ignored: silently dropping an unrecognised
// record across 24 transcript format versions is how a false negative gets in
// through the back door.
type TypeCount struct {
	Type  string `json:"type"`
	Count int    `json:"count"`
}

// TierIntegrity is one tier file's integrity result. Per tier rather than per
// archive because a corrupt result tier and a corrupt conversation tier are
// different problems: the first costs `--results`, the second costs everything.
type TierIntegrity struct {
	Tier     string `json:"tier"`
	Path     string `json:"path"`
	OK       bool   `json:"ok"`
	Bytes    int64  `json:"bytes"`
	Turns    int    `json:"turns"`
	Checksum string `json:"checksum,omitempty"`
}

// Doctor is the whole result of `recall doctor`.
type Doctor struct {
	Verb string `json:"verb"`
	Dir  string `json:"dir"`
	Root string `json:"root"`
	// OK is the verdict and drives the exit status. FoundOK is the store as it
	// was before this run refreshed it, and AfterOK is the store now: a Force
	// refresh rebuilds a corrupt file, so the two can disagree and the
	// difference is the whole alarm. Checked is false on a first run, when
	// there was nothing there to have been corrupt.
	OK       bool            `json:"ok"`
	FoundOK  bool            `json:"found_ok"`
	AfterOK  bool            `json:"after_ok"`
	Checked  bool            `json:"found_checked"`
	Tiers    []TierIntegrity `json:"tiers"`
	MetaOK   bool            `json:"meta_ok"`
	CursorOK bool            `json:"cursor_ok"`
	Bytes    int64           `json:"bytes"`
	Turns    int             `json:"turns"`
	Sessions int             `json:"sessions"`

	LiveFrom    string `json:"live_from,omitempty"`
	ContentFrom string `json:"content_from,omitempty"`
	ContentTo   string `json:"content_to,omitempty"`
	SkewDays    int    `json:"skew_days"`
	SkewFile    string `json:"skew_file,omitempty"`

	Files      int      `json:"files"`
	Vanished   []string `json:"vanished,omitempty"`
	Unreadable []string `json:"unreadable,omitempty"`

	Lines        int         `json:"lines"`
	Malformed    int         `json:"malformed"`
	Untyped      int         `json:"untyped"`
	UnknownTotal int         `json:"unknown_total"`
	UnknownTypes []TypeCount `json:"unknown_types,omitempty"`
	Collapsed    int         `json:"collapsed"`

	HumanShaped        int  `json:"human_shaped"`
	Typed              int  `json:"typed"`
	CommandArgs        int  `json:"command_args"`
	TypedLabelsMissing bool `json:"typed_labels_missing"`

	Problems []string `json:"problems,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

// JSONL is the whole report on one line. A doctor run is a single verdict, not
// a stream of records, so the line-oriented form is the object itself.
func (d Doctor) JSONL() ([]byte, error) { return JSON(d) }

// Text is the human form of a doctor run.
func (d Doctor) Text() []byte {
	var b strings.Builder
	state := okOf(d.OK)
	if d.Checked && !d.FoundOK {
		state = "as found FAILED · after refresh " + okOf(d.AfterOK)
	}
	fmt.Fprintf(&b, "archive    %s\n", d.Dir)
	fmt.Fprintf(&b, "integrity  %s · %d turns · %d sessions · %s\n", state, d.Turns, d.Sessions, bytesOf(d.Bytes))
	for _, t := range d.Tiers {
		fmt.Fprintf(&b, "  %-13s %-6s · %9s · %7d turns\n", t.Tier, okOf(t.OK), bytesOf(t.Bytes), t.Turns)
	}
	// meta.json and the cursor get their own lines because the header above is
	// a summary, and a summary nobody can check is not evidence. meta.json
	// carries the format version, the tier checksums and both coverage
	// boundaries, so a corrupt one misstates coverage rather than failing.
	fmt.Fprintf(&b, "  %-13s %s\n", "meta.json", okOf(d.MetaOK))
	fmt.Fprintf(&b, "  %-13s %s\n", "cursor", okOf(d.CursorOK))
	fmt.Fprintf(&b, "coverage   live to %s · content %s to %s\n", dash(d.LiveFrom), dash(d.ContentFrom), dash(d.ContentTo))
	if d.SkewFile != "" {
		fmt.Fprintf(&b, "skew       %d days on %s\n", d.SkewDays, d.SkewFile)
	}
	fmt.Fprintf(&b, "corpus     %s · %d files · %d vanished · %d unreadable\n",
		d.Root, d.Files, len(d.Vanished), len(d.Unreadable))
	fmt.Fprintf(&b, "records    %d lines · %d malformed · %d untyped · %d of an unknown type\n",
		d.Lines, d.Malformed, d.Untyped, d.UnknownTotal)
	for _, u := range d.UnknownTypes {
		fmt.Fprintf(&b, "  unknown type %s  %d\n", u.Type, u.Count)
	}
	fmt.Fprintf(&b, "dedup      %d records collapsed on (session, uuid) at ingest\n", d.Collapsed)
	fmt.Fprintf(&b, "authorship %d human-shaped · %d typed · %d command-args\n",
		d.HumanShaped, d.Typed, d.CommandArgs)

	for _, f := range d.Vanished {
		fmt.Fprintf(&b, "  vanished between stat and open: %s\n", f)
	}
	for _, f := range d.Unreadable {
		fmt.Fprintf(&b, "  unreadable: %s\n", f)
	}
	for _, w := range d.Warnings {
		fmt.Fprintf(&b, "warning    %s\n", w)
	}
	for _, p := range d.Problems {
		fmt.Fprintf(&b, "problem    %s\n", p)
	}
	return []byte(b.String())
}

// TypedLabelsWarning is the degradation the design requires doctor to catch:
// human-shaped main-session records with not one `typed` label means Claude Code
// stopped writing promptSource, so --mine silently returns nothing instead of
// returning noise.
const TypedLabelsWarning = "the corpus has human-shaped main-session records and no `typed` labels — " +
	"promptSource stopped being written, so --mine now returns nothing rather than too much"

func okOf(ok bool) string {
	if ok {
		return "ok"
	}
	return "FAILED"
}

func bytesOf(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}
