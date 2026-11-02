package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yvyty/h2ams/internal/config"
)

func runInit(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	t.Chdir(dir)
	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(append([]string{"init"}, args...))
	err := cmd.Execute()
	return out.String(), err
}

func TestInitCreatesValidConfig(t *testing.T) {
	dir := t.TempDir()
	out, err := runInit(t, dir)
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	if !strings.Contains(out, "Created") {
		t.Errorf("unexpected output: %q", out)
	}
	cfg, err := config.Load(filepath.Join(dir, config.DefaultFileName))
	if err != nil {
		t.Fatalf("generated config is invalid: %v", err)
	}
	if len(cfg.Apps) == 0 {
		t.Error("generated config has no apps")
	}
}

func TestInitDoesNotOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, config.DefaultFileName)
	if err := os.WriteFile(path, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := runInit(t, dir)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected 'already exists' error, got %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != "mine" {
		t.Fatalf("file was overwritten: %q", got)
	}

	if _, err := runInit(t, dir, "--force"); err != nil {
		t.Fatalf("init --force: %v", err)
	}
	if _, err := config.Load(path); err != nil {
		t.Fatalf("forced config is invalid: %v", err)
	}
}
