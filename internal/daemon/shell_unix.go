//go:build unix

package daemon

func shellLine(args []string) string { return shellLineUnix(args) }
