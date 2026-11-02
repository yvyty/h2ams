// Package ports finds the TCP ports that process groups are listening on.
package ports

import (
	"bufio"
	"io"
	"slices"
	"strconv"
	"strings"
)

// Listening returns the TCP ports in LISTEN state for each process group ID.
// Groups with no listeners are absent from the map. On platforms without
// support it returns errors.ErrUnsupported.
func Listening(pgids []int) (map[int][]int, error) {
	if len(pgids) == 0 {
		return map[int][]int{}, nil
	}
	return listening(pgids)
}

// parseLsof reads `lsof -F pgn` output and groups ports by process group.
func parseLsof(r io.Reader) map[int][]int {
	out := make(map[int][]int)
	pgid := 0
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			continue
		}
		switch line[0] {
		case 'p':
			pgid = 0
		case 'g':
			pgid, _ = strconv.Atoi(line[1:])
		case 'n':
			if port, ok := portFromAddr(line[1:]); ok && pgid != 0 {
				out[pgid] = append(out[pgid], port)
			}
		}
	}
	return normalize(out)
}

// portFromAddr extracts the port from "host:port", "*:port", or "[::1]:port".
func portFromAddr(addr string) (int, bool) {
	i := strings.LastIndexByte(addr, ':')
	if i < 0 {
		return 0, false
	}
	port, err := strconv.Atoi(addr[i+1:])
	return port, err == nil && port > 0
}

// normalize sorts and de-duplicates each port list (IPv4 and IPv6 listeners
// on the same port count once).
func normalize(m map[int][]int) map[int][]int {
	for k, v := range m {
		slices.Sort(v)
		m[k] = slices.Compact(v)
	}
	return m
}
