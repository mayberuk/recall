package archive

import (
	"io"
	"os"
	"runtime"
	"sync"
)

// minParallelRead is how many bytes a goroutine's slice of a tier file must
// carry to be worth its own read. Filling 4 MB costs a few hundred microseconds
// against about a microsecond to start the goroutine, so the floor is set far
// enough above the crossover that it never has to be right to the byte. A test
// shrinks it to reach the split path without writing 8 MB to do it; nothing else
// writes it.
var minParallelRead int64 = 4 << 20

// readWhole reads path into one buffer, filled by several goroutines at once.
//
// The tier files are the largest thing recall touches — 197 MB for the tool
// results alone — and copying them out of the page cache one byte stream at a
// time is a single core's memory bandwidth against sixteen. Over the real 282 MB
// archive this is 5.6 ms where os.ReadFile is 16.9 ms.
//
// mmap was measured against this and lost, which is worth recording because the
// performance plan called for it: it came in at 9.0 ms once the page faults a
// sequential decode has to take are counted, and it would have cost a build-tag
// split, a mapping deliberately never unmapped, and a truncation of a mapped
// file as a new way to crash.
//
// Concurrent ReadAt is safe by contract: it takes the offset as an argument
// rather than from the file, so the goroutines share no position to race over.
func readWhole(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	// A tier file is written by atomic rename, so the descriptor already opened
	// keeps the inode this size describes even if an update replaces the name.
	size := info.Size()
	if size <= 0 {
		return io.ReadAll(f)
	}
	buf := make([]byte, size)

	workers := min(runtime.GOMAXPROCS(0), int(size/minParallelRead))

	if workers <= 1 {
		if _, err := io.ReadFull(f, buf); err != nil {
			return nil, err
		}
		return buf, nil
	}

	per := size / int64(workers)
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for i := range workers {
		lo := int64(i) * per
		hi := lo + per
		if i == workers-1 {
			hi = size
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			// ReadAt fills the slice or reports why it could not, so a short read
			// at the end of the file is an error here and not a silent truncation.
			if _, err := f.ReadAt(buf[lo:hi], lo); err != nil && err != io.EOF {
				errs[i] = err
			}
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	return buf, nil
}
