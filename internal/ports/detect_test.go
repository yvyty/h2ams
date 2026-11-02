//go:build linux || darwin

package ports

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/yvyty/h2ams/internal/config"
	"github.com/yvyty/h2ams/internal/process"
	"github.com/yvyty/h2ams/internal/testutil"
)

func TestMain(m *testing.M) {
	testutil.RunHelperIfRequested()
	os.Exit(m.Run())
}

func TestDetectsListenerOnFreePort(t *testing.T) {
	out := filepath.Join(t.TempDir(), "port")
	p := process.Start(process.Spec{
		Name:    "listener",
		Command: testutil.Command(testutil.Listen, out),
		Policy:  config.RestartNever,
	})
	defer p.Stop()

	raw, ok := testutil.ReadFile(out, 5*time.Second)
	if !ok {
		t.Fatal("helper never reported its port")
	}
	want, _ := strconv.Atoi(raw)
	pgid := p.Status().PID // apps lead their own process group

	var got map[int][]int
	found := testutil.WaitFor(5*time.Second, func() bool {
		var err error
		got, err = Listening([]int{pgid})
		if err != nil {
			t.Fatalf("Listening: %v", err)
		}
		return slices.Contains(got[pgid], want)
	})
	if !found {
		t.Fatalf("port %d not detected for pgid %d: %v", want, pgid, got)
	}
}

func TestNoListenerReturnsNothing(t *testing.T) {
	p := process.Start(process.Spec{
		Name:    "quiet",
		Command: testutil.Command(testutil.Sleep, ""),
		Policy:  config.RestartNever,
	})
	defer p.Stop()
	if !testutil.WaitFor(5*time.Second, func() bool { return p.Status().PID != 0 }) {
		t.Fatal("process never started")
	}
	pgid := p.Status().PID
	got, err := Listening([]int{pgid})
	if err != nil {
		t.Fatal(err)
	}
	if len(got[pgid]) != 0 {
		t.Errorf("unexpected ports %v", got[pgid])
	}
}
