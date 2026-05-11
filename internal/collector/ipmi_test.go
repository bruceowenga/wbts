package collector

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bruceowenga/wbts/pkg/event"
)

func TestParseIPMILine(t *testing.T) {
	year := 2026
	cases := []struct {
		line      string
		wantOk    bool
		wantLevel event.Level
		wantParts []string // substrings expected in summary
	}{
		{
			line:      "   1 | 05/06/2026 | 17:13:00 | Temperature #0x30 | Lower Non-critical going low  | Asserted",
			wantOk:    true,
			wantLevel: event.Warn,
			wantParts: []string{"Temperature", "Non-critical"},
		},
		{
			line:      "   2 | 05/06/2026 | 17:14:00 | Fan #0x71         | Lower Critical going low      | Asserted",
			wantOk:    true,
			wantLevel: event.Error,
			wantParts: []string{"Fan", "Critical"},
		},
		{
			line:      "   4 | 05/06/2026 | 17:21:00 | Memory #0x10      | Uncorrectable ECC             | Asserted",
			wantOk:    true,
			wantLevel: event.Critical,
			wantParts: []string{"Memory", "Uncorrectable"},
		},
		{
			line:      "   5 | 05/06/2026 | 17:30:00 | Power Supply #0x02| AC Lost                       | Asserted",
			wantOk:    true,
			wantLevel: event.Error,
			wantParts: []string{"Power Supply", "AC Lost"},
		},
		{
			line:      "   6 | 05/06/2026 | 17:35:00 | Power Supply #0x02| Fully Redundant               | Deasserted",
			wantOk:    true,
			wantLevel: event.Info,
			wantParts: []string{"Power Supply"},
		},
		{
			line:   "not a valid sel line",
			wantOk: false,
		},
		{
			line:   "",
			wantOk: false,
		},
	}

	for _, c := range cases {
		e, ok := parseIPMILine(c.line, year)
		if ok != c.wantOk {
			t.Errorf("parseIPMILine(%q) ok=%v, want %v", c.line, ok, c.wantOk)
			continue
		}
		if !c.wantOk {
			continue
		}
		if e.Level != c.wantLevel {
			t.Errorf("parseIPMILine(%q) level=%v, want %v", c.line, e.Level, c.wantLevel)
		}
		for _, part := range c.wantParts {
			if !strings.Contains(e.Summary, part) {
				t.Errorf("summary %q missing %q", e.Summary, part)
			}
		}
		if e.Source != "ipmi" {
			t.Errorf("source=%q, want ipmi", e.Source)
		}
		if e.Category != event.Kernel {
			t.Errorf("category=%v, want Kernel", e.Category)
		}
	}
}

func TestIPMITimestamp(t *testing.T) {
	e, ok := parseIPMILine("   1 | 05/06/2026 | 17:13:00 | Temperature #0x30 | Lower Non-critical | Asserted", 2026)
	if !ok {
		t.Fatal("expected ok=true")
	}
	want := time.Date(2026, 5, 6, 17, 13, 0, 0, time.UTC)
	if !e.Timestamp.Equal(want) {
		t.Errorf("timestamp=%v, want %v", e.Timestamp, want)
	}
}

func TestIPMIEventLevel(t *testing.T) {
	cases := []struct {
		name     string
		evtType  string
		state    string
		want     event.Level
	}{
		{"non-recoverable", "Upper Non-recoverable going high", "Asserted", event.Critical},
		{"uncorrectable ecc", "Uncorrectable ECC", "Asserted", event.Critical},
		{"critical", "Lower Critical going low", "Asserted", event.Error},
		{"ac lost", "AC Lost", "Asserted", event.Error},
		{"non-critical", "Lower Non-critical going low", "Asserted", event.Warn},
		{"fully redundant restored", "Fully Redundant", "Deasserted", event.Info},
		{"oem boot", "OEM System Boot Event", "Asserted", event.Info},
	}
	for _, c := range cases {
		got := ipmiEventLevel(c.evtType, c.state)
		if got != c.want {
			t.Errorf("ipmiEventLevel(%q, %q) = %v, want %v", c.evtType, c.state, got, c.want)
		}
	}
}

func TestParseIPMIFromFixture(t *testing.T) {
	f, err := os.Open("../../testdata/ipmi/sel.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	since := time.Date(2026, 5, 6, 0, 0, 0, 0, time.UTC)
	until := time.Date(2026, 5, 7, 0, 0, 0, 0, time.UTC)

	events, err := parseIPMIOutput(f, since, until, 2026)
	if err != nil {
		t.Fatal(err)
	}

	// Fixture has 7 lines; "Fully Redundant Deasserted" and "OEM Boot" are Info
	// All should be emitted (we emit Info level from IPMI too, for context)
	if len(events) == 0 {
		t.Fatal("no events parsed from fixture")
	}

	levels := map[event.Level]int{}
	for _, e := range events {
		levels[e.Level]++
	}
	if levels[event.Critical] < 1 {
		t.Error("expected at least one Critical event (Uncorrectable ECC)")
	}
	if levels[event.Error] < 1 {
		t.Error("expected at least one Error event (Critical fan, AC Lost)")
	}
	if levels[event.Warn] < 1 {
		t.Error("expected at least one Warn event (Non-critical temperature)")
	}
}
