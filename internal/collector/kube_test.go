package collector

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bruceowenga/wbts/pkg/event"
)

func TestKubeEventLevel(t *testing.T) {
	cases := []struct {
		eventType string
		reason    string
		want      event.Level
	}{
		{"Warning", "OOMKilling", event.Critical},
		{"Warning", "BackOff", event.Error},
		{"Warning", "CrashLoopBackOff", event.Error},
		{"Warning", "Unhealthy", event.Error},
		{"Warning", "Failed", event.Error},
		{"Warning", "NodeNotReady", event.Error},
		{"Warning", "FailedScheduling", event.Error},
		{"Warning", "SomeOtherWarning", event.Warn},
		{"Normal", "Killing", event.Warn},
		{"Normal", "Evicted", event.Warn},
		{"Normal", "Started", event.Info},
		{"Normal", "Pulled", event.Info},
	}
	for _, c := range cases {
		got := kubeEventLevel(c.eventType, c.reason)
		if got != c.want {
			t.Errorf("kubeEventLevel(%q, %q) = %v, want %v", c.eventType, c.reason, got, c.want)
		}
	}
}

func TestKubeEventSummary(t *testing.T) {
	cases := []struct {
		kind, namespace, name, reason, message string
		count                                  int32
		wantContains                           []string
	}{
		{
			kind: "Pod", namespace: "default", name: "nginx-abc",
			reason: "OOMKilling", message: "Container nginx was OOM killed", count: 3,
			wantContains: []string{"Pod", "nginx-abc", "default", "OOMKilling", "[×3]"},
		},
		{
			kind: "Node", namespace: "", name: "odin",
			reason: "NodeNotReady", message: "Node odin status is now: NodeNotReady", count: 1,
			wantContains: []string{"Node", "odin", "NodeNotReady"},
		},
		{
			kind: "Pod", namespace: "kube-system", name: "coredns-abc",
			reason: "BackOff", message: "Back-off restarting failed", count: 1,
			wantContains: []string{"kube-system", "BackOff"},
		},
	}
	for _, c := range cases {
		got := buildKubeSummary(c.kind, c.namespace, c.name, c.reason, c.message, c.count)
		for _, want := range c.wantContains {
			if !strings.Contains(got, want) {
				t.Errorf("buildKubeSummary(...) = %q, missing %q", got, want)
			}
		}
	}
}

func TestParseKubeEventsFromFixture(t *testing.T) {
	data, err := os.ReadFile("../../testdata/kube/events.json")
	if err != nil {
		t.Fatal(err)
	}

	since := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	until := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)

	events, err := parseKubeEvents(data, since, until)
	if err != nil {
		t.Fatal(err)
	}

	// Fixture has 6 events. Started (Normal/routine) should be filtered.
	// Killing (Normal but in include list) should be kept.
	// Expect: OOMKilling(CRIT), BackOff(ERR), Killing(WARN), NodeNotReady(ERR), Unhealthy(ERR) = 5
	if len(events) != 5 {
		t.Errorf("got %d events, want 5 (Started filtered out)", len(events))
		for _, e := range events {
			t.Logf("  %v %s %s", e.Level, e.Source, e.Summary)
		}
	}

	// All events should have source="kubernetes" and category=Container or Kernel
	for _, e := range events {
		if e.Source != "kubernetes" {
			t.Errorf("source = %q, want kubernetes", e.Source)
		}
	}

	// OOMKilling should be Critical
	var foundOOM bool
	for _, e := range events {
		if strings.Contains(e.Summary, "OOMKilling") {
			foundOOM = true
			if e.Level != event.Critical {
				t.Errorf("OOMKilling level = %v, want Critical", e.Level)
			}
			if !strings.Contains(e.Summary, "[×3]") {
				t.Errorf("expected count annotation [×3], got: %s", e.Summary)
			}
		}
	}
	if !foundOOM {
		t.Error("no OOMKilling event found")
	}
}

func TestParseKubeEvents_TimeRangeFilter(t *testing.T) {
	data, err := os.ReadFile("../../testdata/kube/events.json")
	if err != nil {
		t.Fatal(err)
	}

	// Only events from 18:00 onwards (excludes Killing at 17:00, NodeNotReady at 17:13, Unhealthy at 17:20)
	since := time.Date(2026, 5, 6, 18, 0, 0, 0, time.UTC)
	until := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)

	events, err := parseKubeEvents(data, since, until)
	if err != nil {
		t.Fatal(err)
	}

	// Should only get: OOMKilling (18:13), BackOff (18:14) = 2
	if len(events) != 2 {
		t.Errorf("got %d events, want 2 (after 18:00)", len(events))
	}
}

func TestParseKubeEvents_InvalidJSON(t *testing.T) {
	_, err := parseKubeEvents([]byte("not json"), time.Time{}, time.Now())
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}
