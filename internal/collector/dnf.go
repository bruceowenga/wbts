package collector

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/bruceowenga/wbts/pkg/event"
)

// dnfLogPaths are checked in order; first readable file wins.
// dnf5 is the default on Fedora 41+; dnf (v4) on RHEL 8/9, older Fedora.
// yum.log covers RHEL 7 and CentOS 7.
var dnfLogPaths = []string{
	"/var/log/dnf5.rpm.log",
	"/var/log/dnf.rpm.log",
	"/var/log/yum.log",
}

// DnfCollector reads package transaction history for Fedora/RHEL/Rocky systems.
type DnfCollector struct{ path string }

func NewDnfCollector() *DnfCollector {
	for _, p := range dnfLogPaths {
		if _, err := os.Stat(p); err == nil {
			return &DnfCollector{path: p}
		}
	}
	return &DnfCollector{path: dnfLogPaths[1]} // Available() will report the error
}

func (d *DnfCollector) Name() string { return "dnf" }

func (d *DnfCollector) Available() error {
	f, err := os.Open(d.path)
	if err != nil {
		return fmt.Errorf("dnf log not readable at %s: %w", d.path, err)
	}
	f.Close()
	return nil
}

func (d *DnfCollector) Collect(ctx context.Context, opts event.Options) (<-chan event.Event, error) {
	r, closers, err := multiFileReader(rotatedPaths(d.path))
	if err != nil {
		return nil, fmt.Errorf("dnf: open logs: %w", err)
	}

	// yum.log has no year in timestamps — infer from the since boundary
	year := opts.Since.Year()

	ch := make(chan event.Event, 64)
	go func() {
		defer close(ch)
		defer func() {
			for _, c := range closers {
				c.Close()
			}
		}()

		events, err := parseDnfHistory(r, opts.Since, opts.Until, year)
		if err != nil {
			return
		}
		for _, e := range events {
			select {
			case ch <- e:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}

// dnfEntry is a single parsed line from dnf.rpm.log or yum.log.
type dnfEntry struct {
	Timestamp time.Time
	Action    string // Installed, Upgraded, Erased, Updated, etc.
	Package   string // full NVRA string
}

// parseDnfHistory parses the log file and groups consecutive entries within
// 60 seconds into single transaction events, matching the apt collector model.
func parseDnfHistory(r io.Reader, since, until time.Time, year int) ([]event.Event, error) {
	var entries []dnfEntry
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		e, ok := parseDnfLineWithYear(scanner.Text(), year)
		if !ok {
			continue
		}
		if e.Timestamp.Before(since) || e.Timestamp.After(until) {
			continue
		}
		entries = append(entries, e)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return groupDnfEntries(entries), nil
}

// groupDnfEntries collapses consecutive entries within 60s into one event.
func groupDnfEntries(entries []dnfEntry) []event.Event {
	if len(entries) == 0 {
		return nil
	}

	const txWindow = 60 * time.Second
	var events []event.Event
	group := []dnfEntry{entries[0]}

	for _, e := range entries[1:] {
		if e.Timestamp.Sub(group[0].Timestamp) <= txWindow {
			group = append(group, e)
		} else {
			events = append(events, dnfGroupToEvent(group))
			group = []dnfEntry{e}
		}
	}
	events = append(events, dnfGroupToEvent(group))
	return events
}

func dnfGroupToEvent(group []dnfEntry) event.Event {
	// Determine dominant action and level
	// Upgraded/Erased/Downgraded → Warn; Installed/Reinstalled → Info
	lvl := event.Info
	actionCounts := make(map[string]int)
	for _, e := range group {
		actionCounts[e.Action]++
		if dnfActionLevel(e.Action) == event.Warn {
			lvl = event.Warn
		}
	}

	// Pick primary action (highest count)
	primaryAction := group[0].Action
	maxCount := 0
	for a, c := range actionCounts {
		if c > maxCount {
			maxCount = c
			primaryAction = a
		}
	}

	// Build package list (first 3, then "... and N more")
	pkgs := make([]string, 0, len(group))
	for _, e := range group {
		pkgs = append(pkgs, formatDnfPackage(e.Package))
	}
	summary := buildDnfSummary(strings.ToLower(primaryAction), pkgs)

	raw := make([]string, len(group))
	for i, e := range group {
		raw[i] = e.Package
	}

	return event.Event{
		Timestamp: group[0].Timestamp,
		Source:    "dnf",
		Level:     lvl,
		Category:  event.Package,
		Summary:   summary,
		Raw:       strings.Join(raw, "\n"),
	}
}

func buildDnfSummary(action string, pkgs []string) string {
	total := len(pkgs)
	if total > 3 {
		pkgs = pkgs[:3]
	}
	result := fmt.Sprintf("dnf %s: %s", action, strings.Join(pkgs, ", "))
	if total > 3 {
		result += fmt.Sprintf(" ... and %d more", total-3)
	}
	return truncate(result, 140)
}

// dnfActionLevel maps dnf actions to event severity.
func dnfActionLevel(action string) event.Level {
	switch action {
	case "Upgraded", "Updated", "Erased", "Removed", "Downgraded", "Obsoleted":
		return event.Warn
	default:
		return event.Info
	}
}

// parseDnfLine parses a single line from dnf.rpm.log or yum.log.
// Returns (entry, false) for header/separator lines that should be skipped.
// year is used for yum.log which has no year in its timestamps; pass 0 to use current year.
func parseDnfLine(line string) (dnfEntry, bool) {
	return parseDnfLineWithYear(line, 0)
}

func parseDnfLineWithYear(line string, year int) (dnfEntry, bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return dnfEntry{}, false
	}

	// Try dnf.rpm.log format: "2026-05-06T17:49:51+0000 INFO Installed: go-1.22.0-1.fc40.x86_64"
	if e, ok := parseDnfRpmLine(line); ok {
		return e, true
	}

	// Try yum.log format: "May  6 10:30:01 Updated: nginx-1.16.1-3.el7.x86_64"
	if e, ok := parseYumLine(line, year); ok {
		return e, true
	}

	return dnfEntry{}, false
}

// dnfActionRe matches "Action: package" at the end of a dnf.rpm.log line.
var dnfActionRe = regexp.MustCompile(`\b(Installed|Upgraded|Erased|Removed|Reinstalled|Downgraded|Obsoleted|Updated):\s+(\S+)$`)

func parseDnfRpmLine(line string) (dnfEntry, bool) {
	// Minimum: "2026-05-06T10:30:00+0000 INFO ..."
	if len(line) < 25 {
		return dnfEntry{}, false
	}
	parts := strings.SplitN(line, " ", 3)
	if len(parts) < 3 {
		return dnfEntry{}, false
	}

	ts, err := parseDnfTimestamp(parts[0])
	if err != nil {
		return dnfEntry{}, false
	}

	rest := parts[2] // "INFO Installed: go-1.22.0-1.fc40.x86_64" or just "Installed: ..."
	// Strip leading level word if present (INFO/WARN/DEBUG)
	if strings.HasPrefix(rest, "INFO ") || strings.HasPrefix(rest, "WARN ") || strings.HasPrefix(rest, "DEBUG ") {
		rest = rest[strings.Index(rest, " ")+1:]
	}

	m := dnfActionRe.FindStringSubmatch(rest)
	if m == nil {
		return dnfEntry{}, false
	}

	return dnfEntry{Timestamp: ts, Action: m[1], Package: m[2]}, true
}

// yumLineRe matches "May  6 10:30:01 Action: package"
var yumLineRe = regexp.MustCompile(`^(\w{3}\s+\d{1,2}\s+\d{2}:\d{2}:\d{2})\s+(Installed|Updated|Erased|Upgraded|Removed|Reinstalled):\s+(\S+)$`)

func parseYumLine(line string, year int) (dnfEntry, bool) {
	m := yumLineRe.FindStringSubmatch(line)
	if m == nil {
		return dnfEntry{}, false
	}
	if year == 0 {
		year = time.Now().Year()
	}
	ts, err := parseAuthTimestamp(m[1], year)
	if err != nil {
		return dnfEntry{}, false
	}
	return dnfEntry{Timestamp: ts, Action: m[2], Package: m[3]}, true
}

// parseDnfTimestamp parses "2026-05-06T17:49:51+0000" (no colon in tz offset).
func parseDnfTimestamp(s string) (time.Time, error) {
	return time.Parse("2006-01-02T15:04:05-0700", s)
}

// nvraRe strips the release, dist, and arch suffixes from an NVRA string.
// e.g. "nginx-1.24.0-2.fc40.x86_64" → name="nginx", version="1.24.0"
var nvraRe = regexp.MustCompile(`^(.+)-(\d[^-]*)-[^-]+\.[^.]+$`)

// formatDnfPackage extracts a readable "name (version)" from an NVRA string.
func formatDnfPackage(nvra string) string {
	m := nvraRe.FindStringSubmatch(nvra)
	if m == nil {
		return nvra
	}
	return fmt.Sprintf("%s (%s)", m[1], m[2])
}
