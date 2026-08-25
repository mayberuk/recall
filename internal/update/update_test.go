package update

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNewerComparesReleasesAndRefusesWhatItCannotParse(t *testing.T) {
	for _, c := range []struct {
		tag, current string
		want         bool
		why          string
	}{
		{"v1.2.0", "v1.1.0", true, "a later minor is newer"},
		{"v1.1.1", "v1.1.0", true, "a later patch is newer"},
		{"v2.0.0", "v1.9.9", true, "major dominates"},
		{"v1.1.0", "v1.1.0", false, "the same release is not newer"},
		{"v1.0.0", "v1.1.0", false, "an older release is not newer"},
		{"v1.10.0", "v1.9.0", true, "10 beats 9, which string comparison gets wrong"},
		{"v1.1.0", "dev", false, "a local build was never installed from a release"},
		{"", "v1.1.0", false, "an empty tag is not a release"},
		{"v1.1", "v1.1.0", false, "a two-part tag is not comparable"},
		{"v1.1.x", "v1.1.0", false, "a non-numeric part is not comparable"},
		{"v1.1.-1", "v1.1.0", false, "a negative part is not a version"},
	} {
		if got := Newer(c.tag, c.current); got != c.want {
			t.Errorf("Newer(%q, %q) = %v, want %v: %s", c.tag, c.current, got, c.want, c.why)
		}
	}
}

// The stated reason for a local build never being nagged is that its owner did
// not install it from a release. That has to hold in both directions.
func TestADevBuildIsNeverBehindOrAhead(t *testing.T) {
	if Newer("v9.9.9", "dev") || Newer("dev", "v0.0.1") {
		t.Error("a dev build must not compare against a release in either direction")
	}
}

func TestNoticeSaysNothingWhenThereIsNothingToSay(t *testing.T) {
	if got := Notice("v1.1.0", State{Latest: "v1.1.0"}); got != "" {
		t.Errorf("current version produced a notice: %q", got)
	}
	if got := Notice("v1.1.0", State{}); got != "" {
		t.Errorf("an unchecked state produced a notice: %q", got)
	}
	got := Notice("v1.1.0", State{Latest: "v1.2.0"})
	for _, want := range []string{"1.2.0", "1.1.0", "recall update"} {
		if !strings.Contains(got, want) {
			t.Errorf("notice %q does not name %q", got, want)
		}
	}
	if strings.Contains(got, "v1.2.0") {
		t.Errorf("notice should print the version without the tag's v: %q", got)
	}
}

func TestLoadTreatsEveryBrokenCacheAsNothingChecked(t *testing.T) {
	for _, c := range []struct{ name, body string }{
		{"absent", ""},
		{"truncated", `{"latest":"v1.2`},
		{"wrong shape", `["not an object"]`},
		{"empty", ``},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			if c.name != "absent" {
				if err := os.WriteFile(filepath.Join(dir, stateFile), []byte(c.body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if got := Load(dir); got != (State{}) {
				t.Errorf("a %s cache produced %+v, want the zero state", c.name, got)
			}
		})
	}
}

func TestSaveThenLoadRoundTrips(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "not-created-yet")
	want := State{Latest: "v1.2.0", CheckedAt: time.Now().UTC().Truncate(time.Second)}
	if err := Save(dir, want); err != nil {
		t.Fatalf("Save into a missing directory: %v", err)
	}
	if got := Load(dir); !got.CheckedAt.Equal(want.CheckedAt) || got.Latest != want.Latest {
		t.Errorf("round trip lost data: got %+v, want %+v", got, want)
	}
}

// terminal reports itself as a character device, which is what style.IsTerminal
// asks. A bytes.Buffer cannot, which is exactly why the notice is invisible to
// every test that does not opt in.
type terminal struct{ bytes.Buffer }

func (t *terminal) Stat() (os.FileInfo, error) { return charDevice{}, nil }

type charDevice struct{ os.FileInfo }

func (charDevice) Mode() os.FileMode { return os.ModeCharDevice }

func TestNagWritesOnlyToATerminal(t *testing.T) {
	dir := t.TempDir()
	if err := Save(dir, State{Latest: "v9.9.9"}); err != nil {
		t.Fatal(err)
	}

	var pipe bytes.Buffer
	if Nag(&pipe, "v1.1.0", dir, time.Now()) || pipe.Len() != 0 {
		t.Errorf("nagged into a pipe: %q", pipe.String())
	}

	tty := &terminal{}
	if !Nag(tty, "v1.1.0", dir, time.Now()) {
		t.Fatal("did not nag on a terminal with a newer release cached")
	}
	if !strings.Contains(tty.String(), "9.9.9") {
		t.Errorf("notice does not name the new version: %q", tty.String())
	}
}

func TestNagIsSilencedByTheEnvironment(t *testing.T) {
	dir := t.TempDir()
	if err := Save(dir, State{Latest: "v9.9.9"}); err != nil {
		t.Fatal(err)
	}
	// Empty is not set, matching NO_COLOR: an unset-by-emptying variable is not
	// a preference.
	t.Setenv(SilenceEnv, "")
	tty := &terminal{}
	if !Nag(tty, "v1.1.0", dir, time.Now()) {
		t.Error("an empty value silenced the notice; only a non-empty value should")
	}

	t.Setenv(SilenceEnv, "1")
	quiet := &terminal{}
	if Nag(quiet, "v1.1.0", dir, time.Now()) || quiet.Len() != 0 {
		t.Errorf("%s=1 did not silence the notice: %q", SilenceEnv, quiet.String())
	}
}

func TestNagRepeatsAtMostOncePerDay(t *testing.T) {
	dir := t.TempDir()
	if err := Save(dir, State{Latest: "v9.9.9"}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if !Nag(&terminal{}, "v1.1.0", dir, now) {
		t.Fatal("the first notice did not go out")
	}
	if Nag(&terminal{}, "v1.1.0", dir, now.Add(23*time.Hour)) {
		t.Error("notified again inside the quiet period")
	}
	if !Nag(&terminal{}, "v1.1.0", dir, now.Add(25*time.Hour)) {
		t.Error("did not notify again after the quiet period")
	}
}

func TestNagSaysNothingWhenTheCachedReleaseIsNotNewer(t *testing.T) {
	dir := t.TempDir()
	if err := Save(dir, State{Latest: "v1.1.0"}); err != nil {
		t.Fatal(err)
	}
	tty := &terminal{}
	if Nag(tty, "v1.1.0", dir, time.Now()) || tty.Len() != 0 {
		t.Errorf("nagged about the version already running: %q", tty.String())
	}
}

// A notice that fires but cannot record that it fired must still be a notice,
// not a failure, and must not become a notice on every single command.
func TestNagStillPrintsWhenTheCacheCannotBeWritten(t *testing.T) {
	dir := t.TempDir()
	if err := Save(dir, State{Latest: "v9.9.9"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Skip("cannot make the directory read-only here")
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	if os.Geteuid() == 0 {
		t.Skip("root ignores the permission bits this asserts on")
	}
	tty := &terminal{}
	if !Nag(tty, "v1.1.0", dir, time.Now()) {
		t.Error("an unwritable cache suppressed the notice entirely")
	}
}

func TestSilencedFollowsTheNoColorConvention(t *testing.T) {
	os.Unsetenv(SilenceEnv)
	if Silenced() {
		t.Error("unset should not silence")
	}
	t.Setenv(SilenceEnv, "")
	if Silenced() {
		t.Error("empty should not silence, matching NO_COLOR")
	}
	t.Setenv(SilenceEnv, "0")
	if !Silenced() {
		t.Error("any non-empty value should silence, including \"0\"")
	}
}

func TestStateSerialisesTheFieldsTheCacheNeeds(t *testing.T) {
	b, err := json.Marshal(State{Latest: "v1.2.0", CheckedAt: time.Unix(0, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	// All three, including a zero notified_at: encoding/json cannot omit a zero
	// time.Time, so the struct carries no omitempty claiming otherwise.
	for _, want := range []string{`"latest"`, `"checked_at"`, `"notified_at"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("serialised state %s is missing %s", b, want)
		}
	}
}

func TestSaveReportsADirectoryItCannotCreate(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores the permission bits this asserts on")
	}
	parent := t.TempDir()
	if err := os.Chmod(parent, 0o500); err != nil {
		t.Skip("cannot make the directory read-only here")
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o700) })
	if err := Save(filepath.Join(parent, "nope"), State{Latest: "v1.2.0"}); err == nil {
		t.Error("Save into an uncreatable directory reported success")
	}
}
