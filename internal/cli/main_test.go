package cli

import (
	"os"
	"runtime"
	"testing"

	"github.com/yvyty/h2ams/internal/testutil"
)

// TestMain lets end-to-end tests use this test binary as a fake app.
func TestMain(m *testing.M) {
	testutil.RunHelperIfRequested()
	os.Exit(m.Run())
}

// exeSuffix is the executable extension for binaries built by tests.
var exeSuffix = func() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}()
