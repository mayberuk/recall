package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// API is where releases are published. A variable so tests point it at a local
// server rather than the internet.
var API = "https://api.github.com/repos/mayberuk/recall/releases"

// Downloads is where release assets live, likewise overridable.
var Downloads = "https://github.com/mayberuk/recall/releases/download"

// timeout bounds every request. A check that hangs is worse than a check that
// fails: the verbs that run it are interactive, and a user waiting on a
// version number has already lost more than the answer is worth.
const timeout = 10 * time.Second

// Release is the part of a GitHub release recall reads.
type Release struct {
	Tag string
}

// Latest resolves the newest published tag.
func Latest(ctx context.Context, c *http.Client) (Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, API+"/latest", nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := c.Do(req)
	if err != nil {
		return Release{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("the releases API answered %s", resp.Status)
	}
	var body struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return Release{}, fmt.Errorf("cannot read the releases API response: %w", err)
	}
	if body.TagName == "" {
		return Release{}, fmt.Errorf("the releases API returned no tag_name")
	}
	return Release{Tag: body.TagName}, nil
}

// Client is the http.Client every call here uses.
func Client() *http.Client { return &http.Client{Timeout: timeout} }

// AssetName is the release asset for this build's platform, matching the names
// the release workflow cross-compiles to.
func AssetName(tag string) string { return assetName(tag, runtime.GOOS, runtime.GOARCH) }

// assetName takes the platform rather than reading it, so the Windows spelling
// is reachable from a test on any machine. A name this file gets wrong is an
// update that 404s on one platform only, which is exactly the bug a
// build-tagged branch hides until somebody reports it.
func assetName(tag, goos, goarch string) string {
	ext := ""
	if goos == "windows" {
		ext = ".exe"
	}
	return fmt.Sprintf("recall-%s-%s-%s%s", tag, goos, goarch, ext)
}

// Install downloads the asset for tag, checks it against the release's own
// checksums.txt, and replaces the binary at dst.
//
// The verification is not optional and not a flag. install.sh has always
// checked the sha256 before putting anything on disk, and a second install
// path that skipped it would make that guarantee a matter of which one you
// happened to use.
func Install(ctx context.Context, c *http.Client, tag, dst string, progress io.Writer) error {
	asset := AssetName(tag)
	base := Downloads + "/" + tag

	sums, err := get(ctx, c, base+"/checksums.txt")
	if err != nil {
		return fmt.Errorf("the release publishes no checksums.txt: %w", err)
	}
	want := checksumFor(string(sums), asset)
	if want == "" {
		return fmt.Errorf("checksums.txt does not list %s", asset)
	}

	fmt.Fprintf(progress, "  downloading %s\n", asset)
	body, err := get(ctx, c, base+"/"+asset)
	if err != nil {
		return fmt.Errorf("cannot download %s: %w", asset, err)
	}
	sum := sha256.Sum256(body)
	if got := hex.EncodeToString(sum[:]); got != want {
		return fmt.Errorf("sha256 mismatch for %s: the release says %s, the bytes are %s", asset, want, got)
	}
	fmt.Fprintln(progress, "  sha256 verified")

	return replace(dst, body)
}

// replace swaps the running binary for the downloaded one, writing to the same
// directory first so the final step is a rename on one filesystem rather than
// a copy that can half-succeed.
func replace(dst string, body []byte) error {
	return replaceOn(dst, body, runtime.GOOS == "windows")
}

// replaceOn takes moveAside rather than reading runtime.GOOS, so both halves
// run on one machine. The Windows half is the one with a rollback in it, and an
// untested rollback is a rollback that does not work.
func replaceOn(dst string, body []byte, moveAside bool) error {
	dir := filepath.Dir(dst)
	tmp, err := os.CreateTemp(dir, ".recall-update-")
	if err != nil {
		return fmt.Errorf("cannot write beside %s: %w", dst, err)
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, 0o755); err != nil {
		return err
	}

	// Windows refuses to rename over a running image, so the outgoing binary is
	// moved aside first and swept on the next run. Every other platform renames
	// straight over it: the running process keeps its open inode.
	if moveAside {
		old := dst + ".old"
		_ = os.Remove(old)
		if err := os.Rename(dst, old); err != nil {
			return fmt.Errorf("cannot move the running binary aside: %w", err)
		}
		if err := os.Rename(name, dst); err != nil {
			_ = os.Rename(old, dst)
			return err
		}
		return nil
	}
	return os.Rename(name, dst)
}

// SweepOld removes the binary a previous Windows update moved aside. A no-op
// everywhere else, and never an error worth reporting: a leftover file is
// untidy, not broken.
func SweepOld(dst string) { sweepOld(dst, runtime.GOOS == "windows") }

func sweepOld(dst string, moveAside bool) {
	if moveAside {
		_ = os.Remove(dst + ".old")
	}
}

func get(ctx context.Context, c *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s answered %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 128<<20))
}

// checksumFor reads one line of sha256sum output. The leading `*` marks a file
// read in binary mode, which sha256sum writes on some platforms and not others.
func checksumFor(sums, asset string) string {
	for _, line := range strings.Split(sums, "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && (f[1] == asset || f[1] == "*"+asset) {
			return f[0]
		}
	}
	return ""
}
