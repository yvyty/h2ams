// Package logs writes per-app log files and reads them back (tail and follow).
package logs

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"
)

// MaxSize is the size at which Open rotates a log to "<path>.1".
const MaxSize = 10 << 20

// Open opens path for appending, creating parent directories. A log larger
// than MaxSize is first rotated to path+".1", replacing any older rotation.
func Open(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if info, err := os.Stat(path); err == nil && info.Size() > MaxSize {
		if err := os.Rename(path, path+".1"); err != nil {
			return nil, err
		}
	}
	return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
}

// Tail writes the last n lines of path to w (all lines when n <= 0) and
// returns the file offset where it stopped, for use with Follow.
func Tail(w io.Writer, path string, n int) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return 0, err
	}
	start := int64(0)
	if n > 0 {
		if start, err = lastLinesOffset(f, info.Size(), n); err != nil {
			return 0, err
		}
	}
	copied, err := io.Copy(w, io.NewSectionReader(f, start, info.Size()-start))
	return start + copied, err
}

// lastLinesOffset returns where the last n lines of a size-byte file begin.
func lastLinesOffset(f io.ReaderAt, size int64, n int) (int64, error) {
	const chunk = 32 << 10
	buf := make([]byte, chunk)
	newlines := 0
	for end := size; end > 0; {
		start := max(end-chunk, 0)
		b := buf[:end-start]
		if _, err := f.ReadAt(b, start); err != nil && !errors.Is(err, io.EOF) {
			return 0, err
		}
		for i := len(b) - 1; i >= 0; i-- {
			if b[i] != '\n' || start+int64(i) == size-1 { // a trailing newline ends the last line
				continue
			}
			if newlines++; newlines == n {
				return start + int64(i) + 1, nil
			}
		}
		end = start
	}
	return 0, nil
}

// Follow copies data appended to path after offset into w until ctx is done.
// It waits for the file to appear and starts over if the file is truncated
// or replaced (rotated).
func Follow(ctx context.Context, w io.Writer, path string, offset int64, interval time.Duration) error {
	var f *os.File
	defer func() {
		if f != nil {
			f.Close()
		}
	}()
	tick := time.NewTicker(interval)
	defer tick.Stop()

	for {
		if f == nil {
			if opened, err := os.Open(path); err == nil {
				f = opened
				if _, err := f.Seek(offset, io.SeekStart); err != nil {
					return err
				}
			}
		}
		if f != nil {
			n, err := io.Copy(w, f)
			offset += n
			if err != nil {
				return err
			}
			if replacedOrTruncated(f, path, offset) {
				f.Close()
				f, offset = nil, 0
				continue
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
	}
}

func replacedOrTruncated(f *os.File, path string, offset int64) bool {
	cur, err := f.Stat()
	if err != nil {
		return true
	}
	onDisk, err := os.Stat(path)
	if err != nil {
		return false // rotated away and not yet recreated; keep reading the old file
	}
	return !os.SameFile(cur, onDisk) || onDisk.Size() < offset
}
