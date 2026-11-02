package cli

import (
	"bytes"
	"strings"
	"testing"
)

// run executes the CLI with args and returns combined stdout and stderr.
func run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestEveryCommandHasHelp(t *testing.T) {
	root := NewRootCmd()
	for _, c := range root.Commands() {
		if c.Hidden || c.Name() == "help" || c.Name() == "completion" {
			continue
		}
		if c.Short == "" || c.Long == "" || c.Example == "" {
			t.Errorf("%s: missing Short, Long, or Example help", c.Name())
		}
		out, err := run(t, c.Name(), "--help")
		if err != nil || !strings.Contains(out, "Usage:") || !strings.Contains(out, "Examples:") {
			t.Errorf("%s --help: err=%v\n%s", c.Name(), err, out)
		}
	}
}

func TestRootHelpShowsWorkflow(t *testing.T) {
	out, err := run(t, "--help")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Typical workflow", "h2ams up", "h2ams logs <app> -f", "--version"} {
		if !strings.Contains(out, want) {
			t.Errorf("root help missing %q", want)
		}
	}
}
