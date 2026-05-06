package collector

import (
	"bufio"
	"os"
	"testing"
	"time"

	"github.com/bruceowenga/wbts/pkg/event"
)

func TestParseDmesgLine(t *testing.T) {
	// Use a synthetic boot time 1000 seconds before Unix epoch for easy math.
	bootTime := time.Unix(0, 0).Add(-1000 * time.Second)

	tests := []struct {
		name        string
		line        string
		wantOk      bool
		wantSecs    float64 // seconds since boot
		wantCat     event.Category
		wantLevel   event.Level
	}{
		{
			name:      "OOM kill line",
			line:      "[  300.123456] Killed process 14821 (node) total-vm:2048000kB",
			wantOk:    true,
			wantSecs:  300.123456,
			wantCat:   event.Kernel,
			wantLevel: event.Error,
		},
		{
			name:      "disk I/O error classified as Disk",
			line:      "[  420.000000] EXT4-fs error (device sda1): bad block bitmap",
			wantOk:    true,
			wantSecs:  420.0,
			wantCat:   event.Disk,
			wantLevel: event.Error,
		},
		{
			name:      "kernel panic classified as Critical",
			line:      "[  500.000000] kernel BUG at fs/ext4/inode.c:3180!",
			wantOk:    true,
			wantSecs:  500.0,
			wantCat:   event.Kernel,
			wantLevel: event.Critical,
		},
		{
			name:   "malformed — no brackets",
			line:   "this is not a dmesg line",
			wantOk: false,
		},
		{
			name:   "empty line",
			line:   "",
			wantOk: false,
		},
		{
			name:   "bracket but no message",
			line:   "[  300.000000] ",
			wantOk: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, ok := parseDmesgLine(tt.line, bootTime)
			if ok != tt.wantOk {
				t.Fatalf("parseDmesgLine ok = %v, want %v", ok, tt.wantOk)
			}
			if !tt.wantOk {
				return
			}
			wantTS := bootTime.Add(time.Duration(tt.wantSecs * float64(time.Second)))
			if !e.Timestamp.Equal(wantTS) {
				t.Errorf("timestamp = %v, want %v", e.Timestamp, wantTS)
			}
			if e.Category != tt.wantCat {
				t.Errorf("category = %v, want %v", e.Category, tt.wantCat)
			}
			if e.Level != tt.wantLevel {
				t.Errorf("level = %v, want %v", e.Level, tt.wantLevel)
			}
			if e.Source != "dmesg" {
				t.Errorf("source = %q, want dmesg", e.Source)
			}
		})
	}
}

func TestParseDmesgFixture(t *testing.T) {
	f, err := os.Open("../../testdata/dmesg/sample.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	bootTime := time.Now().Add(-600 * time.Second) // pretend we booted 600s ago

	var events []event.Event
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		e, ok := parseDmesgLine(scanner.Text(), bootTime)
		if ok {
			events = append(events, e)
		}
	}

	if len(events) == 0 {
		t.Fatal("no events parsed from fixture")
	}

	var diskCount, kernelCount int
	for _, e := range events {
		switch e.Category {
		case event.Disk:
			diskCount++
		case event.Kernel:
			kernelCount++
		}
	}
	if diskCount == 0 {
		t.Error("expected at least one Disk category event")
	}
	if kernelCount == 0 {
		t.Error("expected at least one Kernel category event")
	}
}

func TestTruncate(t *testing.T) {
	cases := []struct{ input string; n int; want string }{
		{"hello", 10, "hello"},
		{"hello world", 8, "hello..."},
		{"", 5, ""},
		{"abcde", 5, "abcde"},
		{"abcdef", 5, "ab..."},
	}
	for _, c := range cases {
		got := truncate(c.input, c.n)
		if got != c.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", c.input, c.n, got, c.want)
		}
	}
}
