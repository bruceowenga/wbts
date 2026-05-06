package collector

import (
	"bufio"
	"os"
	"testing"
	"time"

	"github.com/bruceowenga/wbts/pkg/event"
)

func TestParseJournaldTimestamp(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    time.Time
		wantErr bool
	}{
		{
			name:  "valid microsecond timestamp",
			input: "1746413233000000",
			want:  time.Unix(1746413233, 0).UTC(),
		},
		{
			name:  "timestamp with sub-second precision",
			input: "1746413233500000",
			want:  time.Unix(1746413233, 500000*1000).UTC(),
		},
		{
			name:    "empty string",
			input:   "",
			wantErr: true,
		},
		{
			name:    "non-numeric",
			input:   "not-a-timestamp",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseJournaldTimestamp(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseJournaldTimestamp(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if !tt.wantErr && !got.Equal(tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestExtractMessage(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "plain string message",
			input: `"hello world"`,
			want:  "hello world",
		},
		{
			name:  "binary message as byte array",
			input: `[115,115,104,100,58,32,101,114,114,111,114]`,
			want:  "sshd: error",
		},
		{
			name:  "empty",
			input: `""`,
			want:  "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractMessage([]byte(tt.input))
			if got != tt.want {
				t.Errorf("extractMessage(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestJournaldPriorityToLevel(t *testing.T) {
	cases := []struct{ p string; want event.Level }{
		{"0", event.Critical},
		{"1", event.Critical},
		{"2", event.Critical},
		{"3", event.Error},
		{"4", event.Warn},
		{"5", event.Info},
		{"6", event.Info},
		{"7", event.Info},
		{"",  event.Info},
	}
	for _, c := range cases {
		got := journaldPriorityToLevel(c.p)
		if got != c.want {
			t.Errorf("priority %q: got %v, want %v", c.p, got, c.want)
		}
	}
}

func TestExtractEmbeddedLevel(t *testing.T) {
	cases := []struct {
		msg  string
		want event.Level
	}{
		// Structured logging
		{`time="2026-05-06T18:02:51Z" level=error msg="failed to connect"`, event.Error},
		{`time="2026-05-06T18:02:51Z" level=warn msg="retry"`, event.Warn},
		{`{"level":"error","msg":"oops"}`, event.Error},
		{`{"level":"warn","msg":"slow"}`, event.Warn},
		// Cloudflared style
		{`2026-05-06T18:02:49Z ERR failed to run datagram handler`, event.Error},
		{`2026-05-06T18:02:49Z WRN failed to serve tunnel connection`, event.Warn},
		{`2026-05-06T18:02:49Z INF Retrying connection`, event.Info},
		// Kubernetes / k3s log format
		{`E0506 18:13:42.801842   53335 kubelet.go:2618] "Housekeeping took longer"`, event.Error},
		{`W0506 18:13:42.801842   53335 kubelet.go:2618] "Slow start"`, event.Warn},
		{`I0506 18:13:42.801842   53335 kubelet.go:2618] "Starting"`, event.Info},
		{`F0506 18:13:42.801842   53335 main.go:100] "fatal error"`, event.Error},
		// Alertmanager forwarded alerts
		{`Forwarded alert: CRITICAL: SystemLoadCritical`, event.Critical},
		{`Forwarded alert: WARNING: DiskSpaceLow`, event.Warn},
		// No embedded level
		{`ordinary log line with no embedded level`, event.Info},
	}
	for _, c := range cases {
		got := extractEmbeddedLevel(c.msg)
		if got != c.want {
			t.Errorf("extractEmbeddedLevel(%q) = %v, want %v", c.msg, got, c.want)
		}
	}
}

func TestParseJournaldEntryFromFixture(t *testing.T) {
	f, err := os.Open("../../testdata/journald/sample.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	var events []event.Event
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		e, ok := parseJournaldEntry(scanner.Bytes())
		if ok {
			events = append(events, e)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}

	if len(events) == 0 {
		t.Fatal("no events parsed from fixture")
	}

	// First event: nginx failure, should be Error level, Service category
	first := events[0]
	if first.Level != event.Error {
		t.Errorf("event[0] level = %v, want Error", first.Level)
	}
	if first.Category != event.Service {
		t.Errorf("event[0] category = %v, want Service", first.Category)
	}
	if first.Source != "journald" {
		t.Errorf("event[0] source = %q, want journald", first.Source)
	}

	// Find the OOM event (priority 2 → Critical, kernel transport)
	var foundOOM bool
	for _, e := range events {
		if e.Level == event.Critical {
			foundOOM = true
			if e.Category != event.Kernel {
				t.Errorf("OOM event category = %v, want Kernel", e.Category)
			}
		}
	}
	if !foundOOM {
		t.Error("no Critical-level event found in fixture")
	}

	// Binary message entry should be parsed without error
	var foundBinary bool
	for _, e := range events {
		if e.Source == "journald" && len(e.Summary) > 0 && e.Summary != "" {
			foundBinary = true
		}
	}
	if !foundBinary {
		t.Error("no events with non-empty summary found")
	}
}
