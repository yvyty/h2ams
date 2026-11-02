package tray

import (
	"errors"
	"testing"

	"github.com/yvyty/h2ams/internal/daemon"
)

func TestShouldAutostart(t *testing.T) {
	tests := []struct {
		name string
		goos string
		env  map[string]string
		want bool
	}{
		{"macOS", "darwin", nil, true},
		{"windows", "windows", nil, true},
		{"disabled", "darwin", map[string]string{NoTrayEnv: "1"}, false},
		{"explicitly enabled", "darwin", map[string]string{NoTrayEnv: "0"}, true},
		{"over ssh", "darwin", map[string]string{"SSH_CONNECTION": "1.2.3.4 22 5.6.7.8 22"}, false},
		{"linux with X11", "linux", map[string]string{"DISPLAY": ":0"}, true},
		{"linux with Wayland", "linux", map[string]string{"WAYLAND_DISPLAY": "wayland-0"}, true},
		{"headless linux", "linux", nil, false},
	}
	for _, tt := range tests {
		getenv := func(k string) string { return tt.env[k] }
		if got := ShouldAutostart(tt.goos, getenv); got != tt.want {
			t.Errorf("%s: ShouldAutostart = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestLockIsExclusive(t *testing.T) {
	t.Setenv(daemon.StateDirEnv, t.TempDir())
	if Running() {
		t.Fatal("Running before any lock")
	}
	release, err := Lock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Lock(); !errors.Is(err, ErrRunning) {
		t.Errorf("second Lock = %v, want ErrRunning", err)
	}
	if !Running() {
		t.Error("Running = false while locked")
	}
	release()
	if Running() {
		t.Error("Running = true after release")
	}
	release2, err := Lock()
	if err != nil {
		t.Errorf("Lock after release: %v", err)
	} else {
		release2()
	}
}
