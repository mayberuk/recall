package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// serve stands in for GitHub. Every test here runs against it: a suite that
// reached the real API would be measuring somebody else's uptime.
func serve(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(h)
	t.Cleanup(s.Close)
	oldAPI, oldDL := API, Downloads
	API, Downloads = s.URL+"/releases", s.URL+"/download"
	t.Cleanup(func() { API, Downloads = oldAPI, oldDL })
	return s
}

func TestLatestReadsTheTag(t *testing.T) {
	serve(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/releases/latest") {
			t.Errorf("asked for %s, want /releases/latest", r.URL.Path)
		}
		if got := r.Header.Get("Accept"); got != "application/vnd.github+json" {
			t.Errorf("Accept header is %q", got)
		}
		w.Write([]byte(`{"tag_name":"v2.0.0","name":"v2.0.0"}`))
	})
	rel, err := Latest(context.Background(), Client())
	if err != nil {
		t.Fatal(err)
	}
	if rel.Tag != "v2.0.0" {
		t.Errorf("tag = %q, want v2.0.0", rel.Tag)
	}
}

func TestLatestReportsWhatWentWrong(t *testing.T) {
	for _, c := range []struct {
		name string
		h    http.HandlerFunc
		want string
	}{
		{"rate limited", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
		}, "403"},
		{"not json", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`<html>no</html>`))
		}, "cannot read"},
		{"no tag", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{}`))
		}, "no tag_name"},
	} {
		t.Run(c.name, func(t *testing.T) {
			serve(t, c.h)
			_, err := Latest(context.Background(), Client())
			if err == nil {
				t.Fatal("no error")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %q does not mention %q", err, c.want)
			}
		})
	}
}

func TestAssetNameMatchesWhatTheReleaseWorkflowBuilds(t *testing.T) {
	got := AssetName("v1.2.0")
	want := "recall-v1.2.0-" + runtime.GOOS + "-" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		want += ".exe"
	}
	if got != want {
		t.Errorf("AssetName = %q, want %q", got, want)
	}
}

func TestChecksumForAcceptsBothSha256sumSpellings(t *testing.T) {
	sums := "aaa  other-file\nbbb  recall-v1-linux-amd64\nccc *recall-v1-darwin-arm64\n"
	for _, c := range []struct{ asset, want string }{
		{"recall-v1-linux-amd64", "bbb"},
		{"recall-v1-darwin-arm64", "ccc"},
		{"recall-v1-windows-amd64", ""},
	} {
		if got := checksumFor(sums, c.asset); got != c.want {
			t.Errorf("checksumFor(%s) = %q, want %q", c.asset, got, c.want)
		}
	}
}

// release serves a checksums.txt and an asset whose bytes are `body`, with the
// digest optionally corrupted so the mismatch path can be exercised.
func release(t *testing.T, tag string, body []byte, corrupt bool) {
	t.Helper()
	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])
	if corrupt {
		digest = strings.Repeat("0", 64)
	}
	asset := AssetName(tag)
	serve(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "checksums.txt"):
			w.Write([]byte(digest + "  " + asset + "\n"))
		case strings.HasSuffix(r.URL.Path, asset):
			w.Write(body)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

func TestInstallVerifiesAndReplaces(t *testing.T) {
	body := []byte("the new binary")
	release(t, "v2.0.0", body, false)

	dst := filepath.Join(t.TempDir(), "recall")
	if err := os.WriteFile(dst, []byte("the old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	var log strings.Builder
	if err := Install(context.Background(), Client(), "v2.0.0", dst, &log); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(body) {
		t.Errorf("binary is %q, want %q", got, body)
	}
	if !strings.Contains(log.String(), "sha256 verified") {
		t.Errorf("install said nothing about verifying: %q", log.String())
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(dst)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm()&0o111 == 0 {
			t.Errorf("the replacement is not executable: %v", fi.Mode())
		}
	}
}

// The verification is the whole reason this path is allowed to write to the
// binary, so a mismatch has to leave the old one exactly where it was.
func TestInstallRefusesAMismatchAndLeavesTheBinaryAlone(t *testing.T) {
	release(t, "v2.0.0", []byte("tampered"), true)

	dst := filepath.Join(t.TempDir(), "recall")
	if err := os.WriteFile(dst, []byte("the old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := Install(context.Background(), Client(), "v2.0.0", dst, &strings.Builder{})
	if err == nil {
		t.Fatal("a sha256 mismatch was installed")
	}
	if !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Errorf("error %q does not name the mismatch", err)
	}
	got, _ := os.ReadFile(dst)
	if string(got) != "the old binary" {
		t.Errorf("the binary was replaced anyway: %q", got)
	}
}

func TestInstallRefusesAReleaseThatDoesNotListTheAsset(t *testing.T) {
	serve(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "checksums.txt") {
			w.Write([]byte("aaa  some-other-project\n"))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	dst := filepath.Join(t.TempDir(), "recall")
	os.WriteFile(dst, []byte("old"), 0o755)
	err := Install(context.Background(), Client(), "v2.0.0", dst, &strings.Builder{})
	if err == nil || !strings.Contains(err.Error(), "does not list") {
		t.Fatalf("err = %v, want one naming the missing asset", err)
	}
}

func TestInstallRefusesAReleaseWithNoChecksums(t *testing.T) {
	serve(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNotFound) })
	dst := filepath.Join(t.TempDir(), "recall")
	os.WriteFile(dst, []byte("old"), 0o755)
	err := Install(context.Background(), Client(), "v2.0.0", dst, &strings.Builder{})
	if err == nil || !strings.Contains(err.Error(), "checksums.txt") {
		t.Fatalf("err = %v, want one naming checksums.txt", err)
	}
}

func TestReplaceReportsAnUnwritableDestination(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "recall")
	if err := os.WriteFile(dst, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores the permission bits this asserts on")
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Skip("cannot make the directory read-only here")
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	if err := replace(dst, []byte("new")); err == nil {
		t.Error("replace into a read-only directory reported success")
	}
}

func TestSweepOldIsHarmlessWhereThereIsNothingToSweep(t *testing.T) {
	SweepOld(filepath.Join(t.TempDir(), "recall"))
}

func TestAssetNameSpellsWindowsWithAnExe(t *testing.T) {
	for _, c := range []struct{ goos, goarch, want string }{
		{"linux", "amd64", "recall-v1.2.0-linux-amd64"},
		{"darwin", "arm64", "recall-v1.2.0-darwin-arm64"},
		{"windows", "amd64", "recall-v1.2.0-windows-amd64.exe"},
		{"windows", "arm64", "recall-v1.2.0-windows-arm64.exe"},
	} {
		if got := assetName("v1.2.0", c.goos, c.goarch); got != c.want {
			t.Errorf("assetName(%s/%s) = %q, want %q", c.goos, c.goarch, got, c.want)
		}
	}
}

// The Windows path is the one carrying a rollback, so it is the one worth
// running. Both halves are exercised here regardless of the host.
func TestReplaceOnBothPlatformStrategies(t *testing.T) {
	for _, moveAside := range []bool{false, true} {
		t.Run(map[bool]string{false: "rename over", true: "move aside first"}[moveAside], func(t *testing.T) {
			dst := filepath.Join(t.TempDir(), "recall")
			if err := os.WriteFile(dst, []byte("old"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := replaceOn(dst, []byte("new"), moveAside); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(dst)
			if err != nil || string(got) != "new" {
				t.Fatalf("binary is %q (%v), want %q", got, err, "new")
			}
			_, err = os.Stat(dst + ".old")
			if moveAside && err != nil {
				t.Error("the move-aside strategy left no .old to sweep")
			}
			if !moveAside && err == nil {
				t.Error("the rename strategy left a stray .old behind")
			}
		})
	}
}

// The move-aside strategy has to report the case it cannot recover from: the
// outgoing binary is gone by the time the swap runs, so there is nothing to
// move and nothing to roll back to.
func TestMoveAsideReportsAMissingBinary(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "recall")
	err := replaceOn(dst, []byte("new"), true)
	if err == nil {
		t.Fatal("replacing an absent binary reported success")
	}
	if !strings.Contains(err.Error(), "move the running binary aside") {
		t.Errorf("error %q does not say what it could not do", err)
	}
	if _, statErr := os.Stat(dst); statErr == nil {
		t.Error("a failed swap left a binary where there was none")
	}
}

func TestSweepOldRemovesOnlyWhereTheStrategyLeavesOne(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "recall")
	old := dst + ".old"
	for _, moveAside := range []bool{false, true} {
		if err := os.WriteFile(old, []byte("stale"), 0o644); err != nil {
			t.Fatal(err)
		}
		sweepOld(dst, moveAside)
		_, err := os.Stat(old)
		if moveAside && err == nil {
			t.Error("the leftover was not swept")
		}
		if !moveAside && err != nil {
			t.Error("swept a file on a platform that never creates one")
		}
	}
	_ = os.Remove(old)
}

func TestGetRefusesAUrlItCannotRequest(t *testing.T) {
	if _, err := get(context.Background(), Client(), "http://\x7f/bad"); err == nil {
		t.Error("a malformed URL reported success")
	}
}
