package resources

import (
	"os"
	"path/filepath"
	"strconv"
)

// clockTicks is USER_HZ, which is 100 on every mainstream Linux build. Reading
// it exactly needs sysconf(_SC_CLK_TCK) via cgo, which h2ams avoids.
const clockTicks = 100

// sample reads /proc directly, summing every process that belongs to each
// wanted group (see owners).
func (s *Sampler) sample(pgids []int) (map[int]Usage, error) {
	want := make(map[int]bool, len(pgids))
	for _, id := range pgids {
		want[id] = true
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	page := uint64(os.Getpagesize())
	var procs []proc
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		data, err := os.ReadFile(filepath.Join("/proc", e.Name(), "stat"))
		if err != nil {
			continue // exited
		}
		if st, ok := parseProcStat(string(data)); ok {
			procs = append(procs, proc{pid: pid, ppid: st.ppid, pgid: st.pgrp, rssBytes: st.rssPages * page, ticks: st.ticks})
		}
	}
	ticks := make(map[int]uint64)
	out := make(map[int]Usage)
	for i, owner := range owners(procs, want) {
		u := out[owner]
		u.MemoryBytes += procs[i].rssBytes
		out[owner] = u
		ticks[owner] += procs[i].ticks
	}

	now := s.now()
	next := make(map[int]cpuSample, len(ticks))
	for pgid, t := range ticks {
		cur := cpuSample{ticks: t, at: now}
		if prev, ok := s.prev[pgid]; ok {
			u := out[pgid]
			u.CPUPercent = cpuPercent(prev, cur, clockTicks)
			out[pgid] = u
		}
		next[pgid] = cur
	}
	s.prev = next // forget groups that are gone
	return out, nil
}
