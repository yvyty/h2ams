package resources

import (
	"os"
	"path/filepath"
	"strconv"
)

// clockTicks is USER_HZ, which is 100 on every mainstream Linux build. Reading
// it exactly needs sysconf(_SC_CLK_TCK) via cgo, which h2ams avoids.
const clockTicks = 100

// sample reads /proc directly, summing every process in each wanted group.
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
	ticks := make(map[int]uint64)
	out := make(map[int]Usage)
	for _, e := range entries {
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		data, err := os.ReadFile(filepath.Join("/proc", e.Name(), "stat"))
		if err != nil {
			continue // exited
		}
		st, ok := parseProcStat(string(data))
		if !ok || !want[st.pgrp] {
			continue
		}
		u := out[st.pgrp]
		u.MemoryBytes += st.rssPages * page
		out[st.pgrp] = u
		ticks[st.pgrp] += st.ticks
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
