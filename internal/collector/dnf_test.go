package collector

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bruceowenga/wbts/pkg/event"
)

func TestParseDnfTimestamp(t *testing.T) {
	cases := []struct {
		input   string
		wantErr bool
		wantHr  int
	}{
		{"2026-05-06T17:49:51+0000", false, 17},
		{"2026-05-06T10:30:01+0000", false, 10},
		{"not-a-date", true, 0},
	}
	for _, c := range cases {
		ts, err := parseDnfTimestamp(c.input)
		if (err != nil) != c.wantErr {
			t.Errorf("parseDnfTimestamp(%q) err=%v, wantErr=%v", c.input, err, c.wantErr)
			continue
		}
		if !c.wantErr && ts.Hour() != c.wantHr {
			t.Errorf("parseDnfTimestamp(%q) hour=%d, want %d", c.input, ts.Hour(), c.wantHr)
		}
	}
}

func TestParseDnfLine(t *testing.T) {
	cases := []struct {
		line      string
		wantOk    bool
		wantAction string
		wantPkg   string
	}{
		{
			line:       "2026-05-06T17:49:51+0000 INFO Installed: go-1.22.0-1.fc40.x86_64",
			wantOk:     true,
			wantAction: "Installed",
			wantPkg:    "go-1.22.0-1.fc40.x86_64",
		},
		{
			line:       "2026-05-06T10:30:01+0000 INFO Upgraded: nginx-1.24.0-2.fc40.x86_64",
			wantOk:     true,
			wantAction: "Upgraded",
			wantPkg:    "nginx-1.24.0-2.fc40.x86_64",
		},
		{
			line:       "2026-05-06T12:00:01+0000 INFO Erased: old-package-1.0.0-1.fc40.x86_64",
			wantOk:     true,
			wantAction: "Erased",
			wantPkg:    "old-package-1.0.0-1.fc40.x86_64",
		},
		{
			line:   "2026-05-06T10:30:00+0000 INFO --- logging initialized ---",
			wantOk: false, // header lines are skipped
		},
		{
			line:   "",
			wantOk: false,
		},
	}
	for _, c := range cases {
		entry, ok := parseDnfLine(c.line)
		if ok != c.wantOk {
			t.Errorf("parseDnfLine(%q) ok=%v, want %v", c.line, ok, c.wantOk)
			continue
		}
		if !c.wantOk {
			continue
		}
		if entry.Action != c.wantAction {
			t.Errorf("action=%q, want %q", entry.Action, c.wantAction)
		}
		if entry.Package != c.wantPkg {
			t.Errorf("package=%q, want %q", entry.Package, c.wantPkg)
		}
	}
}

func TestFormatDnfPackage(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"go-1.22.0-1.fc40.x86_64", "go (1.22.0)"},
		{"nginx-1.24.0-2.fc40.x86_64", "nginx (1.24.0)"},
		{"old-package-1.0.0-1.fc40.x86_64", "old-package (1.0.0)"},
		{"docker-ce-26.0.0-1.fc40.x86_64", "docker-ce (26.0.0)"},
	}
	for _, c := range cases {
		got := formatDnfPackage(c.input)
		if got != c.want {
			t.Errorf("formatDnfPackage(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestDnfActionLevel(t *testing.T) {
	cases := []struct{ action string; want event.Level }{
		{"Upgraded", event.Warn},
		{"Erased", event.Warn},
		{"Downgraded", event.Warn},
		{"Installed", event.Info},
		{"Reinstalled", event.Info},
	}
	for _, c := range cases {
		got := dnfActionLevel(c.action)
		if got != c.want {
			t.Errorf("dnfActionLevel(%q) = %v, want %v", c.action, got, c.want)
		}
	}
}

func TestParseDnfHistoryFromFixture(t *testing.T) {
	f, err := os.Open("../../testdata/dnf/dnf.rpm.log")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	since := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	until := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)

	events, err := parseDnfHistory(f, since, until, 0)
	if err != nil {
		t.Fatal(err)
	}

	// Fixture has 4 transactions: upgrade(3pkgs), install(2pkgs), erase(1pkg), install go(2pkgs)
	if len(events) != 4 {
		t.Fatalf("got %d events, want 4", len(events))
	}

	// First: upgrade → Warn
	if events[0].Level != event.Warn {
		t.Errorf("upgrade level = %v, want Warn", events[0].Level)
	}
	if !strings.Contains(events[0].Summary, "nginx") {
		t.Errorf("upgrade summary missing nginx: %s", events[0].Summary)
	}
	if events[0].Category != event.Package {
		t.Errorf("category = %v, want Package", events[0].Category)
	}
	if events[0].Source != "dnf" {
		t.Errorf("source = %q, want dnf", events[0].Source)
	}

	// Second: install → Info
	if events[1].Level != event.Info {
		t.Errorf("install level = %v, want Info", events[1].Level)
	}
	if !strings.Contains(events[1].Summary, "docker-ce") {
		t.Errorf("install summary missing docker-ce: %s", events[1].Summary)
	}

	// Third: erase → Warn
	if events[2].Level != event.Warn {
		t.Errorf("erase level = %v, want Warn", events[2].Level)
	}
}

func TestParseDnfHistory_TimeRangeFilter(t *testing.T) {
	f, err := os.Open("../../testdata/dnf/dnf.rpm.log")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	since := time.Date(2026, 5, 6, 10, 0, 0, 0, time.UTC)
	until := time.Date(2026, 5, 6, 11, 30, 0, 0, time.UTC)

	events, err := parseDnfHistory(f, since, until, 0)
	if err != nil {
		t.Fatal(err)
	}

	// Only upgrade (10:30) and install (11:00) are in range
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2 (time range filtered)", len(events))
	}
}

func TestParseYumHistoryFromFixture(t *testing.T) {
	f, err := os.Open("../../testdata/dnf/yum.log")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	since := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	until := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)

	events, err := parseDnfHistory(f, since, until, 2026)
	if err != nil {
		t.Fatal(err)
	}

	// 3 transactions: update(2pkgs), install(1pkg), erase(1pkg)
	if len(events) != 3 {
		t.Fatalf("got %d events, want 3", len(events))
	}
	if events[0].Level != event.Warn {
		t.Errorf("update level = %v, want Warn", events[0].Level)
	}
}
