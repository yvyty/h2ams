//go:build unix

package tray

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestTrayActionsEndToEnd drives the tray's logic (everything except drawing
// the menu) against the real h2ams binary: discover a project, start an
// environment, switch environments, and stop it.
func TestTrayActionsEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the h2ams binary")
	}
	work, err := os.MkdirTemp("", "h2tray") // short path for the socket
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(work) })
	t.Setenv("H2AMS_STATE_DIR", filepath.Join(work, "state"))

	bin := filepath.Join(work, "h2ams")
	if out, err := exec.Command("go", "build", "-o", bin, "github.com/yvyty/h2ams/cmd/h2ams").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	cfg := writeConfig(t, filepath.Join(work, "shop"), `version: 1
default_environment: dev
dashboard: {enabled: false}
environments:
  dev: {apps: [api, web]}
  testing: {apps: [api]}
apps:
  api: {command: sleep 60}
  web: {command: sleep 60}
`)
	runner := Runner{H2ams: bin}
	t.Cleanup(func() { runner.Run(Action{Kind: ActDown, ConfigPath: cfg}) })

	project := func() Project {
		t.Helper()
		ps, err := Load()
		if err != nil || len(ps) != 1 {
			t.Fatalf("Load = %+v, %v", ps, err)
		}
		return ps[0]
	}
	waitFor := func(what string, cond func(Project) bool) Project {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			p := project()
			if cond(p) {
				return p
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: %+v", what, p)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}

	// A project becomes known the first time it runs.
	if err := runner.Run(Action{Kind: ActUp, ConfigPath: cfg}); err != nil {
		t.Fatalf("up: %v", err)
	}
	p := waitFor("dev running", func(p Project) bool { return p.Running && p.Health() == OK && len(p.Apps) == 2 })
	if p.Environment != "dev" || p.Name != "shop" {
		t.Errorf("after up: %+v", p)
	}
	if find(Menu([]Project{p}), "Switch to testing") == nil {
		t.Error("menu offers no switch to testing")
	}

	if err := runner.Run(Action{Kind: ActSwitch, ConfigPath: cfg, Env: "testing"}); err != nil {
		t.Fatalf("switch: %v", err)
	}
	p = waitFor("testing running", func(p Project) bool { return p.Running && p.Environment == "testing" && p.Health() == OK })
	if len(p.Apps) != 1 || p.Apps[0].Name != "api" {
		t.Errorf("testing apps = %+v", p.Apps)
	}

	if err := runner.Run(Action{Kind: ActDown, ConfigPath: cfg}); err != nil {
		t.Fatalf("down: %v", err)
	}
	p = project()
	if p.Running || p.Health() != Idle {
		t.Errorf("after down: %+v", p)
	}
	if find(Menu([]Project{p}), "Start testing") == nil {
		t.Error("stopped project cannot be started from the menu")
	}
}
