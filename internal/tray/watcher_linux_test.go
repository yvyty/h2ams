package tray

import (
	"os"
	"sync/atomic"
	"testing"

	"github.com/godbus/dbus/v5"
)

// fakeWatcher stands in for the panel's StatusNotifierWatcher (what KDE,
// GNOME's AppIndicator extension, and others provide), so a headless CI
// session can check that the tray really registers its icon.
type fakeWatcher struct{ registered atomic.Int32 }

func (w *fakeWatcher) RegisterStatusNotifierItem(service string) *dbus.Error {
	w.registered.Add(1)
	return nil
}

// startWatcher owns org.kde.StatusNotifierWatcher on the session bus. It
// returns nil when there is no session bus to own it on.
func startWatcher(t *testing.T) func() bool {
	t.Helper()
	if os.Getenv("DBUS_SESSION_BUS_ADDRESS") == "" {
		return nil
	}
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		t.Fatalf("session bus: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	w := &fakeWatcher{}
	if err := conn.Export(w, "/StatusNotifierWatcher", "org.kde.StatusNotifierWatcher"); err != nil {
		t.Fatal(err)
	}
	reply, err := conn.RequestName("org.kde.StatusNotifierWatcher", dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Skipf("a real StatusNotifierWatcher is running (reply %v, %v)", reply, err)
	}
	return func() bool { return w.registered.Load() > 0 }
}
