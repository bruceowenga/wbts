package collector

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/bruceowenga/wbts/pkg/event"
	_ "modernc.org/sqlite" // pure-Go SQLite driver, no CGO required
)

const rasDaemonDBPath = "/var/lib/rasdaemon/ras-mc.db"

// RasdaemonCollector reads hardware ECC and MCA errors from the rasdaemon
// SQLite database. rasdaemon intercepts Machine Check Architecture (MCA)
// events from the kernel and stores them with full context — DIMM label,
// error type, error count — that never appears in journald or dmesg.
type RasdaemonCollector struct{ path string }

func NewRasdaemonCollector() *RasdaemonCollector {
	return &RasdaemonCollector{path: rasDaemonDBPath}
}

func (r *RasdaemonCollector) Name() string { return "rasdaemon" }

func (r *RasdaemonCollector) Available() error {
	if _, err := os.Stat(r.path); err != nil {
		return fmt.Errorf("rasdaemon database not found at %s (is rasdaemon installed and running?)", r.path)
	}
	// Quick open check — verifies permissions and valid SQLite file
	db, err := sql.Open("sqlite", r.path)
	if err != nil {
		return fmt.Errorf("rasdaemon: open db: %w", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		return fmt.Errorf("rasdaemon: cannot read db: %w", err)
	}
	return nil
}

func (r *RasdaemonCollector) Collect(ctx context.Context, opts event.Options) (<-chan event.Event, error) {
	db, err := sql.Open("sqlite", r.path)
	if err != nil {
		return nil, fmt.Errorf("rasdaemon: open: %w", err)
	}

	ch := make(chan event.Event, 256)
	go func() {
		defer close(ch)
		defer db.Close()

		// rasdaemon timestamps: "2026-05-06 18:13:42 +0000"
		const tsLayout = "2006-01-02 15:04:05 -0700"
		sinceStr := opts.Since.UTC().Format(tsLayout)
		untilStr := opts.Until.UTC().Format(tsLayout)

		rows, err := db.QueryContext(ctx, `
			SELECT timestamp, err_count, err_type, err_msg, label
			FROM mc_event
			WHERE timestamp >= ? AND timestamp <= ?
			ORDER BY timestamp ASC
		`, sinceStr, untilStr)
		if err != nil {
			return
		}
		defer rows.Close()

		for rows.Next() {
			var tsStr, errType, errMsg, label string
			var errCount int
			if err := rows.Scan(&tsStr, &errCount, &errType, &errMsg, &label); err != nil {
				continue
			}
			ts, err := time.Parse(tsLayout, tsStr)
			if err != nil {
				continue
			}

			lvl := rasEventLevel(errType)
			summary := buildRasSummary(errType, errMsg, label, errCount)

			select {
			case ch <- event.Event{
				Timestamp: ts,
				Source:    "rasdaemon",
				Level:     lvl,
				Category:  event.Kernel,
				Summary:   summary,
				Raw:       fmt.Sprintf("type=%s count=%d label=%s msg=%s", errType, errCount, label, errMsg),
			}:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}

// rasEventLevel maps rasdaemon error types to severity.
// Correctable errors (CE) are recoverable but indicate hardware degradation.
// Uncorrectable errors (UE) are unrecoverable and precede data corruption or crashes.
func rasEventLevel(errType string) event.Level {
	upper := strings.ToUpper(errType)
	if strings.Contains(upper, "UNCORRECT") || strings.Contains(upper, "UE") ||
		strings.Contains(upper, "FATAL") {
		return event.Critical
	}
	return event.Warn // CE / Corrected / unknown — hardware degrading but recoverable
}

func buildRasSummary(errType, errMsg, label string, errCount int) string {
	short := truncate(errMsg, 80)
	s := fmt.Sprintf("hardware %s: %s", strings.ToLower(errType), short)
	if label != "" {
		s += " [" + label + "]"
	}
	if errCount > 1 {
		s += fmt.Sprintf(" (×%d)", errCount)
	}
	return truncate(s, 120)
}
