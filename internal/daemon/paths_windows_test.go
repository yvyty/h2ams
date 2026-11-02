package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Without H2AMS_STATE_DIR, state lives under %LocalAppData%\h2ams and the
// socket path fits the AF_UNIX limit even for a deep config path.
func TestPathsForUsesLocalAppData(t *testing.T) {
	t.Setenv(StateDirEnv, "")
	local := os.Getenv("LocalAppData")
	if local == "" {
		t.Skip("LocalAppData not set")
	}
	deep := filepath.Join(t.TempDir(), strings.Repeat("nested-folder\\", 12), "h2ams.yml")
	p, err := PathsFor(deep)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(local, "h2ams") + string(filepath.Separator)
	if !strings.HasPrefix(strings.ToLower(p.Dir), strings.ToLower(want)) {
		t.Errorf("Dir = %q, want under %q", p.Dir, want)
	}
	if len(p.Socket) > maxSocketPath {
		t.Errorf("socket path is %d bytes, over %d: %s", len(p.Socket), maxSocketPath, p.Socket)
	}
	if filepath.Ext(p.Socket) != ".sock" || !filepath.IsAbs(p.Socket) {
		t.Errorf("Socket = %q", p.Socket)
	}
}
