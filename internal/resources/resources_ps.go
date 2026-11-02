//go:build unix && !linux

package resources

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
)

// sample reads every process from ps (bundled with macOS and the BSDs). Its
// %cpu is the kernel's decaying average, so no previous sample is needed.
func (s *Sampler) sample(pgids []int) (map[int]Usage, error) {
	bin, err := exec.LookPath("ps")
	if err != nil {
		return nil, fmt.Errorf("resource monitoring needs ps: %w", errors.ErrUnsupported)
	}
	var out bytes.Buffer
	cmd := exec.Command(bin, "-A", "-o", "pid=,ppid=,pgid=,rss=,%cpu=")
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("running ps: %w", err)
	}
	want := make(map[int]bool, len(pgids))
	for _, id := range pgids {
		want[id] = true
	}
	return sumPS(parsePS(&out), want), nil
}
