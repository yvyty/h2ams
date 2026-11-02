package notify

import (
	"errors"
	"reflect"
	"testing"
)

func TestCommand(t *testing.T) {
	name, args, err := command("darwin", "h2ams", `api "crashed" \ again`)
	if err != nil || name != "osascript" {
		t.Fatalf("darwin: %s %v %v", name, args, err)
	}
	want := []string{"-e", `display notification "api \"crashed\" \\ again" with title "h2ams"`}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("darwin args = %q, want %q", args, want)
	}

	name, args, err = command("linux", "h2ams", "-dash message")
	if err != nil || name != "notify-send" || !reflect.DeepEqual(args, []string{"--app-name=h2ams", "--", "h2ams", "-dash message"}) {
		t.Errorf("linux: %s %q %v", name, args, err)
	}

	if _, _, err := command("windows", "t", "m"); !errors.Is(err, errors.ErrUnsupported) {
		t.Errorf("windows: err = %v, want ErrUnsupported", err)
	}
}
