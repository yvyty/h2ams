// Package config defines the h2ams.yml schema and loads and validates it.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// DefaultFileName is the config file h2ams looks for in the working directory.
const DefaultFileName = "h2ams.yml"

// SupportedVersion is the only schema version this build understands.
const SupportedVersion = 1

// RestartPolicy controls what the supervisor does when an app exits.
type RestartPolicy string

const (
	RestartAlways    RestartPolicy = "always"
	RestartOnFailure RestartPolicy = "on-failure"
	RestartNever     RestartPolicy = "never"
)

// Defaults applied when an app omits restart settings.
const (
	DefaultRestartPolicy = RestartOnFailure
	DefaultMaxAttempts   = 5
	DefaultBackoff       = time.Second
)

// DefaultDashboardPort is where the web dashboard listens unless configured.
const DefaultDashboardPort = 9999

// Config is the root of h2ams.yml.
type Config struct {
	Version            int                    `yaml:"version"`
	DefaultEnvironment string                 `yaml:"default_environment,omitempty"`
	Environments       map[string]Environment `yaml:"environments,omitempty"`
	Apps               map[string]App         `yaml:"apps"`
	Dashboard          Dashboard              `yaml:"dashboard,omitempty"`
	Notifications      Notifications          `yaml:"notifications,omitempty"`
}

// Dashboard configures the local web UI served by the daemon.
type Dashboard struct {
	Enabled *bool `yaml:"enabled,omitempty"` // nil means enabled
	Port    int   `yaml:"port,omitempty"`    // 0 means DefaultDashboardPort
}

// IsEnabled reports whether the dashboard should run.
func (d Dashboard) IsEnabled() bool { return d.Enabled == nil || *d.Enabled }

// Notifications configures desktop notifications.
type Notifications struct {
	// CrashLoop notifies when an app restarts repeatedly or gives up.
	CrashLoop bool `yaml:"crash_loop,omitempty"`
}

// Environment is a named group of apps started together.
type Environment struct {
	Apps []string `yaml:"apps"`
}

// App describes one managed application.
type App struct {
	Command string            `yaml:"command,omitempty"` // set exactly one of Command and Docker
	Docker  *Docker           `yaml:"docker,omitempty"`
	Cwd     string            `yaml:"cwd,omitempty"`
	Env     map[string]string `yaml:"env,omitempty"` // for Docker apps, passed into the container
	Restart Restart           `yaml:"restart,omitempty"`
}

// Dir returns the app's working directory: Cwd resolved against baseDir (the
// config file's directory), or baseDir itself when Cwd is empty.
func (a App) Dir(baseDir string) string {
	if filepath.IsAbs(a.Cwd) {
		return a.Cwd
	}
	return filepath.Join(baseDir, a.Cwd)
}

// Docker runs an app as a container through the docker CLI.
type Docker struct {
	Image   string   `yaml:"image"`
	Ports   []string `yaml:"ports,omitempty"`   // "[ip:]host:container[/proto]", e.g. "8080:80"
	Volumes []string `yaml:"volumes,omitempty"` // "host:container[:opts]"; ./relative hosts resolve against cwd
	Args    []string `yaml:"args,omitempty"`    // passed after the image
}

// PublishedPort returns the host port of a docker port mapping such as
// "8080:80", "127.0.0.1:5432:5432", or "53:53/udp".
func PublishedPort(mapping string) (int, error) {
	spec, _, _ := strings.Cut(mapping, "/")
	parts := strings.Split(spec, ":")
	if len(parts) < 2 {
		return 0, fmt.Errorf("%q: want host:container, e.g. \"8080:80\"", mapping)
	}
	hostPort, ctrPort := parts[len(parts)-2], parts[len(parts)-1]
	for _, p := range []string{hostPort, ctrPort} {
		if n, err := strconv.Atoi(p); err != nil || n < 1 || n > 65535 {
			return 0, fmt.Errorf("%q: %q is not a port number", mapping, p)
		}
	}
	port, _ := strconv.Atoi(hostPort)
	return port, nil
}

// Restart holds an app's restart behavior.
type Restart struct {
	Policy      RestartPolicy `yaml:"policy,omitempty"`
	MaxAttempts int           `yaml:"max_attempts,omitempty"`
	Backoff     Duration      `yaml:"backoff,omitempty"`
}

// Duration is a time.Duration that unmarshals from strings like "500ms" or "2s".
type Duration time.Duration

// UnmarshalYAML parses a Go duration string.
func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	var s string
	if err := node.Decode(&s); err != nil {
		return fmt.Errorf("line %d: duration must be a string like \"1s\"", node.Line)
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("line %d: invalid duration %q", node.Line, s)
	}
	*d = Duration(parsed)
	return nil
}

// MarshalYAML writes the duration as a string.
func (d Duration) MarshalYAML() (any, error) {
	return time.Duration(d).String(), nil
}

// Load reads and parses the config file at path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}
	cfg, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

// Parse decodes YAML, rejects unknown fields, applies defaults, and validates.
func Parse(data []byte) (*Config, error) {
	var cfg Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("config is empty")
		}
		return nil, fmt.Errorf("invalid YAML: %w", err)
	}
	cfg.applyDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) applyDefaults() {
	if c.Dashboard.Port == 0 {
		c.Dashboard.Port = DefaultDashboardPort
	}
	for name, app := range c.Apps {
		if app.Restart.Policy == "" {
			app.Restart.Policy = DefaultRestartPolicy
		}
		if app.Restart.MaxAttempts == 0 {
			app.Restart.MaxAttempts = DefaultMaxAttempts
		}
		if app.Restart.Backoff == 0 {
			app.Restart.Backoff = Duration(DefaultBackoff)
		}
		c.Apps[name] = app
	}
}

// validName limits app and environment names to characters that are safe in
// file names (each app gets its own log file).
var validName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// ValidationError lists every problem found in a config.
type ValidationError struct {
	Problems []string
}

func (e *ValidationError) Error() string {
	return "invalid config:\n  - " + strings.Join(e.Problems, "\n  - ")
}

// Validate checks required fields and cross-references. Call after defaults.
func (c *Config) Validate() error {
	var problems []string
	add := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }

	if c.Version != SupportedVersion {
		add("version: must be %d, got %d", SupportedVersion, c.Version)
	}
	if len(c.Apps) == 0 {
		add("apps: at least one app is required")
	}

	for _, name := range sortedKeys(c.Apps) {
		app := c.Apps[name]
		if !validName.MatchString(name) {
			add("apps.%s: name must start with a letter or digit and contain only letters, digits, '.', '_' or '-'", name)
		}
		hasCommand := strings.TrimSpace(app.Command) != ""
		switch {
		case hasCommand && app.Docker != nil:
			add("apps.%s: set either command or docker, not both", name)
		case !hasCommand && app.Docker == nil:
			add("apps.%s.command: required (or docker for a container app)", name)
		case app.Docker != nil:
			if strings.TrimSpace(app.Docker.Image) == "" {
				add("apps.%s.docker.image: required", name)
			}
			for _, p := range app.Docker.Ports {
				if _, err := PublishedPort(p); err != nil {
					add("apps.%s.docker.ports: %v", name, err)
				}
			}
			for _, v := range app.Docker.Volumes {
				if host, ctr, ok := strings.Cut(v, ":"); !ok || host == "" || ctr == "" {
					add("apps.%s.docker.volumes: %q: want host:container", name, v)
				}
			}
		}
		switch app.Restart.Policy {
		case RestartAlways, RestartOnFailure, RestartNever:
		default:
			add("apps.%s.restart.policy: must be one of always, on-failure, never; got %q", name, app.Restart.Policy)
		}
		if app.Restart.MaxAttempts < 0 {
			add("apps.%s.restart.max_attempts: must not be negative", name)
		}
		if app.Restart.Backoff < 0 {
			add("apps.%s.restart.backoff: must not be negative", name)
		}
	}

	for _, envName := range sortedKeys(c.Environments) {
		env := c.Environments[envName]
		if !validName.MatchString(envName) {
			add("environments.%s: name must start with a letter or digit and contain only letters, digits, '.', '_' or '-'", envName)
		}
		if len(env.Apps) == 0 {
			add("environments.%s.apps: at least one app is required", envName)
		}
		for _, appName := range env.Apps {
			if _, ok := c.Apps[appName]; !ok {
				add("environments.%s.apps: unknown app %q", envName, appName)
			}
		}
	}

	if c.Dashboard.Port < 1 || c.Dashboard.Port > 65535 {
		add("dashboard.port: must be between 1 and 65535, got %d", c.Dashboard.Port)
	}

	if c.DefaultEnvironment != "" {
		if _, ok := c.Environments[c.DefaultEnvironment]; !ok {
			add("default_environment: unknown environment %q", c.DefaultEnvironment)
		}
	}

	if len(problems) > 0 {
		return &ValidationError{Problems: problems}
	}
	return nil
}

// ResolveEnvironment returns the environment name that envName refers to:
// envName itself, or the default environment when it is empty. An empty result
// means "all apps".
func (c *Config) ResolveEnvironment(envName string) string {
	if envName == "" {
		return c.DefaultEnvironment
	}
	return envName
}

// AppsFor returns the app names for an environment. An empty name means the
// default environment, or every app when no default is set.
func (c *Config) AppsFor(envName string) ([]string, error) {
	envName = c.ResolveEnvironment(envName)
	if envName == "" {
		return sortedKeys(c.Apps), nil
	}
	env, ok := c.Environments[envName]
	if !ok {
		return nil, fmt.Errorf("unknown environment %q", envName)
	}
	return append([]string(nil), env.Apps...), nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
