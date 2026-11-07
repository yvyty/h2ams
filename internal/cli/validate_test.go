package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, dir, body string) string {
	t.Helper()
	path := filepath.Join(dir, "h2ams.yml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestValidateAcceptsValidConfig(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "api"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := writeConfig(t, dir, "version: 1\napps:\n  api:\n    command: x\n    cwd: ./api\n  web:\n    command: y\n")
	out, err := run(t, "validate", "--config", cfg)
	if err != nil {
		t.Fatalf("validate: %v\n%s", err, out)
	}
	if !strings.Contains(out, "is valid") {
		t.Errorf("output = %q", out)
	}
}

func TestValidateListsEveryProblem(t *testing.T) {
	cfg := writeConfig(t, t.TempDir(), "version: 2\napps:\n  api:\n    restart:\n      policy: sometimes\n")
	_, err := run(t, "validate", "--config", cfg)
	if err == nil {
		t.Fatal("want an error for an invalid config")
	}
	for _, want := range []string{"version: must be 1", "apps.api.command: required", "apps.api.restart.policy"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q:\n%v", want, err)
		}
	}
}

func TestValidateReportsMissingCwd(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "file"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := writeConfig(t, dir, "version: 1\napps:\n  api:\n    command: x\n    cwd: ./missing\n  web:\n    command: y\n    cwd: ./file\n  db:\n    docker:\n      image: postgres:16\n")
	_, err := run(t, "validate", "--config", cfg)
	if err == nil {
		t.Fatal("want an error for a missing cwd")
	}
	for _, want := range []string{"apps.api.cwd: " + filepath.Join(dir, "missing") + " does not exist", "apps.web.cwd: " + filepath.Join(dir, "file") + " is not a directory"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q:\n%v", want, err)
		}
	}
	if strings.Contains(err.Error(), "apps.db") {
		t.Errorf("db has no cwd and defaults to the config dir, should pass:\n%v", err)
	}
}

func TestValidateChecksEnvironment(t *testing.T) {
	cfg := writeConfig(t, t.TempDir(), "version: 1\nenvironments:\n  dev:\n    apps: [api]\napps:\n  api:\n    command: x\n")
	if _, err := run(t, "validate", "--config", cfg, "--env", "dev"); err != nil {
		t.Errorf("--env dev: %v", err)
	}
	if _, err := run(t, "validate", "--config", cfg, "--env", "prod"); err == nil || !strings.Contains(err.Error(), `unknown environment "prod"`) {
		t.Errorf("--env prod: err = %v", err)
	}
}

func TestValidateJSON(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name     string
		body     string
		valid    bool
		problems []string
	}{
		{"valid", "version: 1\napps:\n  api:\n    command: x\n", true, []string{}},
		{"invalid", "version: 1\napps:\n  api:\n    restart:\n      policy: never\n", false,
			[]string{"apps.api.command: required (or docker for a container app)"}},
		{"unknown field", "version: 1\napps:\n  api:\n    command: x\n    colour: red\n", false, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := writeConfig(t, dir, tc.body)
			out, err := run(t, "validate", "--config", cfg, "--json")
			if (err == nil) != tc.valid {
				t.Fatalf("err = %v, want valid=%v", err, tc.valid)
			}
			var res validateResult
			if jerr := json.Unmarshal([]byte(out), &res); jerr != nil {
				t.Fatalf("output is not JSON: %v\n%s", jerr, out)
			}
			if res.Valid != tc.valid || res.Config != cfg {
				t.Errorf("result = %+v", res)
			}
			if tc.problems != nil && !slices.Equal(res.Problems, tc.problems) {
				t.Errorf("problems = %q, want %q", res.Problems, tc.problems)
			}
			if !tc.valid && len(res.Problems) == 0 {
				t.Error("invalid result has no problems")
			}
		})
	}
}

func TestValidateMissingFile(t *testing.T) {
	out, err := run(t, "validate", "--config", filepath.Join(t.TempDir(), "nope.yml"), "--json")
	if err == nil {
		t.Fatal("want an error for a missing file")
	}
	var res validateResult
	if jerr := json.Unmarshal([]byte(out), &res); jerr != nil || res.Valid || len(res.Problems) != 1 || !strings.Contains(res.Problems[0], "reading config") {
		t.Errorf("result = %+v (json err %v)", res, jerr)
	}
}
