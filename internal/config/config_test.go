package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const validYAML = `
version: 1
default_environment: dev
environments:
  dev:
    apps: [api, web]
apps:
  api:
    command: go run ./cmd/api
    cwd: ./api
    env:
      PORT: "8080"
    restart:
      policy: always
      max_attempts: 3
      backoff: 500ms
  web:
    command: npm run dev
`

func TestParseValid(t *testing.T) {
	cfg, err := Parse([]byte(validYAML))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	api := cfg.Apps["api"]
	if api.Command != "go run ./cmd/api" || api.Cwd != "./api" || api.Env["PORT"] != "8080" {
		t.Errorf("api parsed wrong: %+v", api)
	}
	if api.Restart.Policy != RestartAlways || api.Restart.MaxAttempts != 3 || time.Duration(api.Restart.Backoff) != 500*time.Millisecond {
		t.Errorf("api restart parsed wrong: %+v", api.Restart)
	}

	web := cfg.Apps["web"]
	if web.Restart.Policy != DefaultRestartPolicy || web.Restart.MaxAttempts != DefaultMaxAttempts || time.Duration(web.Restart.Backoff) != DefaultBackoff {
		t.Errorf("web defaults not applied: %+v", web.Restart)
	}

	apps, err := cfg.AppsFor("")
	if err != nil || strings.Join(apps, ",") != "api,web" {
		t.Errorf("AppsFor default = %v, %v", apps, err)
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantMsg []string
	}{
		{"empty", "", []string{"config is empty"}},
		{"malformed yaml", "version: 1\napps: [unclosed", []string{"invalid YAML"}},
		{"unknown field", "version: 1\napps:\n  a:\n    command: x\n    comand: y\n", []string{"invalid YAML", "comand"}},
		{"missing version", "apps:\n  a:\n    command: x\n", []string{"version: must be 1"}},
		{"no apps", "version: 1\n", []string{"apps: at least one app"}},
		{"missing command", "version: 1\napps:\n  a:\n    cwd: .\n", []string{"apps.a.command: required"}},
		{
			"invalid restart policy",
			"version: 1\napps:\n  a:\n    command: x\n    restart:\n      policy: sometimes\n",
			[]string{`apps.a.restart.policy: must be one of always, on-failure, never; got "sometimes"`},
		},
		{"negative attempts", "version: 1\napps:\n  a:\n    command: x\n    restart:\n      max_attempts: -1\n", []string{"max_attempts: must not be negative"}},
		{"bad backoff", "version: 1\napps:\n  a:\n    command: x\n    restart:\n      backoff: soon\n", []string{`invalid duration "soon"`}},
		{
			"unknown app in environment",
			"version: 1\nenvironments:\n  dev:\n    apps: [ghost]\napps:\n  a:\n    command: x\n",
			[]string{`environments.dev.apps: unknown app "ghost"`},
		},
		{"unknown default environment", "version: 1\ndefault_environment: prod\napps:\n  a:\n    command: x\n", []string{`default_environment: unknown environment "prod"`}},
		{
			"reports all problems",
			"version: 2\napps:\n  a:\n    restart:\n      policy: bad\n",
			[]string{"version: must be 1", "apps.a.command: required", "apps.a.restart.policy"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.yaml))
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			for _, msg := range tt.wantMsg {
				if !strings.Contains(err.Error(), msg) {
					t.Errorf("error %q does not contain %q", err, msg)
				}
			}
		})
	}
}

func TestValidationErrorType(t *testing.T) {
	_, err := Parse([]byte("version: 1\napps:\n  a:\n    cwd: .\n"))
	var verr *ValidationError
	if !errors.As(err, &verr) || len(verr.Problems) != 1 {
		t.Fatalf("expected one ValidationError problem, got %v", err)
	}
}

func TestLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), DefaultFileName)
	if err := os.WriteFile(path, []byte(validYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, err := Load(filepath.Join(t.TempDir(), "missing.yml")); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestAppsForUnknownEnvironment(t *testing.T) {
	cfg, err := Parse([]byte(validYAML))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cfg.AppsFor("prod"); err == nil {
		t.Fatal("expected error for unknown environment")
	}
}
