package daemon

import (
	"strings"

	"testing"
)

func TestWindowsQuote(t *testing.T) {
	for in, want := range map[string]string{
		"plain":            "plain",
		"":                 `""`,
		"two words":        `"two words"`,
		`C:\Program Files`: `"C:\Program Files"`,
		`say "hi"`:         `"say \"hi\""`,
		`trailing\ x\`:     `"trailing\ x\\"`,
		`a\"b c`:           `"a\\\"b c"`,
		`q"uote`:           `q\"uote`,
		`back\slash`:       `back\slash`,
	} {
		if got := windowsQuote(in); got != want {
			t.Errorf("windowsQuote(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestShellLineWindows(t *testing.T) {
	got := shellLineWindows([]string{`C:\Program Files\Docker\docker.exe`, "run", "-e", "P=100%", "-e", "B=two words", "img"})
	want := `"C:\Program Files\Docker\docker.exe" run -e P=100^% -e "B=two words" img`
	if got != want {
		t.Errorf("shellLineWindows =\n%s\nwant\n%s", got, want)
	}
	if strings.HasPrefix(shellLineWindows([]string{"docker"}), "exec") {
		t.Error("cmd.exe has no exec")
	}
}

// TestWindowsQuoteMatchesSyscall cross-checks against the standard library
// where it is available.
func TestWindowsQuoteMatchesSyscall(t *testing.T) {
	escape := escapeArg
	if escape == nil {
		t.Skip("syscall.EscapeArg is Windows-only")
	}
	for _, s := range []string{"plain", "", "a b", `C:\x y\`, `q"uote`, `\\"x y`} {
		if got, want := windowsQuote(s), escape(s); got != want {
			t.Errorf("windowsQuote(%q) = %s, syscall.EscapeArg = %s", s, got, want)
		}
	}

}
