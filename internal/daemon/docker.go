package daemon

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/yvyty/h2ams/internal/config"
)

// DockerEnv overrides the docker CLI binary (tests point it at a fake).
const DockerEnv = "H2AMS_DOCKER"

const dockerTimeout = 15 * time.Second

func dockerBinary() string {
	if bin := os.Getenv(DockerEnv); bin != "" {
		return bin
	}
	return "docker"
}

// checkDocker reports a clear error when the docker CLI cannot be found, so a
// container app fails at once instead of retrying a command that cannot run.
func checkDocker() error {
	bin := dockerBinary()
	if _, err := exec.LookPath(bin); err != nil {
		if os.Getenv(DockerEnv) != "" {
			return fmt.Errorf("%s=%s: not found or not executable", DockerEnv, bin)
		}
		return fmt.Errorf("docker CLI not found on PATH; install Docker or set %s", DockerEnv)
	}
	return nil
}

// containerName is unique per project and app, so two projects can run an
// app with the same name.
func containerName(project, app string) string {
	return "h2ams-" + project + "-" + app
}

// dockerRunCommand builds the shell command that runs app's container in the
// foreground. Running `docker run` as the supervised process means restart
// policies, logs, and stop/start work exactly as for native apps: the CLI
// forwards SIGTERM to the container and streams its output.
func dockerRunCommand(project, name, dir string, app config.App) string {
	d := app.Docker
	args := []string{
		dockerBinary(), "run", "--rm", "--name", name,
		"--label", "h2ams.project=" + project,
		"--label", "h2ams.app=" + strings.TrimPrefix(name, "h2ams-"+project+"-"),
	}
	for _, p := range d.Ports {
		args = append(args, "-p", p)
	}
	keys := make([]string, 0, len(app.Env))
	for k := range app.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, "-e", k+"="+app.Env[k])
	}
	for _, v := range d.Volumes {
		args = append(args, "-v", resolveVolume(v, dir))
	}
	args = append(args, d.Image)
	args = append(args, d.Args...)

	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = shellQuote(a)
	}
	return "exec " + strings.Join(quoted, " ")
}

// resolveVolume makes a relative host path ("./data", "../x", "~/y")
// absolute; named volumes ("pgdata:/var/lib/...") are left alone.
func resolveVolume(v, dir string) string {
	host, rest, _ := strings.Cut(v, ":")
	switch {
	case host == "." || strings.HasPrefix(host, "./") || strings.HasPrefix(host, "../"):
		host = filepath.Join(dir, host)
	case host == "~" || strings.HasPrefix(host, "~/"):
		if home, err := os.UserHomeDir(); err == nil {
			host = filepath.Join(home, strings.TrimPrefix(host, "~"))
		}
	}
	return host + ":" + rest
}

func shellQuote(s string) string {
	if s != "" && strings.IndexFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_./:=@,+", r))
	}) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// removeContainer force-removes name, ignoring "no such container".
func removeContainer(name string) {
	ctx, cancel := context.WithTimeout(context.Background(), dockerTimeout)
	defer cancel()
	_ = exec.CommandContext(ctx, dockerBinary(), "rm", "-f", name).Run()
}

// inspectContainers returns each existing container's state ("running",
// "exited", ...). Missing containers are absent from the map.
func inspectContainers(names []string) map[string]string {
	states := make(map[string]string, len(names))
	if len(names) == 0 {
		return states
	}
	ctx, cancel := context.WithTimeout(context.Background(), dockerTimeout)
	defer cancel()
	args := append([]string{"inspect", "--format", "{{.Name}} {{.State.Status}}"}, names...)
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, dockerBinary(), args...)
	cmd.Stdout = &out
	_ = cmd.Run() // exits non-zero if any container is missing; stdout still lists the rest
	for _, line := range strings.Split(out.String(), "\n") {
		name, state, ok := strings.Cut(strings.TrimSpace(line), " ")
		if ok {
			states[strings.TrimPrefix(name, "/")] = state
		}
	}
	return states
}
