package tray

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/yvyty/h2ams/internal/daemon"
)

// TestTrayBinarySmoke starts the real h2ams-tray and checks that it stays up
// and that a second copy exits at once. It needs a desktop session (on Linux:
// a display and a D-Bus session bus), so it is opt-in:
//
//	H2AMS_TEST_TRAY=1 go test -run TrayBinarySmoke ./internal/tray/
//
// CI runs it under xvfb-run and dbus-run-session on Linux.
func TestTrayBinarySmoke(t *testing.T) {
	if os.Getenv("H2AMS_TEST_TRAY") != "1" {
		t.Skip("set H2AMS_TEST_TRAY=1 to start the real tray (needs a desktop session)")
	}
	work := t.TempDir()
	t.Setenv(daemon.StateDirEnv, filepath.Join(work, "state"))
	exe := ""
	if runtime.GOOS == "windows" {
		exe = ".exe"
	}
	// h2ams-tray refuses to start without h2ams next to it.
	for _, name := range []string{"h2ams", "h2ams-tray"} {
		out := filepath.Join(work, name+exe)
		if b, err := exec.Command("go", "build", "-o", out, "github.com/yvyty/h2ams/cmd/"+name).CombinedOutput(); err != nil {
			t.Fatalf("build %s: %v\n%s", name, err, b)
		}
	}
	trayBin := filepath.Join(work, "h2ams-tray"+exe)
	logPath := filepath.Join(work, "tray.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()

	first := exec.Command(trayBin)
	first.Stdout, first.Stderr = logFile, logFile
	if err := first.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- first.Wait() }()
	t.Cleanup(func() {
		_ = first.Process.Kill()
		<-exited
	})

	select {
	case err := <-exited:
		log, _ := os.ReadFile(logPath)
		t.Fatalf("tray exited after starting: %v\n%s", err, log)
	case <-time.After(10 * time.Second):
	}
	if log, _ := os.ReadFile(logPath); len(log) > 0 {
		t.Errorf("tray wrote to its log while idle:\n%s", log)
	}

	second := exec.Command(trayBin)
	done := make(chan error, 1)
	start := time.Now()
	if err := second.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { done <- second.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("second tray exited with %v, want 0", err)
		}
		t.Logf("second tray exited after %v", time.Since(start))
	case <-time.After(5 * time.Second):
		_ = second.Process.Kill()
		t.Error("second tray kept running; the single-instance lock did not work")
	}
}
