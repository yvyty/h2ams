// Package tray holds the logic of h2ams-tray, separate from the GUI so it can
// be tested: discovering projects, summarizing their health, building the
// menu, and running menu actions.
package tray

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/yvyty/h2ams/internal/config"
	"github.com/yvyty/h2ams/internal/daemon"
	"github.com/yvyty/h2ams/internal/process"
)

// Health summarizes how a project (or every project) is doing.
type Health int

const (
	Idle Health = iota // nothing running
	OK                 // running, all apps healthy
	Warn               // something is starting, restarting, or stopped
	Bad                // something failed
)

func (h Health) String() string {
	return [...]string{"idle", "ok", "warning", "failed"}[h]
}

// Project is one known h2ams project as the tray shows it.
type Project struct {
	Name               string // the config file's directory name
	ConfigPath         string
	Running            bool
	Environment        string   // active environment while running ("" = all apps)
	Environments       []string // from the config, sorted
	DefaultEnvironment string
	Dashboard          string
	Apps               []daemon.AppStatus
	Err                string // config or daemon problem to show
}

// Health is Idle when stopped, otherwise the worst app state.
func (p Project) Health() Health {
	if !p.Running {
		return Idle
	}
	h := OK
	for _, app := range p.Apps {
		switch app.State {
		case process.StateFailed:
			return Bad
		case process.StateRunning:
		default:
			h = Warn
		}
	}
	return h
}

// Overall is the worst health of any running project, or Idle.
func Overall(projects []Project) Health {
	h := Idle
	for _, p := range projects {
		h = max(h, p.Health())
	}
	return h
}

// Summary is a one-line tooltip such as "2 projects running, 1 app failed".
func Summary(projects []Project) string {
	running, failed := 0, 0
	for _, p := range projects {
		if p.Running {
			running++
		}
		for _, a := range p.Apps {
			if a.State == process.StateFailed {
				failed++
			}
		}
	}
	if running == 0 {
		return "h2ams: nothing running"
	}
	s := fmt.Sprintf("h2ams: %d project%s running", running, plural(running))
	if failed > 0 {
		s += fmt.Sprintf(", %d app%s failed", failed, plural(failed))
	}
	return s
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// Load discovers every known project and asks running daemons for status.
func Load() ([]Project, error) {
	known, err := daemon.KnownProjects()
	if err != nil {
		return nil, err
	}
	return load(known, func(socket string) (daemon.Response, error) {
		return daemon.Call(socket, daemon.Request{Cmd: daemon.CmdStatus}, 2*time.Second)
	}), nil
}

func load(known []daemon.Project, status func(socket string) (daemon.Response, error)) []Project {
	projects := make([]Project, 0, len(known))
	for _, k := range known {
		p := Project{Name: filepath.Base(filepath.Dir(k.ConfigPath)), ConfigPath: k.ConfigPath}
		if cfg, err := config.Load(k.ConfigPath); err != nil {
			p.Err = "invalid config"
		} else {
			p.DefaultEnvironment = cfg.DefaultEnvironment
			for name := range cfg.Environments {
				p.Environments = append(p.Environments, name)
			}
			sort.Strings(p.Environments)
		}
		if resp, err := status(k.Paths.Socket); err == nil {
			p.Running = true
			p.Environment = resp.Environment
			p.Dashboard = resp.Dashboard
			p.Apps = resp.Apps
		}
		projects = append(projects, p)
	}
	disambiguate(projects)
	return projects
}

// disambiguate appends the parent directory to duplicate project names.
func disambiguate(projects []Project) {
	count := map[string]int{}
	for _, p := range projects {
		count[p.Name]++
	}
	for i, p := range projects {
		if count[p.Name] > 1 {
			parent := filepath.Base(filepath.Dir(filepath.Dir(p.ConfigPath)))
			projects[i].Name = parent + "/" + p.Name
		}
	}
}

// ActionKind is what a menu item does when clicked.
type ActionKind string

const (
	ActUp      ActionKind = "up"     // start the project in Env
	ActDown    ActionKind = "down"   // stop the project
	ActSwitch  ActionKind = "switch" // down, then up in Env
	ActOpen    ActionKind = "open"   // open URL in the browser
	ActRefresh ActionKind = "refresh"
	ActQuit    ActionKind = "quit"
)

// Action is attached to clickable menu items.
type Action struct {
	Kind       ActionKind
	ConfigPath string
	Env        string // "" means the config's default
	URL        string
}

// Item is one menu entry. Items without an Action are informational.
type Item struct {
	Title     string
	Disabled  bool
	Checked   bool
	Separator bool
	Action    *Action
	Children  []Item
}

// Menu builds the tray menu for projects.
func Menu(projects []Project) []Item {
	items := []Item{{Title: Summary(projects), Disabled: true}, {Separator: true}}
	if len(projects) == 0 {
		items = append(items, Item{Title: "No projects yet: run \"h2ams up\" in one", Disabled: true})
	}
	for _, p := range projects {
		items = append(items, projectItem(p))
	}
	return append(items,
		Item{Separator: true},
		Item{Title: "Refresh", Action: &Action{Kind: ActRefresh}},
		Item{Title: "Quit h2ams-tray", Action: &Action{Kind: ActQuit}},
	)
}

func projectItem(p Project) Item {
	item := Item{Title: projectTitle(p)}
	if p.Err != "" {
		item.Children = append(item.Children, Item{Title: p.Err + ": " + p.ConfigPath, Disabled: true})
		return item
	}

	if p.Running {
		for _, app := range p.Apps {
			item.Children = append(item.Children, Item{Title: appLine(app), Disabled: true})
		}
		item.Children = append(item.Children, Item{Separator: true})
	}

	envs := p.Environments
	if len(envs) == 0 {
		envs = []string{""} // the project has no environments: one "all apps" entry
	}
	for _, env := range envs {
		label := env
		if env == "" {
			label = "all apps"
		}
		if env != "" && env == p.DefaultEnvironment {
			label += " (default)"
		}
		switch {
		case p.Running && env == p.Environment:
			item.Children = append(item.Children, Item{Title: label, Checked: true, Disabled: true})
		case p.Running:
			item.Children = append(item.Children, Item{Title: "Switch to " + label, Action: &Action{Kind: ActSwitch, ConfigPath: p.ConfigPath, Env: env}})
		default:
			item.Children = append(item.Children, Item{Title: "Start " + label, Action: &Action{Kind: ActUp, ConfigPath: p.ConfigPath, Env: env}})
		}
	}

	if p.Running {
		item.Children = append(item.Children, Item{Separator: true})
		if p.Dashboard != "" {
			item.Children = append(item.Children, Item{Title: "Open dashboard", Action: &Action{Kind: ActOpen, URL: p.Dashboard}})
		}
		item.Children = append(item.Children, Item{Title: "Stop", Action: &Action{Kind: ActDown, ConfigPath: p.ConfigPath}})
	}
	return item
}

func projectTitle(p Project) string {
	if !p.Running {
		return p.Name + ": stopped"
	}
	env := p.Environment
	if env == "" {
		env = "all apps"
	}
	switch p.Health() {
	case Bad:
		return fmt.Sprintf("%s: %s, %s", p.Name, env, countState(p.Apps, process.StateFailed, "failed"))
	case Warn:
		return fmt.Sprintf("%s: %s, not all running", p.Name, env)
	default:
		return fmt.Sprintf("%s: %s, running", p.Name, env)
	}
}

func countState(apps []daemon.AppStatus, state process.State, word string) string {
	n := 0
	for _, a := range apps {
		if a.State == state {
			n++
		}
	}
	return fmt.Sprintf("%d %s", n, word)
}

func appLine(app daemon.AppStatus) string {
	line := fmt.Sprintf("%s: %s", app.Name, app.State)
	if len(app.Ports) > 0 {
		ports := make([]string, len(app.Ports))
		for i, p := range app.Ports {
			ports[i] = fmt.Sprintf(":%d", p)
		}
		line += " " + strings.Join(ports, " ")
	}
	return line
}
