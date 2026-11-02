package logs

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func writeLines(t *testing.T, path string, lines ...string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, l := range lines {
		fmt.Fprintln(f, l)
	}
}

func TestTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	writeLines(t, path, "one", "two", "three", "four")

	tests := []struct {
		n    int
		want string
	}{
		{2, "three\nfour\n"},
		{1, "four\n"},
		{10, "one\ntwo\nthree\nfour\n"},
		{0, "one\ntwo\nthree\nfour\n"},
	}
	for _, tt := range tests {
		var b bytes.Buffer
		off, err := Tail(&b, path, tt.n)
		if err != nil {
			t.Fatal(err)
		}
		if b.String() != tt.want {
			t.Errorf("Tail(n=%d) = %q, want %q", tt.n, b.String(), tt.want)
		}
		if off != int64(len("one\ntwo\nthree\nfour\n")) {
			t.Errorf("Tail(n=%d) offset = %d", tt.n, off)
		}
	}
}

func TestTailAcrossChunks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "big.log")
	long := strings.Repeat("x", 40<<10) // longer than one read chunk
	writeLines(t, path, "first", long, "last")
	var b bytes.Buffer
	if _, err := Tail(&b, path, 2); err != nil {
		t.Fatal(err)
	}
	if b.String() != long+"\nlast\n" {
		t.Errorf("Tail across chunks returned %d bytes", b.Len())
	}
}

func TestTailWithoutTrailingNewline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	os.WriteFile(path, []byte("a\nb\npartial"), 0o600)
	var b bytes.Buffer
	Tail(&b, path, 2)
	if b.String() != "b\npartial" {
		t.Errorf("got %q", b.String())
	}
}

func TestTailMissingFile(t *testing.T) {
	if _, err := Tail(&bytes.Buffer{}, filepath.Join(t.TempDir(), "none"), 5); !os.IsNotExist(err) {
		t.Errorf("err = %v, want not-exist", err)
	}
}

// syncBuffer is a bytes.Buffer safe for Follow's goroutine and the test.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func follow(t *testing.T, path string, offset int64) (*syncBuffer, func()) {
	t.Helper()
	var out syncBuffer
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Follow(ctx, &out, path, offset, 10*time.Millisecond) }()
	return &out, func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Follow: %v", err)
		}
	}
}

func eventually(t *testing.T, buf *syncBuffer, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if buf.String() == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("followed output = %q, want %q", buf.String(), want)
}

func TestFollowAppends(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	writeLines(t, path, "old")
	off, _ := Tail(&bytes.Buffer{}, path, 0)

	out, stop := follow(t, path, off)
	defer stop()
	writeLines(t, path, "new 1", "new 2")
	eventually(t, out, "new 1\nnew 2\n")
}

func TestFollowWaitsForFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "later.log")
	out, stop := follow(t, path, 0)
	defer stop()
	time.Sleep(30 * time.Millisecond)
	writeLines(t, path, "hello")
	eventually(t, out, "hello\n")
}

func TestFollowAfterRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	writeLines(t, path, "before")
	off, _ := Tail(&bytes.Buffer{}, path, 0)

	out, stop := follow(t, path, off)
	defer stop()
	os.Rename(path, path+".1")
	writeLines(t, path, "after")
	eventually(t, out, "after\n")
}

func TestOpenRotatesLargeLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs", "app.log")
	f, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	f.Write(bytes.Repeat([]byte("x"), MaxSize+1))
	f.Close()

	f, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	if info, _ := os.Stat(path); info.Size() != 0 {
		t.Errorf("new log size = %d, want 0", info.Size())
	}
	if info, err := os.Stat(path + ".1"); err != nil || info.Size() != MaxSize+1 {
		t.Errorf("rotated log missing or wrong size: %v", err)
	}
}
