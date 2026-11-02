package daemon

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// projectFile records which config a state directory belongs to. It stays
// after "h2ams down" so tools such as h2ams-tray can start the project again.
const projectFile = "project.json"

// Project is a config file h2ams has run, found through its state directory.
type Project struct {
	ConfigPath string `json:"config_path"`
	Paths      Paths  `json:"-"`
}

// Running reports whether the project's daemon is up.
func (p Project) Running() bool { return Running(p.Paths.Socket) }

func writeProject(paths Paths, configPath string) error {
	data, err := json.MarshalIndent(Project{ConfigPath: configPath}, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(paths.Dir, projectFile+".tmp")
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(paths.Dir, projectFile))
}

// KnownProjects lists every project h2ams has run whose config file still
// exists, sorted by config path.
func KnownProjects() ([]Project, error) {
	base, err := stateBase()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(base)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var projects []Project
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(base, e.Name())
		data, err := os.ReadFile(filepath.Join(dir, projectFile))
		if err != nil {
			continue
		}
		var p Project
		if json.Unmarshal(data, &p) != nil || p.ConfigPath == "" {
			continue
		}
		if _, err := os.Stat(p.ConfigPath); err != nil {
			continue // config moved or deleted
		}
		p.Paths = NewPaths(dir)
		projects = append(projects, p)
	}
	sort.Slice(projects, func(i, j int) bool { return projects[i].ConfigPath < projects[j].ConfigPath })
	return projects, nil
}
