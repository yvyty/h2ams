// Package resources measures approximate CPU and memory use per app. An app
// is identified by its process group, and its figures include every process
// in that group plus every descendant that moved to a group of its own (as
// Erlang's port programs and other setsid-ing helpers do).
package resources

import (
	"bufio"
	"io"
	"strconv"
	"strings"
	"time"
)

// Usage is the combined resource use of one process group.
type Usage struct {
	CPUPercent  float64 // percent of one core; can exceed 100 on multi-core
	MemoryBytes uint64  // resident set size
}

// Sampler collects Usage. CPU on Linux is measured between consecutive calls,
// so the first sample of a group reports 0% CPU. Not safe for concurrent use.
type Sampler struct {
	prev map[int]cpuSample
	now  func() time.Time
}

type cpuSample struct {
	ticks uint64
	at    time.Time
}

// NewSampler returns a ready Sampler.
func NewSampler() *Sampler {
	return &Sampler{prev: make(map[int]cpuSample), now: time.Now}
}

// Sample returns usage for each process group ID that has live processes.
// On unsupported platforms it returns errors.ErrUnsupported.
func (s *Sampler) Sample(pgids []int) (map[int]Usage, error) {
	if len(pgids) == 0 {
		return map[int]Usage{}, nil
	}
	return s.sample(pgids)
}

// proc is one process as read from ps or /proc.
type proc struct {
	pid, ppid, pgid int
	rssBytes        uint64
	cpu             float64 // ps only: percent of a core
	ticks           uint64  // /proc only: utime + stime
}

// maxDepth bounds the walk up the parent chain (and guards against cycles
// from pid reuse between reads).
const maxDepth = 64

// owners maps each process index to the wanted group it belongs to: its own
// group if wanted, otherwise the nearest ancestor's wanted group. Processes
// that belong to no wanted group are absent.
func owners(procs []proc, want map[int]bool) map[int]int {
	byPid := make(map[int]int, len(procs))
	for i, p := range procs {
		byPid[p.pid] = i
	}
	memo := make(map[int]int, len(procs)) // pid -> owner pgid, 0 = none
	var owner func(pid, depth int) int
	owner = func(pid, depth int) int {
		if o, ok := memo[pid]; ok {
			return o
		}
		i, ok := byPid[pid]
		if !ok || depth > maxDepth || pid <= 1 {
			return 0
		}
		o := 0
		if p := procs[i]; want[p.pgid] {
			o = p.pgid
		} else if p.ppid != pid {
			o = owner(p.ppid, depth+1)
		}
		memo[pid] = o
		return o
	}
	out := make(map[int]int)
	for i, p := range procs {
		if o := owner(p.pid, 0); o != 0 {
			out[i] = o
		}
	}
	return out
}

// parsePS reads `ps -A -o pid=,ppid=,pgid=,rss=,%cpu=` output (rss in KiB).
func parsePS(r io.Reader) []proc {
	var procs []proc
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) != 5 {
			continue
		}
		pid, err1 := strconv.Atoi(f[0])
		ppid, err2 := strconv.Atoi(f[1])
		pgid, err3 := strconv.Atoi(f[2])
		rssKiB, err4 := strconv.ParseUint(f[3], 10, 64)
		cpu, err5 := strconv.ParseFloat(strings.Replace(f[4], ",", ".", 1), 64) // some locales use ','
		if err1 != nil || err2 != nil || err3 != nil || err4 != nil || err5 != nil {
			continue
		}
		procs = append(procs, proc{pid: pid, ppid: ppid, pgid: pgid, rssBytes: rssKiB * 1024, cpu: cpu})
	}
	return procs
}

// sumPS totals ps figures per wanted group.
func sumPS(procs []proc, want map[int]bool) map[int]Usage {
	out := make(map[int]Usage)
	for i, owner := range owners(procs, want) {
		u := out[owner]
		u.MemoryBytes += procs[i].rssBytes
		u.CPUPercent += procs[i].cpu
		out[owner] = u
	}
	return out
}

// procStat is the part of /proc/<pid>/stat we use.
type procStat struct {
	ppid     int
	pgrp     int
	ticks    uint64 // utime + stime, in clock ticks
	rssPages uint64
}

// parseProcStat parses /proc/<pid>/stat. The command name may contain spaces
// and parens, so fields are counted from the last ')'.
func parseProcStat(stat string) (procStat, bool) {
	i := strings.LastIndexByte(stat, ')')
	if i < 0 {
		return procStat{}, false
	}
	// Fields after ')' start at field 3 (state): pgrp is field 5, utime 14,
	// stime 15, rss 24 (1-based, per proc(5)).
	f := strings.Fields(stat[i+1:])
	if len(f) < 22 {
		return procStat{}, false
	}
	ppid, err0 := strconv.Atoi(f[1])
	pgrp, err1 := strconv.Atoi(f[2])
	utime, err2 := strconv.ParseUint(f[11], 10, 64)
	stime, err3 := strconv.ParseUint(f[12], 10, 64)
	rss, err4 := strconv.ParseInt(f[21], 10, 64)
	if err0 != nil || err1 != nil || err2 != nil || err3 != nil || err4 != nil {
		return procStat{}, false
	}
	return procStat{ppid: ppid, pgrp: pgrp, ticks: utime + stime, rssPages: uint64(max(rss, 0))}, true
}

// cpuPercent converts a tick delta over elapsed wall time to percent of a core.
func cpuPercent(prev, cur cpuSample, ticksPerSecond float64) float64 {
	elapsed := cur.at.Sub(prev.at).Seconds()
	if elapsed <= 0 || cur.ticks < prev.ticks { // a group member exited; skip this interval
		return 0
	}
	return float64(cur.ticks-prev.ticks) / ticksPerSecond / elapsed * 100
}
