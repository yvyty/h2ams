//go:build unix && !linux

package ports

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// listening asks lsof (bundled with macOS) for TCP listeners in the groups.
func listening(pgids []int) (map[int][]int, error) {
	bin, err := exec.LookPath("lsof")
	if err != nil {
		return nil, fmt.Errorf("port detection needs lsof: %w", errors.ErrUnsupported)
	}
	ids := make([]string, len(pgids))
	for i, id := range pgids {
		ids[i] = strconv.Itoa(id)
	}
	cmd := exec.Command(bin, "-nP", "-a", "-g", strings.Join(ids, ","), "-iTCP", "-sTCP:LISTEN", "-F", "pgn")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	err = cmd.Run()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 && stdout.Len() == 0 {
		return map[int][]int{}, nil // lsof exits 1 when nothing matches
	}
	if err != nil {
		return nil, fmt.Errorf("running lsof: %w", err)
	}
	return parseLsof(&stdout), nil
}
