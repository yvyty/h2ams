//go:build !linux

package tray

import "testing"

// startWatcher is only needed on Linux, where tray icons go through D-Bus.
func startWatcher(*testing.T) func() bool { return nil }
