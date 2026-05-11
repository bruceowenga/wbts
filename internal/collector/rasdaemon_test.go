package collector

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bruceowenga/wbts/pkg/event"
	_ "modernc.org/sqlite"
)

// createTestRasDB creates a temporary rasdaemon SQLite database with test data.
func createTestRasDB(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "ras-mc.db")

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	_, err = db.Exec(`CREATE TABLE mc_event (
		id INTEGER NOT NULL PRIMARY KEY,
		timestamp TEXT,
		err_count INTEGER,
		err_type TEXT,
		err_msg TEXT,
		label TEXT,
		mc INTEGER,
		top_layer INTEGER,
		middle_layer INTEGER,
		lower_layer INTEGER,
		address INTEGER,
		grain INTEGER,
		syndrome INTEGER,
		driver_detail TEXT
	)`)
	if err != nil {
		t.Fatal(err)
	}

	events := []struct {
		ts      string
		count   int
		errType string
		msg     string
		label   string
	}{
		{"2026-05-06 17:13:00 +0000", 1, "Corrected", "CE memory read error on CPU_SrcID#0_Ha#0_Chan#0_DIMM#0", "DIMM_A1"},
		{"2026-05-06 17:14:00 +0000", 5, "Corrected", "CE memory read error on CPU_SrcID#0_Ha#0_Chan#0_DIMM#0", "DIMM_A1"},
		{"2026-05-06 17:20:00 +0000", 1, "Uncorrected", "UE memory error on CPU_SrcID#0_Ha#0_Chan#1_DIMM#0", "DIMM_B1"},
		{"2026-05-06 19:00:00 +0000", 1, "Corrected", "CE memory read error outside time range", "DIMM_A1"},
	}

	for _, e := range events {
		_, err = db.Exec(
			`INSERT INTO mc_event (timestamp, err_count, err_type, err_msg, label) VALUES (?, ?, ?, ?, ?)`,
			e.ts, e.count, e.errType, e.msg, e.label,
		)
		if err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func TestRasEventLevel(t *testing.T) {
	cases := []struct {
		errType string
		want    event.Level
	}{
		{"Corrected", event.Warn},
		{"CE", event.Warn},
		{"Uncorrected", event.Critical},
		{"UE", event.Critical},
		{"Fatal", event.Critical},
		{"unknown", event.Warn}, // default to warn for unknown hardware errors
	}
	for _, c := range cases {
		got := rasEventLevel(c.errType)
		if got != c.want {
			t.Errorf("rasEventLevel(%q) = %v, want %v", c.errType, got, c.want)
		}
	}
}

func TestRasdaemonCollect_TimeRangeFilter(t *testing.T) {
	dbPath := createTestRasDB(t)
	c := &RasdaemonCollector{path: dbPath}

	if err := c.Available(); err != nil {
		t.Fatalf("Available() = %v, want nil", err)
	}

	since := time.Date(2026, 5, 6, 17, 0, 0, 0, time.UTC)
	until := time.Date(2026, 5, 6, 18, 0, 0, 0, time.UTC)

	opts := event.Options{Since: since, Until: until}
	ch, err := c.Collect(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}

	var events []event.Event
	for e := range ch {
		events = append(events, e)
	}

	// 3 events in range (the 19:00 event is outside)
	if len(events) != 3 {
		t.Fatalf("got %d events, want 3 (time range filtered)", len(events))
	}

	for _, e := range events {
		if e.Source != "rasdaemon" {
			t.Errorf("source = %q, want rasdaemon", e.Source)
		}
		if e.Category != event.Kernel {
			t.Errorf("category = %v, want Kernel", e.Category)
		}
	}
}

func TestRasdaemonCollect_LevelClassification(t *testing.T) {
	dbPath := createTestRasDB(t)
	c := &RasdaemonCollector{path: dbPath}

	since := time.Date(2026, 5, 6, 17, 0, 0, 0, time.UTC)
	until := time.Date(2026, 5, 6, 18, 0, 0, 0, time.UTC)

	ch, err := c.Collect(context.Background(), event.Options{Since: since, Until: until})
	if err != nil {
		t.Fatal(err)
	}

	var critCount, warnCount int
	for e := range ch {
		switch e.Level {
		case event.Critical:
			critCount++
		case event.Warn:
			warnCount++
		}
		// Summary should contain the label
		if !strings.Contains(e.Summary, "DIMM") {
			t.Errorf("summary missing DIMM label: %s", e.Summary)
		}
	}

	if critCount != 1 { // one Uncorrected error
		t.Errorf("Critical count = %d, want 1", critCount)
	}
	if warnCount != 2 { // two Corrected errors
		t.Errorf("Warn count = %d, want 2", warnCount)
	}
}

func TestRasdaemonAvailable_MissingDB(t *testing.T) {
	c := &RasdaemonCollector{path: "/nonexistent/ras-mc.db"}
	if err := c.Available(); err == nil {
		t.Error("expected error for missing database")
	}
}

func TestRasdaemonAvailable_NotInstalled(t *testing.T) {
	// Verify NewRasdaemonCollector picks up the default path
	c := NewRasdaemonCollector()
	if c.path == "" {
		t.Error("path should not be empty")
	}
	// On a non-rasdaemon system, Available() should return an informative error
	if _, err := os.Stat(c.path); os.IsNotExist(err) {
		avErr := c.Available()
		if avErr == nil {
			t.Error("Available() should return error when DB does not exist")
		}
		if !strings.Contains(avErr.Error(), "rasdaemon") {
			t.Errorf("error should mention rasdaemon: %s", avErr.Error())
		}
	}
}
