package ports

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseLsof(t *testing.T) {
	out := "p100\ng100\nf3\nn*:8080\nf4\nn[::1]:8080\nf5\nn127.0.0.1:9000\n" +
		"p101\ng100\nf3\nn127.0.0.1:7000\n" +
		"p200\ng200\nf7\nn*:443\n"
	got := parseLsof(strings.NewReader(out))
	want := map[int][]int{100: {7000, 8080, 9000}, 200: {443}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseLsof = %v, want %v", got, want)
	}
}

func TestParseProcNetTCP(t *testing.T) {
	data := `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 0100007F:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 12345 1 0000000000000000 100 0 0 10 0
   1: 0100007F:1F91 0100007F:C350 01 00000000:00000000 00:00000000 00000000  1000        0 12346 1 0000000000000000 20 4 30 10 -1
   2: 00000000000000000000000000000000:01BB 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 999 1 0000000000000000 100 0 0 10 0
`
	got := parseProcNetTCP(strings.NewReader(data))
	want := map[uint64]int{12345: 8080, 999: 443}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseProcNetTCP = %v, want %v", got, want)
	}
}

func TestParseStatPgrp(t *testing.T) {
	tests := map[string]int{
		"1234 (node) S 1 1234 1234 0 -1":            1234,
		"1235 (my app (dev)) R 1234 1200 1200 0 -1": 1200,
		"garbage": 0,
	}
	for in, want := range tests {
		if got := parseStatPgrp(in); got != want {
			t.Errorf("parseStatPgrp(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestListeningEmpty(t *testing.T) {
	got, err := Listening(nil)
	if err != nil || len(got) != 0 {
		t.Errorf("Listening(nil) = %v, %v", got, err)
	}
}
