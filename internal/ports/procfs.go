package ports

import (
	"bufio"
	"io"
	"strconv"
	"strings"
)

const tcpListen = "0A" // TCP_LISTEN in /proc/net/tcp's st column

// parseProcNetTCP maps socket inode to port for LISTEN rows of /proc/net/tcp
// or /proc/net/tcp6.
func parseProcNetTCP(r io.Reader) map[uint64]int {
	out := make(map[uint64]int)
	sc := bufio.NewScanner(r)
	sc.Scan() // header
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 10 || f[3] != tcpListen {
			continue
		}
		i := strings.LastIndexByte(f[1], ':')
		if i < 0 {
			continue
		}
		port, err := strconv.ParseUint(f[1][i+1:], 16, 16)
		if err != nil {
			continue
		}
		inode, err := strconv.ParseUint(f[9], 10, 64)
		if err != nil || inode == 0 {
			continue
		}
		out[inode] = int(port)
	}
	return out
}

// parseStatPgrp returns the process group from /proc/<pid>/stat. The command
// name (field 2) may contain spaces and parens, so parse after the last ')'.
func parseStatPgrp(stat string) int {
	i := strings.LastIndexByte(stat, ')')
	if i < 0 {
		return 0
	}
	// After ")": state ppid pgrp ...
	f := strings.Fields(stat[i+1:])
	if len(f) < 3 {
		return 0
	}
	pgrp, _ := strconv.Atoi(f[2])
	return pgrp
}
