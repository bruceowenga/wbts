package collector

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/bruceowenga/wbts/pkg/event"
)

// k8s/k3s log lines begin with a severity letter + MMDD timestamp, e.g. "E0506 18:13:42".
// E/F = Error/Fatal, W = Warning. I = Info (not elevated).
var (
	k8sErrorRe = regexp.MustCompile(`\b[EF]\d{4} \d{2}:\d{2}:\d{2}`)
	k8sWarnRe  = regexp.MustCompile(`\bW\d{4} \d{2}:\d{2}:\d{2}`)
)

// JournaldCollector reads from systemd's journal via journalctl.
type JournaldCollector struct{}

func NewJournaldCollector() *JournaldCollector { return &JournaldCollector{} }

func (j *JournaldCollector) Name() string { return "journald" }

func (j *JournaldCollector) Available() error {
	if _, err := exec.LookPath("journalctl"); err != nil {
		return fmt.Errorf("journalctl not found in PATH")
	}
	// Quick probe — exits 0 even with no results
	if err := exec.Command("journalctl", "--lines", "0").Run(); err != nil {
		return fmt.Errorf("journalctl not accessible: %w", err)
	}
	return nil
}

func (j *JournaldCollector) Collect(ctx context.Context, opts event.Options) (<-chan event.Event, error) {
	args := []string{
		"--output", "json",
		"--no-pager",
		"--priority", "6", // info (6) and more urgent; excludes debug (7)
		"--since", opts.Since.Local().Format("2006-01-02 15:04:05"),
		"--until", opts.Until.Local().Format("2006-01-02 15:04:05"),
	}

	cmd := exec.CommandContext(ctx, "journalctl", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("journald: stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("journald: start: %w", err)
	}

	ch := make(chan event.Event, 256)
	go func() {
		defer close(ch)
		defer cmd.Wait() //nolint:errcheck — exit code is not meaningful for log readers

		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
		for scanner.Scan() {
			e, ok := parseJournaldEntry(scanner.Bytes())
			if !ok {
				continue
			}
			if !matchesFilter(e, opts.Filter) {
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

func matchesFilter(e event.Event, f event.Filter) bool {
	if f.Container != "" && !strings.Contains(e.Summary, f.Container) {
		return false
	}
	if f.Service != "" && !strings.Contains(e.Summary, f.Service) {
		return false
	}
	return true
}

// journaldEntry mirrors the fields we care about in journalctl --output json.
type journaldEntry struct {
	RealtimeTimestamp string          `json:"__REALTIME_TIMESTAMP"`
	Message           json.RawMessage `json:"MESSAGE"`
	Priority          string          `json:"PRIORITY"`
	SyslogIdentifier  string          `json:"SYSLOG_IDENTIFIER"`
	SystemdUnit       string          `json:"_SYSTEMD_UNIT"`
	Transport         string          `json:"_TRANSPORT"`
	Unit              string          `json:"UNIT"`
}

func parseJournaldEntry(data []byte) (event.Event, bool) {
	var entry journaldEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return event.Event{}, false
	}

	ts, err := parseJournaldTimestamp(entry.RealtimeTimestamp)
	if err != nil {
		return event.Event{}, false
	}

	msg := extractMessage(entry.Message)
	if msg == "" {
		return event.Event{}, false
	}

	unit := entry.SystemdUnit
	if unit == "" {
		unit = entry.Unit
	}

	// Use journald priority as the baseline, but elevate if the service embeds
	// a higher severity in the message body (e.g. cloudflared, Docker, Logrus).
	lvl := journaldPriorityToLevel(entry.Priority)
	if embedded := extractEmbeddedLevel(msg); embedded > lvl {
		lvl = embedded
	}

	return event.Event{
		Timestamp: ts,
		Source:    "journald",
		Level:     lvl,
		Category:  journaldCategory(entry.Transport, entry.SyslogIdentifier, unit),
		Summary:   buildSummary(unit, msg),
		Raw:       string(data),
	}, true
}

// extractEmbeddedLevel detects log severity markers that services write into the message
// body when they route all output to journald at a single priority level.
// Only elevates — never demotes below what journald reported.
func extractEmbeddedLevel(msg string) event.Level {
	// Alertmanager forwarded alerts carry the severity in human-readable form
	if strings.Contains(msg, "Forwarded alert: CRITICAL") {
		return event.Critical
	}
	if strings.Contains(msg, "Forwarded alert: WARNING") || strings.Contains(msg, "Forwarded alert: WARN") {
		return event.Warn
	}

	// Structured logging: level=error / level=warn (Logrus, Zap, Zerolog, Docker daemon)
	lower := strings.ToLower(msg)
	if strings.Contains(lower, "level=error") || strings.Contains(lower, `"level":"error"`) {
		return event.Error
	}
	if strings.Contains(lower, "level=warn") || strings.Contains(lower, `"level":"warn"`) {
		return event.Warn
	}

	// Cloudflared / HashiCorp Vault style: timestamp + space + "ERR"/"WRN" + space
	if strings.Contains(msg, " ERR ") {
		return event.Error
	}
	if strings.Contains(msg, " WRN ") {
		return event.Warn
	}

	// Kubernetes / k3s log format: E0506 18:13:42 (error), W0506 (warning), F = fatal
	if k8sErrorRe.MatchString(msg) {
		return event.Error
	}
	if k8sWarnRe.MatchString(msg) {
		return event.Warn
	}

	// GIN HTTP access log: elevate 5xx server errors (4xx are often normal client errors)
	if strings.Contains(msg, "[GIN]") && strings.Contains(msg, "| 5") {
		return event.Error
	}

	// Docker container restart — logged at INFO by Docker but warrants attention
	if strings.Contains(lower, `msg="restarting container"`) || strings.Contains(lower, "msg=restarting container") {
		return event.Warn
	}

	return event.Info
}

func parseJournaldTimestamp(s string) (time.Time, error) {
	us, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid timestamp %q: %w", s, err)
	}
	return time.Unix(us/1_000_000, (us%1_000_000)*1000).UTC(), nil
}

// extractMessage handles journald's two MESSAGE formats: JSON string or byte array.
func extractMessage(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var nums []int
	if err := json.Unmarshal(raw, &nums); err == nil {
		b := make([]byte, len(nums))
		for i, v := range nums {
			b[i] = byte(v)
		}
		return string(b)
	}
	return ""
}

func journaldPriorityToLevel(p string) event.Level {
	switch p {
	case "0", "1", "2":
		return event.Critical
	case "3":
		return event.Error
	case "4":
		return event.Warn
	default:
		return event.Info
	}
}

func journaldCategory(transport, syslogID, unit string) event.Category {
	if transport == "kernel" || syslogID == "kernel" {
		return event.Kernel
	}
	if strings.HasSuffix(unit, ".timer") {
		return event.Cron
	}
	if strings.HasSuffix(unit, ".service") || syslogID == "systemd" {
		return event.Service
	}
	return event.Service
}

func buildSummary(unit, msg string) string {
	if unit != "" && !strings.Contains(msg, unit) {
		return truncate(unit+": "+msg, 120)
	}
	return truncate(msg, 120)
}
