// Package resources measures approximate CPU and memory use per process
// group, so an app's figures include every process it spawned.
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

// parsePS sums `ps -A -o pgid=,rss=,%cpu=` output (rss in KiB) per wanted
// process group.
func parsePS(r io.Reader, want map[int]bool) map[int]Usage {
	out := make(map[int]Usage)
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) != 3 {
			continue
		}
		pgid, err := strconv.Atoi(f[0])
		if err != nil || !want[pgid] {
			continue
		}
		rssKiB, err1 := strconv.ParseUint(f[1], 10, 64)
		cpu, err2 := strconv.ParseFloat(strings.Replace(f[2], ",", ".", 1), 64) // some locales use ','
		if err1 != nil || err2 != nil {
			continue
		}
		u := out[pgid]
		u.MemoryBytes += rssKiB * 1024
		u.CPUPercent += cpu
		out[pgid] = u
	}
	return out
}

// procStat is the part of /proc/<pid>/stat we use.
type procStat struct {
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
	pgrp, err1 := strconv.Atoi(f[2])
	utime, err2 := strconv.ParseUint(f[11], 10, 64)
	stime, err3 := strconv.ParseUint(f[12], 10, 64)
	rss, err4 := strconv.ParseInt(f[21], 10, 64)
	if err1 != nil || err2 != nil || err3 != nil || err4 != nil {
		return procStat{}, false
	}
	return procStat{pgrp: pgrp, ticks: utime + stime, rssPages: uint64(max(rss, 0))}, true
}

// cpuPercent converts a tick delta over elapsed wall time to percent of a core.
func cpuPercent(prev, cur cpuSample, ticksPerSecond float64) float64 {
	elapsed := cur.at.Sub(prev.at).Seconds()
	if elapsed <= 0 || cur.ticks < prev.ticks { // a group member exited; skip this interval
		return 0
	}
	return float64(cur.ticks-prev.ticks) / ticksPerSecond / elapsed * 100
}
