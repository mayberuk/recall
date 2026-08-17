package archive

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// withParallelRead cuts the floor so a small file still reaches the split path,
// and pins GOMAXPROCS so a two-core runner exercises the same number of chunks
// as a sixteen-core developer machine — the chunk count is what the boundary
// arithmetic turns on.
func withParallelRead(t *testing.T, bytesPerWorker int64) {
	t.Helper()
	wasMin := minParallelRead
	wasProcs := runtime.GOMAXPROCS(16)
	t.Cleanup(func() {
		minParallelRead = wasMin
		runtime.GOMAXPROCS(wasProcs)
	})
	minParallelRead = bytesPerWorker
}

// pattern is content whose every byte is a function of its offset, so a chunk
// read from the wrong place or left unread shows up as a wrong byte rather than
// as a plausible one.
func pattern(n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = byte(i*31 + i/251)
	}
	return out
}

func writeTemp(t *testing.T, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestReadWholeReturnsTheFileWhateverItIsCutInto is the correctness argument for
// reading a tier from several goroutines at once. Sizes that divide evenly into
// chunks and sizes that leave a remainder are both here, because the last chunk
// is the one that has to absorb the remainder and the one a naive split drops.
func TestReadWholeReturnsTheFileWhateverItIsCutInto(t *testing.T) {
	for _, size := range []int{0, 1, 63, 64, 65, 1000, 1024, 4097} {
		for _, per := range []int64{1, 7, 64, 4 << 20} {
			name := fmt.Sprintf("%d bytes in %d-byte chunks", size, per)
			t.Run(name, func(t *testing.T) {
				withParallelRead(t, per)
				want := pattern(size)
				got, err := readWhole(writeTemp(t, want))
				if err != nil {
					t.Fatalf("readWhole: %v", err)
				}
				if !bytes.Equal(got, want) {
					t.Fatalf("read %d bytes, want %d; first difference at %d",
						len(got), len(want), firstDiff(got, want))
				}
			})
		}
	}
}

// TestReadWholeReportsAMissingFile keeps the caller's own not-exists handling
// working: readTier turns that into "no tier yet", which is an empty archive
// rather than a broken one.
func TestReadWholeReportsAMissingFile(t *testing.T) {
	_, err := readWhole(filepath.Join(t.TempDir(), "absent"))
	if !os.IsNotExist(err) {
		t.Fatalf("error %v, want a not-exists error", err)
	}
}

// TestReadWholeRefusesAPathItCannotRead is about what a failure must not look
// like. The buffer is allocated before anything is read into it, so a read error
// that went unreported would hand back the right number of zero bytes — a tier
// that decodes as empty, which the archive would take for a store with nothing
// in it rather than one it could not read.
func TestReadWholeRefusesAPathItCannotRead(t *testing.T) {
	for _, per := range []int64{1, 4 << 20} {
		t.Run(fmt.Sprintf("%d-byte chunks", per), func(t *testing.T) {
			withParallelRead(t, per)
			got, err := readWhole(t.TempDir())
			if err == nil {
				t.Fatalf("reading a directory returned %d bytes and no error", len(got))
			}
		})
	}
}

func firstDiff(a, b []byte) int {
	for i := range min(len(a), len(b)) {
		if a[i] != b[i] {
			return i
		}
	}
	return min(len(a), len(b))
}
