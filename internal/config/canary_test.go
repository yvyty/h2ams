package config

import (
	"runtime"
	"testing"
)

// Deliberately failing test (Branch 4, task 4.1.1.4): proves CI turns red on
// the matching OS only. This PR is closed without merging.
func TestCICanary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Fatal("deliberate failure on Windows: CI must report this")
	}
}
