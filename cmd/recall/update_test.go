package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mayberuk/recall/internal/update"
)

// releasesAPI stands in for GitHub, so nothing here depends on the network.
func releasesAPI(t *testing.T, tag string) {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"tag_name":"` + tag + `"}`))
	}))
	t.Cleanup(s.Close)
	old := update.API
	update.API = s.URL + "/releases"
	t.Cleanup(func() { update.API = old })
}

// atVersion pins the binary's reported version for one test. version is a
// package variable the release build stamps with ldflags.
func atVersion(t *testing.T, v string) {
	t.Helper()
	old := version
	version = v
	t.Cleanup(func() { version = old })
}

func TestUpdateCheckReportsANewerRelease(t *testing.T) {
	t.Setenv("RECALL_HOME", t.TempDir())
	t.Setenv(update.SilenceEnv, "")
	releasesAPI(t, "v9.9.9")
	atVersion(t, "v1.1.0")

	var out, errOut bytes.Buffer
	if err := runUpdate([]string{"--check"}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"v9.9.9", "v1.1.0", "recall update"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output %q does not name %q", out.String(), want)
		}
	}
}

func TestUpdateCheckSaysSoWhenAlreadyCurrent(t *testing.T) {
	t.Setenv("RECALL_HOME", t.TempDir())
	t.Setenv(update.SilenceEnv, "")
	releasesAPI(t, "v1.1.0")
	atVersion(t, "v1.1.0")

	var out, errOut bytes.Buffer
	if err := runUpdate([]string{"--check"}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "latest release") {
		t.Errorf("output %q does not say the binary is current", out.String())
	}
}

// A binary compiled from a checkout was not installed from a release, and
// replacing it would throw away whatever its owner was testing.
func TestUpdateDeclinesToReplaceASourceBuild(t *testing.T) {
	t.Setenv("RECALL_HOME", t.TempDir())
	t.Setenv(update.SilenceEnv, "")
	releasesAPI(t, "v9.9.9")
	atVersion(t, "dev")

	var out, errOut bytes.Buffer
	if err := runUpdate(nil, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "built from source") {
		t.Errorf("output %q does not explain why it stopped", got)
	}
	if !strings.Contains(got, "v9.9.9") {
		t.Errorf("output %q does not name the latest release anyway", got)
	}
}

// The check is the only thing in recall that opens a socket, so the switch that
// turns it off has to be honoured by the verb that does the opening.
func TestUpdateRefusesWhenTheCheckIsSilenced(t *testing.T) {
	t.Setenv(update.SilenceEnv, "1")
	var out, errOut bytes.Buffer
	err := runUpdate([]string{"--check"}, &out, &errOut)
	if err == nil {
		t.Fatal("ran a network check with the silence switch set")
	}
	if !strings.Contains(err.Error(), update.SilenceEnv) {
		t.Errorf("error %q does not name the variable to unset", err)
	}
	if out.Len() != 0 {
		t.Errorf("wrote to stdout anyway: %q", out.String())
	}
}

func TestUpdateReportsAnUnreachableApi(t *testing.T) {
	t.Setenv("RECALL_HOME", t.TempDir())
	t.Setenv(update.SilenceEnv, "")
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer s.Close()
	old := update.API
	update.API = s.URL + "/releases"
	defer func() { update.API = old }()

	var out, errOut bytes.Buffer
	if err := runUpdate([]string{"--check"}, &out, &errOut); err == nil {
		t.Fatal("an unreachable API reported success")
	}
}

// The check records what it learned so that every other verb can mention an
// update without asking anyone. If this stops happening the notice goes quiet
// and nothing else fails.
func TestUpdateRecordsWhatItLearnedForTheOtherVerbs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("RECALL_HOME", home)
	t.Setenv(update.SilenceEnv, "")
	releasesAPI(t, "v9.9.9")
	atVersion(t, "v1.1.0")

	var out, errOut bytes.Buffer
	if err := runUpdate([]string{"--check"}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	if got := update.Load(home); got.Latest != "v9.9.9" {
		t.Errorf("cached state is %+v, want Latest v9.9.9", got)
	}
	if update.Load(home).CheckedAt.IsZero() {
		t.Error("the check did not record when it ran")
	}
}

// The one guarantee everything else rests on. Every size recall reports, the
// --max-bytes cap, the budget-fitting search and the differential baseline all
// count bytes on stdout, so a notice there would corrupt all four.
func TestTheUpdateNoticeNeverReachesStdout(t *testing.T) {
	home := t.TempDir()
	t.Setenv("RECALL_HOME", home)
	t.Setenv(update.SilenceEnv, "")
	atVersion(t, "v1.1.0")
	if err := update.Save(home, update.State{Latest: "v9.9.9"}); err != nil {
		t.Fatal(err)
	}

	for _, verb := range []string{"find", "turns", "when", "show", "doctor", "guide"} {
		t.Run(verb, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			run([]string{verb, "anything"}, &stdout, &stderr)
			if strings.Contains(stdout.String(), "recall update") ||
				strings.Contains(stdout.String(), "is available") {
				t.Errorf("the update notice reached stdout for %s:\n%s", verb, stdout.String())
			}
		})
	}
}

// The notice is invisible unless a person is looking at it. A pipe is what an
// agent, a script and every harness in this repository gets.
func TestTheUpdateNoticeStaysQuietOnAPipe(t *testing.T) {
	home := t.TempDir()
	t.Setenv("RECALL_HOME", home)
	t.Setenv(update.SilenceEnv, "")
	atVersion(t, "v1.1.0")
	if err := update.Save(home, update.State{Latest: "v9.9.9"}); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	run([]string{"guide"}, &stdout, &stderr)
	if strings.Contains(stderr.String(), "is available") {
		t.Errorf("nagged into a pipe: %q", stderr.String())
	}
}

// `recall update` says it itself, and `mcp serve` owns a protocol stream whose
// stderr belongs to whichever client spawned it.
func TestTheUpdateNoticeSkipsTheVerbsThatOwnTheirOwnOutput(t *testing.T) {
	home := t.TempDir()
	t.Setenv("RECALL_HOME", home)
	t.Setenv(update.SilenceEnv, "")
	atVersion(t, "v1.1.0")
	if err := update.Save(home, update.State{Latest: "v9.9.9"}); err != nil {
		t.Fatal(err)
	}
	for _, verb := range []string{"update", "mcp"} {
		tty := &fakeTTY{}
		noticeAnUpdate(tty, verb)
		if tty.Len() != 0 {
			t.Errorf("%s was nagged at: %q", verb, tty.String())
		}
	}
	// The same terminal, on a verb that should be nagged, proves the check above
	// is measuring the verb and not a broken terminal stub.
	tty := &fakeTTY{}
	noticeAnUpdate(tty, "find")
	if tty.Len() == 0 {
		t.Error("no notice on a verb that should carry one; the negative cases above prove nothing")
	}
}

type fakeTTY struct{ bytes.Buffer }

func (f *fakeTTY) Stat() (os.FileInfo, error) { return charDev{}, nil }

type charDev struct{ os.FileInfo }

func (charDev) Mode() os.FileMode { return os.ModeCharDevice }

func TestUpdateRejectsAnUnknownFlag(t *testing.T) {
	var out, errOut bytes.Buffer
	if err := runUpdate([]string{"--nope"}, &out, &errOut); err == nil {
		t.Error("an unknown flag was accepted")
	}
}

func TestDoctorDoesNotReachTheNetworkWithoutATerminal(t *testing.T) {
	t.Setenv("RECALL_HOME", t.TempDir())
	t.Setenv(update.SilenceEnv, "")
	old := update.API
	update.API = "http://127.0.0.1:1/releases" // refuses instantly if ever dialled
	t.Cleanup(func() { update.API = old })

	var buf bytes.Buffer
	start := time.Now()
	refreshUpdateState(&buf)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("refresh took %v against a pipe; it should not have dialled at all", elapsed)
	}
}
