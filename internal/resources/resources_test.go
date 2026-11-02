package resources

import (
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParsePS(t *testing.T) {
	out := `    1     0     1  21472   0.0
  100     1   100   1024   1.5
  101   100   100   2048  10,5
  200     1   200    512   3.0
  bad   line
`
	got := sumPS(parsePS(strings.NewReader(out)), map[int]bool{100: true, 300: true})
	want := map[int]Usage{100: {CPUPercent: 12, MemoryBytes: 3072 * 1024}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("sumPS = %+v, want %+v", got, want)
	}
}

func TestOwnersFollowsDescendantsIntoNewGroups(t *testing.T) {
	procs := []proc{
		{pid: 1, ppid: 0, pgid: 1},
		{pid: 50, ppid: 1, pgid: 50},     // the h2ams daemon
		{pid: 100, ppid: 50, pgid: 100},  // app "web" (group leader)
		{pid: 110, ppid: 100, pgid: 110}, // helper that called setsid (erl_child_setup)
		{pid: 111, ppid: 110, pgid: 111}, // its child, another new group (esbuild)
		{pid: 200, ppid: 50, pgid: 200},  // app "api"
		{pid: 210, ppid: 200, pgid: 200}, // api's child in its group
		{pid: 300, ppid: 1, pgid: 300},   // unrelated
		{pid: 400, ppid: 401, pgid: 400}, // pid-reuse cycle, unrelated
		{pid: 401, ppid: 400, pgid: 401},
	}
	got := map[int]int{}
	for i, o := range owners(procs, map[int]bool{100: true, 200: true}) {
		got[procs[i].pid] = o
	}
	want := map[int]int{100: 100, 110: 100, 111: 100, 200: 200, 210: 200}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("owners = %v, want %v", got, want)
	}
}

func TestParseProcStat(t *testing.T) {
	// pid (comm) state ppid pgrp session tty tpgid flags minflt cminflt majflt cmajflt utime stime cutime cstime priority nice threads itreal starttime vsize rss
	stat := "4242 (my app (dev)) S 1 4200 4200 0 -1 4194304 100 0 0 0 250 50 0 0 20 0 3 0 12345 1000000 777 18446744073709551615"
	got, ok := parseProcStat(stat)
	if !ok {
		t.Fatal("parse failed")
	}
	want := procStat{ppid: 1, pgrp: 4200, ticks: 300, rssPages: 777}
	if got != want {
		t.Errorf("parseProcStat = %+v, want %+v", got, want)
	}
	if _, ok := parseProcStat("4242 (short) S 1"); ok {
		t.Error("truncated stat parsed")
	}
}

func TestCPUPercent(t *testing.T) {
	at := time.Unix(1000, 0)
	tests := []struct {
		name      string
		prev, cur cpuSample
		want      float64
	}{
		{"one full core", cpuSample{100, at}, cpuSample{300, at.Add(2 * time.Second)}, 100},
		{"quarter core", cpuSample{0, at}, cpuSample{50, at.Add(2 * time.Second)}, 25},
		{"two cores", cpuSample{0, at}, cpuSample{200, at.Add(time.Second)}, 200},
		{"ticks went backwards", cpuSample{500, at}, cpuSample{100, at.Add(time.Second)}, 0},
		{"no time elapsed", cpuSample{0, at}, cpuSample{100, at}, 0},
	}
	for _, tt := range tests {
		if got := cpuPercent(tt.prev, tt.cur, 100); math.Abs(got-tt.want) > 1e-9 {
			t.Errorf("%s: cpuPercent = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestSampleEmpty(t *testing.T) {
	got, err := NewSampler().Sample(nil)
	if err != nil || len(got) != 0 {
		t.Errorf("Sample(nil) = %v, %v", got, err)
	}
}
