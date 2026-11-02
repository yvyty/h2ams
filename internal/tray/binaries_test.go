package tray

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestExeName(t *testing.T) {
	if got := exeName("h2ams-tray", "windows"); got != "h2ams-tray.exe" {
		t.Errorf("windows: %q", got)
	}
	for _, goos := range []string{"darwin", "linux"} {
		if got := exeName("h2ams", goos); got != "h2ams" {
			t.Errorf("%s: %q", goos, got)
		}
	}
}

func TestSiblingOf(t *testing.T) {
	dir := t.TempDir()
	self := filepath.Join(dir, exeName("h2ams", runtime.GOOS))
	tray := exeName("h2ams-tray", runtime.GOOS)
	if _, ok := siblingOf(self, tray); ok {
		t.Fatal("found a sibling that doesn't exist")
	}
	if err := os.Mkdir(filepath.Join(dir, tray), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, ok := siblingOf(self, tray); ok {
		t.Fatal("a directory counted as the binary")
	}
	os.Remove(filepath.Join(dir, tray))
	want := filepath.Join(dir, tray)
	if err := os.WriteFile(want, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, ok := siblingOf(self, tray); !ok || got != want {
		t.Errorf("siblingOf = %q, %v; want %q", got, ok, want)
	}
}
