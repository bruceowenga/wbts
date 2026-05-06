package collector

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/bruceowenga/wbts/pkg/event"
)

// DmesgCollector reads kernel ring buffer messages via dmesg.
type DmesgCollector struct{}

func NewDmesgCollector() *DmesgCollector { return &DmesgCollector{} }

func (d *DmesgCollector) Name() string { return "dmesg" }

func (d *DmesgCollector) Available() error {
	if _, err := exec.LookPath("dmesg"); err != nil {
		return fmt.Errorf("dmesg not found in PATH")
	}
	return nil
}

func (d *DmesgCollector) Collect(ctx context.Context, opts event.Options) (<-chan event.Event, error) {
	bootTime, err := readBootTime()
	if err != nil {
		return nil, fmt.Errorf("dmesg: cannot determine boot time: %w", err)
	}

	// dmesg ring buffer is small (typically 512KB–4MB); reading it all at once is fine.
	cmd := exec.CommandContext(ctx, "dmesg", "--level", "err,warn,crit,alert,emerg")
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		// Surface the actual dmesg error (often "Operation not permitted").
		// Hint users toward the fix rather than leaving them with an exit code.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
			return nil, fmt.Errorf("dmesg: %s (try: sudo usermod -aG adm $USER)",
				strings.TrimSpace(string(exitErr.Stderr)))
		}
		return nil, fmt.Errorf("dmesg: %w", err)
	}

	ch := make(chan event.Event, 256)
	go func() {
		defer close(ch)
		scanner := bufio.NewScanner(bytes.NewReader(out))
		for scanner.Scan() {
			e, ok := parseDmesgLine(scanner.Text(), bootTime)
			if !ok {
				continue
			}
			if e.Timestamp.Before(opts.Since) || e.Timestamp.After(opts.Until) {
				continue
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

// readBootTime calculates system boot time from /proc/uptime.
// /proc/uptime format: "<uptime_seconds> <idle_seconds>"
func readBootTime() (time.Time, error) {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return time.Time{}, err
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return time.Time{}, fmt.Errorf("unexpected /proc/uptime content")
	}
	uptimeSecs, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse uptime %q: %w", fields[0], err)
	}
	return time.Now().Add(-time.Duration(uptimeSecs * float64(time.Second))), nil
}

// parseDmesgLine parses a single line in dmesg default format:
//
//	[SSSSS.UUUUUU] message text
func parseDmesgLine(line string, bootTime time.Time) (event.Event, bool) {
	if len(line) < 3 || line[0] != '[' {
		return event.Event{}, false
	}
	end := strings.Index(line, "]")
	if end < 0 {
		return event.Event{}, false
	}
	secStr := strings.TrimSpace(line[1:end])
	secs, err := strconv.ParseFloat(secStr, 64)
	if err != nil {
		return event.Event{}, false
	}
	msg := strings.TrimSpace(line[end+1:])
	if msg == "" {
		return event.Event{}, false
	}

	ts := bootTime.Add(time.Duration(secs * float64(time.Second)))
	cat, lvl := classifyDmesgMessage(msg)

	return event.Event{
		Timestamp: ts,
		Source:    "dmesg",
		Level:     lvl,
		Category:  cat,
		Summary:   truncate(msg, 120),
		Raw:       line,
	}, true
}

func classifyDmesgMessage(msg string) (event.Category, event.Level) {
	lower := strings.ToLower(msg)

	// Disk errors take category priority over level detection
	if strings.Contains(lower, "i/o error") ||
		strings.Contains(lower, "ext4-fs error") ||
		strings.Contains(lower, "buffer i/o error") ||
		strings.Contains(lower, "scsi error") {
		return event.Disk, event.Error
	}

	var lvl event.Level
	switch {
	case strings.Contains(lower, "panic") ||
		strings.Contains(lower, "oops") ||
		strings.Contains(lower, "bug:") ||
		strings.Contains(lower, "bug at"):
		lvl = event.Critical
	case strings.Contains(lower, "oom") ||
		strings.Contains(lower, "out of memory") ||
		strings.Contains(lower, "killed process") ||
		strings.Contains(lower, "error"):
		lvl = event.Error
	default:
		lvl = event.Warn
	}

	return event.Kernel, lvl
}
