package render

import (
	"strings"
	"testing"
)

func healthyDoctor() Doctor {
	return Doctor{
		Verb:      "doctor",
		Dir:       "/home/x/.local/share/recall",
		Root:      "/home/x/.claude/projects",
		OK:        true,
		FoundOK:   true,
		AfterOK:   true,
		Checked:   true,
		MetaOK:    true,
		CursorOK:  true,
		Collapsed: 10141,
		Bytes:     261_000_000,
		Turns:     174091,
		Sessions:  135,
		Tiers: []TierIntegrity{
			{Tier: "conversation", OK: true, Bytes: 50_900_000, Turns: 35072},
			{Tier: "invocation", OK: true, Bytes: 44_700_000, Turns: 70219},
			{Tier: "result", OK: true, Bytes: 208_700_000, Turns: 68464},
		},
		LiveFrom:    "2026-06-10",
		ContentFrom: "2026-06-10",
		ContentTo:   "2026-08-14",
		SkewDays:    55,
		SkewFile:    "-home-x-dev-cc-plugins/ea9730d2.jsonl",
		Files:       1119,
		Lines:       302774,
		HumanShaped: 4332,
		Typed:       1130,
		CommandArgs: 168,
	}
}

func TestDoctorReportsIntegrityAndBothBoundaries(t *testing.T) {
	got := string(healthyDoctor().Text())
	for _, want := range []string{
		"integrity  ok · 174091 turns · 135 sessions",
		"conversation  ok",
		"result        ok",
		"meta.json     ok",
		"cursor        ok",
		"dedup      10141 records collapsed on (session, uuid) at ingest",
		"coverage   live to 2026-06-10 · content 2026-06-10 to 2026-08-14",
		"corpus     /home/x/.claude/projects · 1119 files · 0 vanished · 0 unreadable",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output is missing %q:\n%s", want, got)
		}
	}
}

// TestDoctorIsWhereTheFileSkewIsReported pins where the 55-day divergence
// belongs. It is a per-file diagnostic, not a coverage boundary, and putting it
// in a search footer would misstate how far back the tool can see.
func TestDoctorIsWhereTheFileSkewIsReported(t *testing.T) {
	if got := string(healthyDoctor().Text()); !strings.Contains(got, "skew       55 days on -home-x-dev-cc-plugins/ea9730d2.jsonl") {
		t.Errorf("doctor does not report the per-file skew:\n%s", got)
	}
}

// TestDoctorWarnsWhenTypedLabelsVanished is the degradation the design names:
// human-shaped records with zero `typed` labels means Claude Code stopped
// writing promptSource, and --mine then returns nothing rather than noise.
func TestDoctorWarnsWhenTypedLabelsVanished(t *testing.T) {
	d := healthyDoctor()
	d.Typed, d.TypedLabelsMissing = 0, true
	d.Warnings = []string{TypedLabelsWarning}

	got := string(d.Text())
	if !strings.Contains(got, "promptSource stopped being written") {
		t.Errorf("the silent-degradation warning is missing:\n%s", got)
	}
	if !strings.Contains(got, "--mine now returns nothing") {
		t.Errorf("the warning does not say what breaks:\n%s", got)
	}
}

func TestDoctorListsUnknownRecordTypesRatherThanIgnoringThem(t *testing.T) {
	d := healthyDoctor()
	d.UnknownTotal = 3
	d.UnknownTypes = []TypeCount{{Type: "quantum-checkpoint", Count: 2}, {Type: "holo-summary", Count: 1}}

	got := string(d.Text())
	for _, want := range []string{"3 of an unknown type", "unknown type quantum-checkpoint  2", "unknown type holo-summary  1"} {
		if !strings.Contains(got, want) {
			t.Errorf("output is missing %q:\n%s", want, got)
		}
	}
}

func TestDoctorSaysFailedWhenIntegrityDidNotHold(t *testing.T) {
	d := healthyDoctor()
	d.OK = false
	d.Problems = []string{"archive checksum does not match the metadata"}

	got := string(d.Text())
	if !strings.Contains(got, "integrity  FAILED") {
		t.Errorf("a failed check did not say so:\n%s", got)
	}
	if !strings.Contains(got, "problem    archive checksum does not match the metadata") {
		t.Errorf("the problem was not reported:\n%s", got)
	}
}

// TestDoctorReportsIntegrityPerTier is why the report is per tier at all: a
// corrupt result tier costs --results and a corrupt conversation tier costs
// every query, and one aggregate line cannot tell them apart.
func TestDoctorReportsIntegrityPerTier(t *testing.T) {
	d := healthyDoctor()
	d.OK = false
	d.Tiers[2].OK = false
	d.Problems = []string{"result tier checksum does not match the metadata"}

	got := string(d.Text())
	for _, want := range []string{
		"conversation  ok",
		"invocation    ok",
		"result        FAILED",
		"problem    result tier checksum does not match the metadata",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output is missing %q:\n%s", want, got)
		}
	}
	for _, line := range strings.Split(got, "\n") {
		if strings.HasPrefix(line, "  conversation") && strings.Contains(line, "FAILED") {
			t.Errorf("a failing result tier was reported against the conversation tier: %q", line)
		}
	}
}

func TestDoctorCountsTurnsPerTier(t *testing.T) {
	got := string(healthyDoctor().Text())
	for _, want := range []string{"35072 turns", "70219 turns", "68464 turns"} {
		if !strings.Contains(got, want) {
			t.Errorf("output is missing the per-tier turn count %q:\n%s", want, got)
		}
	}
}

// TestDoctorNamesACorruptMetaFile is the failure that used to print
// `integrity ok`: meta.json had no checksum of its own, so a corrupt one
// misstated the coverage boundaries instead of failing.
func TestDoctorNamesACorruptMetaFile(t *testing.T) {
	d := healthyDoctor()
	d.OK, d.FoundOK, d.MetaOK = false, false, false
	d.Problems = []string{"meta.json does not match its recorded checksum"}

	got := string(d.Text())
	if strings.Contains(got, "integrity  ok") {
		t.Errorf("a corrupt meta.json still printed `integrity ok`:\n%s", got)
	}
	if !strings.Contains(got, "as found FAILED · after refresh ok") {
		t.Errorf("the verdict does not separate the store as found from the store now:\n%s", got)
	}
	if !strings.Contains(got, "meta.json     FAILED") {
		t.Errorf("the component line does not name meta.json as the failure:\n%s", got)
	}
	if !strings.Contains(got, "cursor        ok") {
		t.Errorf("a meta.json failure was attributed to the cursor as well:\n%s", got)
	}
}

func TestDoctorNamesACorruptCursor(t *testing.T) {
	d := healthyDoctor()
	d.OK, d.FoundOK, d.CursorOK = false, false, false

	got := string(d.Text())
	if !strings.Contains(got, "cursor        FAILED") {
		t.Errorf("the component line does not name the cursor as the failure:\n%s", got)
	}
	if !strings.Contains(got, "meta.json     ok") {
		t.Errorf("a cursor failure was attributed to meta.json as well:\n%s", got)
	}
}

// TestDoctorJSONLIsTheReportAsOneObject: a doctor run is a single verdict,
// not a stream, so its JSONL form is the whole report on one line rather than
// a record per field.
func TestDoctorJSONLIsTheReportAsOneObject(t *testing.T) {
	blob, err := healthyDoctor().JSONL()
	if err != nil {
		t.Fatalf("JSONL: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(blob), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want the whole report on one: %s", len(lines), blob)
	}
	if !strings.Contains(lines[0], `"verb":"doctor"`) {
		t.Errorf("the report line is missing the verb field: %s", lines[0])
	}
}

// TestBytesOfScalesToTheNearestUnit pins the three magnitudes doctor prints
// an archive's size in, direct against the function rather than through a
// fixture whose byte counts could hide which branch actually ran.
func TestBytesOfScalesToTheNearestUnit(t *testing.T) {
	cases := []struct {
		n    int64
		want string
	}{
		{500, "500 B"},
		{4096, "4.0 KB"},
		{5 * 1 << 20, "5.0 MB"},
	}
	for _, tc := range cases {
		if got := bytesOf(tc.n); got != tc.want {
			t.Errorf("bytesOf(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}
