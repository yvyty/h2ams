package ports

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// listening reads /proc directly: it maps listening socket inodes to ports,
// then matches them against the open file descriptors of each group member.
func listening(pgids []int) (map[int][]int, error) {
	inodes := make(map[uint64]int)
	for _, name := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		f, err := os.Open(name)
		if err != nil {
			continue // tcp6 is absent when IPv6 is disabled
		}
		for inode, port := range parseProcNetTCP(f) {
			inodes[inode] = port
		}
		f.Close()
	}

	want := make(map[int]bool, len(pgids))
	for _, id := range pgids {
		want[id] = true
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}

	out := make(map[int][]int)
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		stat, err := os.ReadFile(filepath.Join("/proc", e.Name(), "stat"))
		if err != nil {
			continue // process exited or is not ours
		}
		pgid := parseStatPgrp(string(stat))
		if !want[pgid] {
			continue
		}
		for _, port := range socketPorts(pid, inodes) {
			out[pgid] = append(out[pgid], port)
		}
	}
	return normalize(out), nil
}

func socketPorts(pid int, inodes map[uint64]int) []int {
	dir := filepath.Join("/proc", strconv.Itoa(pid), "fd")
	fds, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var ports []int
	for _, fd := range fds {
		link, err := os.Readlink(filepath.Join(dir, fd.Name()))
		if err != nil || !strings.HasPrefix(link, "socket:[") {
			continue
		}
		inode, err := strconv.ParseUint(strings.TrimSuffix(link[len("socket:["):], "]"), 10, 64)
		if err != nil {
			continue
		}
		if port, ok := inodes[inode]; ok {
			ports = append(ports, port)
		}
	}
	return ports
}
