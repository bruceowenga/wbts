package collector

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/bruceowenga/wbts/pkg/event"
)

// IPMICollector reads the Baseboard Management Controller System Event Log
// via ipmitool. BMC SEL captures hardware events that never reach the OS:
// PSU failures, thermal threshold crossings, drive media errors, fan failures.
// Requires ipmitool and either root or an IPMI device accessible to the user.
type IPMICollector struct{}

func NewIPMICollector() *IPMICollector { return &IPMICollector{} }

func (i *IPMICollector) Name() string { return "ipmi" }

func (i *IPMICollector) Available() error {
	if _, err := exec.LookPath("ipmitool"); err != nil {
		return fmt.Errorf("ipmitool not found in PATH (install: apt install ipmitool / dnf install ipmitool)")
	}
	// Probe IPMI device accessibility without reading the full SEL
	if err := exec.Command("ipmitool", "mc", "info").Run(); err != nil {
		return fmt.Errorf("ipmitool: cannot access IPMI device: %w (try running as root)", err)
	}
	return nil
}

func (i *IPMICollector) Collect(ctx context.Context, opts event.Options) (<-chan event.Event, error) {
	cmd := exec.CommandContext(ctx, "ipmitool", "sel", "elist")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("ipmi: pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("ipmi: start ipmitool: %w", err)
	}

	ch := make(chan event.Event, 256)
	year := opts.Since.Year()

	go func() {
		defer close(ch)
		defer func() { _ = cmd.Wait() }()

		events, err := parseIPMIOutput(stdout, opts.Since, opts.Until, year)
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

// parseIPMIOutput parses `ipmitool sel elist` output into events.
func parseIPMIOutput(r io.Reader, since, until time.Time, year int) ([]event.Event, error) {
	var events []event.Event
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		e, ok := parseIPMILine(scanner.Text(), year)
		if !ok {
			continue
		}
		if e.Timestamp.Before(since) || e.Timestamp.After(until) {
			continue
		}
		events = append(events, e)
	}
	return events, scanner.Err()
}

// parseIPMILine parses a single `ipmitool sel elist` line.
// Format: "   ID | MM/DD/YYYY | HH:MM:SS | Name | Type | State"
func parseIPMILine(line string, year int) (event.Event, bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return event.Event{}, false
	}

	parts := strings.Split(line, "|")
	if len(parts) < 6 {
		return event.Event{}, false
	}

	dateStr := strings.TrimSpace(parts[1]) // MM/DD/YYYY
	timeStr := strings.TrimSpace(parts[2]) // HH:MM:SS
	name := strings.TrimSpace(parts[3])
	evtType := strings.TrimSpace(parts[4])
	state := strings.TrimSpace(parts[5])

	ts, err := time.ParseInLocation("01/02/2006 15:04:05", dateStr+" "+timeStr, time.UTC)
	if err != nil {
		return event.Event{}, false
	}
	// ipmitool includes the year in MM/DD/YYYY so year param is a fallback
	_ = year

	lvl := ipmiEventLevel(evtType, state)
	summary := buildIPMISummary(name, evtType, state)

	return event.Event{
		Timestamp: ts,
		Source:    "ipmi",
		Level:     lvl,
		Category:  event.Kernel,
		Summary:   summary,
		Raw:       line,
	}, true
}

// ipmiEventLevel maps BMC event type and assertion state to severity.
// Non-recoverable and Uncorrectable events are Critical.
// Critical threshold crossings and power failures are Error.
// Non-critical thresholds and deassertions are Warn or Info.
func ipmiEventLevel(evtType, state string) event.Level {
	lower := strings.ToLower(evtType)

	// Deasserted events are recoveries — always Info
	if strings.ToLower(state) == "deasserted" {
		return event.Info
	}

	switch {
	case strings.Contains(lower, "non-recoverable"),
		strings.Contains(lower, "uncorrectable"):
		return event.Critical

	case strings.Contains(lower, "critical") && !strings.Contains(lower, "non-critical"),
		strings.Contains(lower, "ac lost"),
		strings.Contains(lower, "power off"),
		strings.Contains(lower, "drive fault"),
		strings.Contains(lower, "failure detected"):
		return event.Error

	case strings.Contains(lower, "non-critical"),
		strings.Contains(lower, "correctable"):
		return event.Warn

	default:
		return event.Info
	}
}

func buildIPMISummary(name, evtType, state string) string {
	s := fmt.Sprintf("BMC: %s — %s", name, evtType)
	if state != "" && strings.ToLower(state) != "asserted" {
		s += " (" + state + ")"
	}
	return truncate(s, 120)
}

// openIPMILog is a helper for testing that reads from a file instead of ipmitool.
func openIPMILog(path string) (*os.File, error) {
	return os.Open(path)
}
