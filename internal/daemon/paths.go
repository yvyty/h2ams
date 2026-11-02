// Package daemon runs the background supervisor and talks to it over a
// Unix domain socket.
package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

// StateDirEnv overrides the base directory for daemon state.
const StateDirEnv = "H2AMS_STATE_DIR"

// Paths locates one daemon's socket and log. Each config file gets its own
// daemon, keyed by the config's absolute path.
type Paths struct {
	Dir    string
	Socket string
	Log    string
}

// PathsFor returns the state paths for the daemon managing configPath.
func PathsFor(configPath string) (Paths, error) {
	abs, err := filepath.Abs(configPath)
	if err != nil {
		return Paths{}, err
	}
	base := os.Getenv(StateDirEnv)
	if base == "" {
		cache, err := os.UserCacheDir()
		if err != nil {
			return Paths{}, fmt.Errorf("locating state directory: %w", err)
		}
		base = filepath.Join(cache, "h2ams")
	}
	sum := sha256.Sum256([]byte(abs))
	return NewPaths(filepath.Join(base, hex.EncodeToString(sum[:6]))), nil
}

// NewPaths returns the standard file layout inside dir.
func NewPaths(dir string) Paths {
	return Paths{
		Dir:    dir,
		Socket: filepath.Join(dir, "h2ams.sock"),
		Log:    filepath.Join(dir, "daemon.log"),
	}
}
