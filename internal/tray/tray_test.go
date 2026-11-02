package tray

import (
	"bytes"
	"encoding/binary"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/yvyty/h2ams/internal/daemon"
	"github.com/yvyty/h2ams/internal/process"
)

func app(name string, state process.State, ports ...int) daemon.AppStatus {
	return daemon.AppStatus{Status: process.Status{Name: name, State: state}, Ports: ports}
}

func TestHealth(t *testing.T) {
	tests := []struct {
		p    Project
		want Health
	}{
		{Project{}, Idle},
		{Project{Running: true, Apps: []daemon.AppStatus{app("a", process.StateRunning)}}, OK},
		{Project{Running: true, Apps: []daemon.AppStatus{app("a", process.StateRunning), app("b", process.StateRestarting)}}, Warn},
		{Project{Running: true, Apps: []daemon.AppStatus{app("a", process.StateStopped)}}, Warn},
		{Project{Running: true, Apps: []daemon.AppStatus{app("a", process.StateRestarting), app("b", process.StateFailed)}}, Bad},
	}
	for i, tt := range tests {
		if got := tt.p.Health(); got != tt.want {
			t.Errorf("case %d: Health = %v, want %v", i, got, tt.want)
		}
	}
	if got := Overall([]Project{tests[1].p, tests[2].p, {}}); got != Warn {
		t.Errorf("Overall = %v, want warning", got)
	}
	if got := Overall(nil); got != Idle {
		t.Errorf("Overall(nil) = %v", got)
	}
}

func TestSummary(t *testing.T) {
	ps := []Project{
		{Running: true, Apps: []daemon.AppStatus{app("a", process.StateFailed), app("b", process.StateFailed)}},
		{Running: true},
		{},
	}
	if got := Summary(ps); got != "h2ams: 2 projects running, 2 apps failed" {
		t.Errorf("Summary = %q", got)
	}
	if got := Summary(nil); got != "h2ams: nothing running" {
		t.Errorf("Summary(nil) = %q", got)
	}
}

func writeConfig(t *testing.T, dir, yaml string) string {
	t.Helper()
	os.MkdirAll(dir, 0o755)
	path := filepath.Join(dir, "h2ams.yml")
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoad(t *testing.T) {
	root := t.TempDir()
	shop := writeConfig(t, filepath.Join(root, "shop"), "version: 1\ndefault_environment: dev\nenvironments:\n  dev: {apps: [a]}\n  testing: {apps: [a]}\napps:\n  a: {command: x}\n")
	blog := writeConfig(t, filepath.Join(root, "blog"), "version: 1\napps:\n  a: {command: x}\n")
	broken := writeConfig(t, filepath.Join(root, "broken"), "version: 9\n")

	known := []daemon.Project{
		{ConfigPath: shop, Paths: daemon.NewPaths("/s/shop")},
		{ConfigPath: blog, Paths: daemon.NewPaths("/s/blog")},
		{ConfigPath: broken, Paths: daemon.NewPaths("/s/broken")},
	}
	status := func(socket string) (daemon.Response, error) {
		if socket == "/s/shop/h2ams.sock" {
			return daemon.Response{Environment: "testing", Dashboard: "http://127.0.0.1:9999", Apps: []daemon.AppStatus{app("a", process.StateRunning)}}, nil
		}
		return daemon.Response{}, daemon.ErrNotRunning
	}
	ps := load(known, status)

	if ps[0].Name != "shop" || !ps[0].Running || ps[0].Environment != "testing" || !reflect.DeepEqual(ps[0].Environments, []string{"dev", "testing"}) || ps[0].DefaultEnvironment != "dev" {
		t.Errorf("shop = %+v", ps[0])
	}
	if ps[1].Name != "blog" || ps[1].Running || len(ps[1].Environments) != 0 {
		t.Errorf("blog = %+v", ps[1])
	}
	if ps[2].Err == "" {
		t.Errorf("broken config not flagged: %+v", ps[2])
	}
}

func TestDuplicateNamesAreDisambiguated(t *testing.T) {
	ps := []Project{{Name: "api", ConfigPath: "/work/a/api/h2ams.yml"}, {Name: "api", ConfigPath: "/work/b/api/h2ams.yml"}, {Name: "web", ConfigPath: "/x/web/h2ams.yml"}}
	disambiguate(ps)
	if ps[0].Name != "a/api" || ps[1].Name != "b/api" || ps[2].Name != "web" {
		t.Errorf("names = %q %q %q", ps[0].Name, ps[1].Name, ps[2].Name)
	}
}

// find returns the first item (depth-first) whose title has prefix.
func find(items []Item, prefix string) *Item {
	for i := range items {
		if strings.HasPrefix(items[i].Title, prefix) {
			return &items[i]
		}
		if f := find(items[i].Children, prefix); f != nil {
			return f
		}
	}
	return nil
}

func TestMenuRunningProject(t *testing.T) {
	p := Project{
		Name: "shop", ConfigPath: "/p/shop/h2ams.yml", Running: true,
		Environment: "dev", Environments: []string{"dev", "testing"}, DefaultEnvironment: "dev",
		Dashboard: "http://127.0.0.1:9999",
		Apps:      []daemon.AppStatus{app("api", process.StateRunning, 8080), app("worker", process.StateFailed)},
	}
	menu := Menu([]Project{p})

	if menu[0].Title != "h2ams: 1 project running, 1 app failed" || !menu[0].Disabled {
		t.Errorf("header = %+v", menu[0])
	}
	proj := find(menu, "shop:")
	if proj == nil || proj.Title != "shop: dev, 1 failed" {
		t.Fatalf("project item = %+v", proj)
	}
	if api := find(proj.Children, "api:"); api == nil || api.Title != "api: running :8080" || !api.Disabled {
		t.Errorf("app line = %+v", api)
	}
	if active := find(proj.Children, "dev"); active == nil || !active.Checked || !active.Disabled || active.Title != "dev (default)" {
		t.Errorf("active env = %+v", active)
	}
	sw := find(proj.Children, "Switch to testing")
	if sw == nil || *sw.Action != (Action{Kind: ActSwitch, ConfigPath: p.ConfigPath, Env: "testing"}) {
		t.Errorf("switch = %+v", sw)
	}
	if stop := find(proj.Children, "Stop"); stop == nil || stop.Action.Kind != ActDown {
		t.Errorf("stop = %+v", stop)
	}
	if open := find(proj.Children, "Open dashboard"); open == nil || open.Action.URL != p.Dashboard {
		t.Errorf("open = %+v", open)
	}
	if quit := find(menu, "Quit"); quit == nil || quit.Action.Kind != ActQuit {
		t.Errorf("quit = %+v", quit)
	}
}

func TestMenuStoppedProjects(t *testing.T) {
	menu := Menu([]Project{
		{Name: "shop", ConfigPath: "/p/shop/h2ams.yml", Environments: []string{"dev", "testing"}, DefaultEnvironment: "dev"},
		{Name: "blog", ConfigPath: "/p/blog/h2ams.yml"},
	})
	shop := find(menu, "shop:")
	if shop.Title != "shop: stopped" {
		t.Errorf("title = %q", shop.Title)
	}
	start := find(shop.Children, "Start testing")
	if start == nil || *start.Action != (Action{Kind: ActUp, ConfigPath: "/p/shop/h2ams.yml", Env: "testing"}) {
		t.Errorf("start testing = %+v", start)
	}
	if find(shop.Children, "Start dev (default)") == nil {
		t.Error("default environment not labeled")
	}
	if find(shop.Children, "Stop") != nil {
		t.Error("stopped project offers Stop")
	}
	blog := find(menu, "blog:")
	if s := find(blog.Children, "Start all apps"); s == nil || s.Action.Env != "" {
		t.Errorf("project without environments: %+v", blog.Children)
	}
}

func TestMenuEmpty(t *testing.T) {
	if find(Menu(nil), "No projects yet") == nil {
		t.Error("empty menu has no hint")
	}
}

func TestUpArgsAndOpenCommand(t *testing.T) {
	if got := upArgs(Action{ConfigPath: "/p/h2ams.yml", Env: "testing"}); !reflect.DeepEqual(got, []string{"up", "--config", "/p/h2ams.yml", "--env", "testing"}) {
		t.Errorf("upArgs = %q", got)
	}
	if got := upArgs(Action{ConfigPath: "/p/h2ams.yml"}); len(got) != 3 {
		t.Errorf("upArgs without env = %q", got)
	}
	for goos, want := range map[string]string{"darwin": "open", "linux": "xdg-open", "windows": "rundll32"} {
		if name, args := openCommand(goos, "http://x"); name != want || args[len(args)-1] != "http://x" {
			t.Errorf("%s: %s %q", goos, name, args)
		}
	}
}

func TestRunnerReportsCLIErrors(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "h2ams")
	os.WriteFile(bin, []byte("#!/bin/sh\necho 'Error: h2ams.yml: invalid config' >&2\nexit 1\n"), 0o755)
	err := Runner{H2ams: bin}.Run(Action{Kind: ActUp, ConfigPath: "/p/h2ams.yml"})
	if err == nil || err.Error() != "h2ams.yml: invalid config" {
		t.Errorf("err = %v", err)
	}
	if err := (Runner{}).Run(Action{Kind: ActQuit}); err == nil {
		t.Error("quit should not be handled by Runner")
	}
}

func TestIcon(t *testing.T) {
	for h, c := range healthColors {
		data := Icon(h, "darwin")
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("%v: %v", h, err)
		}
		if img.Bounds().Dx() != iconSize {
			t.Errorf("%v: size %v", h, img.Bounds())
		}
		r, g, b, a := img.At(iconSize/2, iconSize/2).RGBA()
		if uint8(r>>8) != c.R || uint8(g>>8) != c.G || uint8(b>>8) != c.B || a == 0 {
			t.Errorf("%v: center pixel %d,%d,%d,%d, want %v", h, r>>8, g>>8, b>>8, a>>8, c)
		}
		if _, _, _, a := img.At(0, 0).RGBA(); a != 0 {
			t.Errorf("%v: corner not transparent", h)
		}
	}

	ico := Icon(Bad, "windows")
	var header [3]uint16
	binary.Read(bytes.NewReader(ico), binary.LittleEndian, &header)
	if header != [3]uint16{0, 1, 1} {
		t.Errorf("ICO header = %v", header)
	}
	if _, err := png.Decode(bytes.NewReader(ico[22:])); err != nil {
		t.Errorf("ICO does not embed a PNG at offset 22: %v", err)
	}
}

func TestFindH2amsOnPath(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "h2ams")
	os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755)
	t.Setenv("PATH", dir)
	got, err := FindH2ams()
	if err != nil || got != bin {
		t.Errorf("FindH2ams = %q, %v", got, err)
	}
	t.Setenv("PATH", t.TempDir())
	if _, err := FindH2ams(); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("err = %v, want not found", err)
	}
}
