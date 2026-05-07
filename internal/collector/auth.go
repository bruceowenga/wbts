package collector

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/bruceowenga/wbts/pkg/event"
)

// authLogPaths are checked in order; first readable file wins.
// /var/log/auth.log = Debian/Ubuntu; /var/log/secure = RHEL/CentOS/Fedora.
var authLogPaths = []string{"/var/log/auth.log", "/var/log/secure"}

// AuthCollector reads authentication events from the system auth log.
type AuthCollector struct{ path string }

func NewAuthCollector() *AuthCollector {
	for _, p := range authLogPaths {
		if _, err := os.Stat(p); err == nil {
			return &AuthCollector{path: p}
		}
	}
	return &AuthCollector{path: authLogPaths[0]} // Available() will report the error
}

func (a *AuthCollector) Name() string { return "auth" }

func (a *AuthCollector) Available() error {
	f, err := os.Open(a.path)
	if err != nil {
		return fmt.Errorf("auth log not readable at %s: %w (try: sudo usermod -aG adm $USER)", a.path, err)
	}
	f.Close()
	return nil
}

func (a *AuthCollector) Collect(ctx context.Context, opts event.Options) (<-chan event.Event, error) {
	r, closers, err := multiFileReader(rotatedPaths(a.path))
	if err != nil {
		return nil, fmt.Errorf("auth: open logs: %w", err)
	}

	ch := make(chan event.Event, 256)
	go func() {
		defer close(ch)
		defer func() {
			for _, c := range closers {
				c.Close()
			}
		}()

		year := opts.Since.Year()
		events, err := parseAuthLog(r, opts.Since, opts.Until, year)
		if err != nil {
			return
		}
		for _, e := range events {
			if opts.Filter.Container != "" {
				continue // auth events are never container-specific
			}
			select {
			case ch <- e:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}

// parseAuthLog parses the auth log, returning events in [since, until].
func parseAuthLog(r io.Reader, since, until time.Time, year int) ([]event.Event, error) {
	var events []event.Event
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		e, ok := parseAuthLine(scanner.Text(), year)
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

// parseAuthLine parses a single syslog-format auth line.
// Format: "Mon DD HH:MM:SS hostname process[pid]: message"
func parseAuthLine(line string, year int) (event.Event, bool) {
	// Syslog timestamp is exactly 15 chars: "May  6 18:30:00"
	if len(line) < 16 {
		return event.Event{}, false
	}
	tsStr := line[:15]
	rest := strings.TrimSpace(line[15:])

	ts, err := parseAuthTimestamp(tsStr, year)
	if err != nil {
		return event.Event{}, false
	}

	// rest = "hostname process[pid]: message"
	parts := strings.SplitN(rest, " ", 3)
	if len(parts) < 3 {
		return event.Event{}, false
	}
	processField := parts[1] // "sshd[1234]:" — colon included by SplitN
	msg := parts[2]         // already clean: SplitN strips the process[pid]: prefix

	lvl, emit := authEventLevel(msg)
	if !emit {
		return event.Event{}, false
	}

	// Strip PID from process name: "sshd[1234]" → "sshd"
	procName := processField
	if idx := strings.Index(processField, "["); idx > 0 {
		procName = processField[:idx]
	}

	return event.Event{
		Timestamp: ts,
		Source:    "auth",
		Level:     lvl,
		Category:  event.Auth,
		Summary:   truncate(procName+": "+msg, 120),
		Raw:       line,
	}, true
}

// authEventLevel classifies auth log messages. Returns (level, true) for events
// worth emitting, or (_, false) to skip the line entirely.
func authEventLevel(msg string) (event.Level, bool) {
	lower := strings.ToLower(msg)

	// Skip noisy connection-lifecycle lines that may contain keywords from
	// other patterns (e.g. "Connection closed by invalid user" contains "invalid user").
	if strings.HasPrefix(lower, "connection closed") ||
		strings.HasPrefix(lower, "disconnect from") ||
		strings.HasPrefix(lower, "received disconnect") {
		return event.Info, false
	}

	switch {
	case strings.Contains(lower, "maximum authentication attempts exceeded"):
		return event.Error, true

	case strings.Contains(lower, "failed password"),
		strings.Contains(lower, "authentication failure"),
		strings.Contains(lower, "invalid user"),
		strings.Contains(lower, "did not receive identification string"):
		return event.Warn, true

	case strings.Contains(lower, "accepted publickey"),
		strings.Contains(lower, "accepted password"),
		strings.Contains(lower, "accepted keyboard-interactive"):
		return event.Info, true

	case strings.Contains(msg, "COMMAND="):
		return event.Info, true

	case strings.Contains(lower, "session opened for user root"):
		return event.Warn, true // root session is worth noting

	case strings.Contains(lower, "session opened for user"):
		return event.Info, true

	case strings.Contains(lower, "new user:"),
		strings.Contains(lower, "new group:"),
		strings.Contains(lower, "password changed for"):
		return event.Warn, true

	default:
		return event.Info, false // skip unrecognised lines
	}
}

// parseAuthTimestamp parses syslog format "May  6 18:30:00" (with optional double space).
func parseAuthTimestamp(s string, year int) (time.Time, error) {
	withYear := fmt.Sprintf("%d %s", year, s)
	t, err := time.ParseInLocation("2006 Jan _2 15:04:05", withYear, time.Local)
	if err != nil {
		return time.Time{}, err
	}
	// If parsed time is more than 1 day in the future, it's from the previous year
	if t.After(time.Now().Add(24 * time.Hour)) {
		t = t.AddDate(-1, 0, 0)
	}
	return t, nil
}
