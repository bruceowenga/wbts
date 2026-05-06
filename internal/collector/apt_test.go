package collector

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bruceowenga/wbts/pkg/event"
)

func TestParseAptTimestamp(t *testing.T) {
	cases := []struct {
		input   string
		wantErr bool
	}{
		{"2026-05-06  10:30:00", false}, // double space (standard apt format)
		{"2026-05-06 10:30:00", false},  // single space (normalised)
		{"not-a-date", true},
	}
	for _, c := range cases {
		_, err := parseAptTimestamp(c.input)
		if (err != nil) != c.wantErr {
			t.Errorf("parseAptTimestamp(%q) err=%v, wantErr=%v", c.input, err, c.wantErr)
		}
	}
}

func TestParseAptTimestamp_Value(t *testing.T) {
	ts, err := parseAptTimestamp("2026-05-06  10:30:00")
	if err != nil {
		t.Fatal(err)
	}
	if ts.Year() != 2026 || ts.Month() != 5 || ts.Day() != 6 {
		t.Errorf("got %v, want 2026-05-06", ts)
	}
	if ts.Hour() != 10 || ts.Minute() != 30 {
		t.Errorf("got %v, want 10:30", ts)
	}
}

func TestFormatAptPackages_Upgrade(t *testing.T) {
	input := "nginx:amd64 (1.18.0-6ubuntu14.3, 1.18.0-6ubuntu14.4), curl:amd64 (7.81.0-1, 7.82.0-1)"
	result := formatAptPackages("Upgrade", input)
	if !strings.Contains(result, "nginx") {
		t.Errorf("expected nginx in result, got: %s", result)
	}
	if !strings.Contains(result, "→") {
		t.Errorf("upgrade should use → arrow, got: %s", result)
	}
}

func TestFormatAptPackages_Install(t *testing.T) {
	input := "docker-ce:amd64 (5:24.0.0-1~ubuntu.22.04~jammy, automatic)"
	result := formatAptPackages("Install", input)
	if !strings.Contains(result, "docker-ce") {
		t.Errorf("expected docker-ce in result, got: %s", result)
	}
	// "automatic" should be stripped
	if strings.Contains(result, "automatic") {
		t.Errorf("'automatic' should be stripped from install summary, got: %s", result)
	}
}

func TestFormatAptPackages_Remove(t *testing.T) {
	input := "old-package:amd64 (1.0.0), old-dep:amd64 (0.9.1)"
	result := formatAptPackages("Remove", input)
	if !strings.Contains(result, "old-package") {
		t.Errorf("expected old-package in result, got: %s", result)
	}
}

func TestFormatAptPackages_LongList_Truncated(t *testing.T) {
	// More than 3 packages should be truncated with "... and N more"
	input := "a:amd64 (1, 2), b:amd64 (1, 2), c:amd64 (1, 2), d:amd64 (1, 2), e:amd64 (1, 2)"
	result := formatAptPackages("Upgrade", input)
	if !strings.Contains(result, "more") {
		t.Errorf("expected truncation with 'more', got: %s", result)
	}
}

func TestParseAptHistoryFromFixture(t *testing.T) {
	f, err := os.Open("../../testdata/apt/history.log")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	// Use a broad time range to get all fixture entries
	since := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	until := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)

	events, err := parseAptHistory(f, since, until)
	if err != nil {
		t.Fatal(err)
	}

	// Fixture has 4 entries (upgrade, install, remove, kernel upgrade)
	if len(events) != 4 {
		t.Fatalf("got %d events, want 4", len(events))
	}

	// First entry: upgrade → Warn
	if events[0].Level != event.Warn {
		t.Errorf("upgrade level = %v, want Warn", events[0].Level)
	}
	if events[0].Category != event.Package {
		t.Errorf("upgrade category = %v, want Package", events[0].Category)
	}
	if events[0].Source != "apt" {
		t.Errorf("source = %q, want apt", events[0].Source)
	}
	if !strings.Contains(events[0].Summary, "nginx") {
		t.Errorf("upgrade summary missing nginx: %s", events[0].Summary)
	}

	// Second entry: install → Info
	if events[1].Level != event.Info {
		t.Errorf("install level = %v, want Info", events[1].Level)
	}
	if !strings.Contains(events[1].Summary, "docker-ce") {
		t.Errorf("install summary missing docker-ce: %s", events[1].Summary)
	}

	// Third entry: remove → Warn
	if events[2].Level != event.Warn {
		t.Errorf("remove level = %v, want Warn", events[2].Level)
	}
}

func TestParseAptHistory_TimeRangeFilter(t *testing.T) {
	f, err := os.Open("../../testdata/apt/history.log")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	// Only include entries between 10:00 and 11:30 on 2026-05-06
	since := time.Date(2026, 5, 6, 10, 0, 0, 0, time.UTC)
	until := time.Date(2026, 5, 6, 11, 30, 0, 0, time.UTC)

	events, err := parseAptHistory(f, since, until)
	if err != nil {
		t.Fatal(err)
	}

	// Should get upgrade (10:30) and install (11:00), but not remove (12:00) or kernel upgrade (13:00)
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2 (only entries in range)", len(events))
	}
}
