package timeline

import (
	"context"
	"testing"
	"time"

	"github.com/bruceowenga/wbts/pkg/event"
)

func makeEvent(t time.Time, level event.Level, cat event.Category, summary string) event.Event {
	return event.Event{Timestamp: t, Level: level, Category: cat, Summary: summary, Source: "test"}
}

func TestDetectIncidentWindows_SingleCluster(t *testing.T) {
	base := time.Now()
	tl := &Timeline{
		Events: []event.Event{
			makeEvent(base, event.Error, event.Kernel, "oom kill"),
			makeEvent(base.Add(5*time.Second), event.Error, event.Container, "container died"),
			makeEvent(base.Add(10*time.Second), event.Error, event.Service, "service failed"),
		},
	}
	tl.detectIncidentWindows()

	if len(tl.IncidentWindows) != 1 {
		t.Fatalf("got %d incident windows, want 1", len(tl.IncidentWindows))
	}
	iw := tl.IncidentWindows[0]
	if iw.EventCount != 3 {
		t.Errorf("EventCount = %d, want 3", iw.EventCount)
	}
	if iw.FirstFaultIndex != 0 {
		t.Errorf("FirstFaultIndex = %d, want 0", iw.FirstFaultIndex)
	}
	if len(iw.Categories) != 3 {
		t.Errorf("Categories = %v, want 3 entries", iw.Categories)
	}
}

func TestDetectIncidentWindows_BelowThreshold(t *testing.T) {
	base := time.Now()
	tl := &Timeline{
		Events: []event.Event{
			makeEvent(base, event.Error, event.Kernel, "oom kill"),
			makeEvent(base.Add(5*time.Second), event.Error, event.Container, "container died"),
			// Only 2 errors — below the threshold of 3
		},
	}
	tl.detectIncidentWindows()

	if len(tl.IncidentWindows) != 0 {
		t.Errorf("got %d windows, want 0 (below threshold)", len(tl.IncidentWindows))
	}
}

func TestDetectIncidentWindows_TwoSeparateClusters(t *testing.T) {
	base := time.Now()
	tl := &Timeline{
		Events: []event.Event{
			// Cluster 1
			makeEvent(base, event.Error, event.Kernel, "oom kill 1"),
			makeEvent(base.Add(5*time.Second), event.Error, event.Container, "container died 1"),
			makeEvent(base.Add(10*time.Second), event.Error, event.Service, "service failed 1"),
			// Gap of 5 minutes
			makeEvent(base.Add(5*time.Minute), event.Info, event.Service, "routine start"),
			// Cluster 2
			makeEvent(base.Add(6*time.Minute), event.Critical, event.Kernel, "disk error"),
			makeEvent(base.Add(6*time.Minute+5*time.Second), event.Error, event.Disk, "io error"),
			makeEvent(base.Add(6*time.Minute+10*time.Second), event.Error, event.Service, "db failed"),
		},
	}
	tl.detectIncidentWindows()

	if len(tl.IncidentWindows) != 2 {
		t.Fatalf("got %d windows, want 2", len(tl.IncidentWindows))
	}
}

func TestDetectIncidentWindows_InfoEventsDoNotCount(t *testing.T) {
	base := time.Now()
	tl := &Timeline{
		Events: []event.Event{
			makeEvent(base, event.Error, event.Kernel, "error 1"),
			makeEvent(base.Add(5*time.Second), event.Info, event.Service, "some info"),
			makeEvent(base.Add(10*time.Second), event.Info, event.Service, "more info"),
			// Only 1 actual error — info events don't count toward threshold
		},
	}
	tl.detectIncidentWindows()

	if len(tl.IncidentWindows) != 0 {
		t.Errorf("got %d windows, want 0 (info events should not count)", len(tl.IncidentWindows))
	}
}

func TestIsNoise(t *testing.T) {
	cases := []struct{ summary string; want bool }{
		{"Started Daily apt activities", true},
		{"systemd-timesyncd: Synchronized", true},
		{"nginx.service: Failed", false},
		{"Reached target Multi-User System", true},
		{"kernel: Out of memory", false},
		{"logrotate: rotating log files", true},
	}
	for _, c := range cases {
		got := isNoise(c.summary)
		if got != c.want {
			t.Errorf("isNoise(%q) = %v, want %v", c.summary, got, c.want)
		}
	}
}

// fakeCollector is a test double that emits a fixed set of events.
type fakeCollector struct {
	name   string
	events []event.Event
	avail  error
}

func (f *fakeCollector) Name() string { return f.name }
func (f *fakeCollector) Available() error { return f.avail }
func (f *fakeCollector) Collect(_ context.Context, _ event.Options) (<-chan event.Event, error) {
	ch := make(chan event.Event, len(f.events))
	for _, e := range f.events {
		ch <- e
	}
	close(ch)
	return ch, nil
}

func TestBuild_MergesAndSortsByTimestamp(t *testing.T) {
	base := time.Now()
	c1 := &fakeCollector{name: "first", events: []event.Event{
		makeEvent(base.Add(10*time.Second), event.Error, event.Service, "later"),
		makeEvent(base, event.Error, event.Kernel, "earlier"),
	}}
	c2 := &fakeCollector{name: "second", events: []event.Event{
		makeEvent(base.Add(5*time.Second), event.Error, event.Container, "middle"),
	}}

	opts := event.Options{Since: base.Add(-1 * time.Minute), Until: base.Add(1 * time.Minute)}
	tl, err := Build(context.Background(), []event.Collector{c1, c2}, opts)
	if err != nil {
		t.Fatal(err)
	}

	if len(tl.Events) != 3 {
		t.Fatalf("got %d events, want 3", len(tl.Events))
	}
	for i := 1; i < len(tl.Events); i++ {
		if tl.Events[i].Timestamp.Before(tl.Events[i-1].Timestamp) {
			t.Errorf("events not sorted: events[%d] (%v) before events[%d] (%v)",
				i, tl.Events[i].Timestamp, i-1, tl.Events[i-1].Timestamp)
		}
	}
}

func TestBuild_UnavailableCollectorIsSkipped(t *testing.T) {
	base := time.Now()
	available := &fakeCollector{name: "good", events: []event.Event{
		makeEvent(base, event.Error, event.Kernel, "error"),
	}}
	unavailable := &fakeCollector{
		name:  "bad",
		avail: errUnavailable("no permission"),
	}

	opts := event.Options{Since: base.Add(-1 * time.Minute), Until: base.Add(1 * time.Minute)}
	tl, err := Build(context.Background(), []event.Collector{available, unavailable}, opts)
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := tl.SkippedSources["bad"]; !ok {
		t.Error("expected 'bad' collector in SkippedSources")
	}
	if len(tl.Events) != 1 {
		t.Errorf("got %d events, want 1", len(tl.Events))
	}
}

type errUnavailable string

func (e errUnavailable) Error() string { return string(e) }
