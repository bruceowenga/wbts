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
	cases := []struct {
		summary string
		level   event.Level
		want    bool
	}{
		{"Started Daily apt activities", event.Info, true},
		{"systemd-timesyncd: Synchronized", event.Info, true},
		{"nginx.service: Failed", event.Info, false},
		{"Reached target Multi-User System", event.Info, true},
		{"kernel: Out of memory", event.Error, false},  // ERROR never suppressed
		{"kernel: Out of memory", event.Info, false},   // not in patterns
		{"logrotate: rotating log files", event.Info, true},
		{"[UFW BLOCK] IN=eno1 OUT= SRC=192.168.1.1", event.Warn, true},
		{"[UFW BLOCK] IN=eno1 OUT= SRC=192.168.1.1", event.Error, false}, // ERROR never suppressed
		{"GET /metrics HTTP/1.1", event.Info, true},
		{"dns: resolver: forward: no upstream resolvers set", event.Info, true},
	}
	for _, c := range cases {
		got := isNoise(c.level, c.summary)
		if got != c.want {
			t.Errorf("isNoise(%v, %q) = %v, want %v", c.level, c.summary, got, c.want)
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

func TestBuildStreaming_DeliversProgressiveUpdates(t *testing.T) {
	base := time.Now()

	// Two collectors: first finishes instantly, second has a small delay
	fast := &fakeCollector{name: "fast", events: []event.Event{
		makeEvent(base, event.Error, event.Kernel, "oom kill"),
		makeEvent(base.Add(5*time.Second), event.Error, event.Service, "service failed"),
		makeEvent(base.Add(10*time.Second), event.Error, event.Container, "container died"),
	}}
	slow := &fakeCollector{name: "slow", events: []event.Event{
		makeEvent(base.Add(time.Minute), event.Warn, event.Service, "slow event"),
	}}

	opts := event.Options{Since: base.Add(-time.Minute), Until: base.Add(2 * time.Minute)}
	ctx := context.Background()

	progressCh := BuildStreaming(ctx, []event.Collector{fast, slow}, opts)

	var updates []ProgressUpdate
	for u := range progressCh {
		updates = append(updates, u)
	}

	// Should receive one update per collector completing
	if len(updates) < 2 {
		t.Fatalf("expected at least 2 updates, got %d", len(updates))
	}

	// Final update must be marked Done
	last := updates[len(updates)-1]
	if !last.Done {
		t.Error("last update should have Done=true")
	}

	// Final timeline should include events from both collectors
	if len(last.Timeline.Events) == 0 {
		t.Error("final timeline should have events")
	}

	// CollectorStates should be populated
	if len(last.CollectorStates) == 0 {
		t.Error("CollectorStates should be non-empty")
	}
}

func TestBuildStreaming_SkippedCollectorReflectedInStates(t *testing.T) {
	base := time.Now()

	available := &fakeCollector{name: "good", events: []event.Event{
		makeEvent(base, event.Error, event.Kernel, "error"),
	}}
	unavailable := &fakeCollector{name: "bad", avail: errUnavailable("no permission")}

	opts := event.Options{Since: base.Add(-time.Minute), Until: base.Add(time.Minute)}
	progressCh := BuildStreaming(context.Background(), []event.Collector{available, unavailable}, opts)

	var last ProgressUpdate
	for u := range progressCh {
		last = u
	}

	if _, ok := last.Timeline.SkippedSources["bad"]; !ok {
		t.Error("unavailable collector should appear in SkippedSources")
	}

	var foundBad bool
	for _, s := range last.CollectorStates {
		if s.Name == "bad" && s.Error != nil {
			foundBad = true
		}
	}
	if !foundBad {
		t.Error("bad collector should appear in CollectorStates with an error")
	}
}

func TestBuildStreaming_EachUpdateHasMoreEvents(t *testing.T) {
	base := time.Now()

	c1 := &fakeCollector{name: "c1", events: []event.Event{
		makeEvent(base, event.Error, event.Service, "c1 error"),
		makeEvent(base.Add(time.Second), event.Error, event.Service, "c1 error 2"),
		makeEvent(base.Add(2*time.Second), event.Error, event.Service, "c1 error 3"),
	}}
	c2 := &fakeCollector{name: "c2", events: []event.Event{
		makeEvent(base.Add(time.Minute), event.Error, event.Kernel, "c2 error"),
		makeEvent(base.Add(time.Minute+time.Second), event.Error, event.Kernel, "c2 error 2"),
		makeEvent(base.Add(time.Minute+2*time.Second), event.Error, event.Kernel, "c2 error 3"),
	}}

	opts := event.Options{Since: base.Add(-time.Minute), Until: base.Add(2 * time.Minute)}
	progressCh := BuildStreaming(context.Background(), []event.Collector{c1, c2}, opts)

	var counts []int
	for u := range progressCh {
		counts = append(counts, len(u.Timeline.Events))
	}

	if len(counts) < 2 {
		t.Fatalf("expected at least 2 updates, got %d", len(counts))
	}
	// Final count should include all events from both collectors
	if counts[len(counts)-1] < 6 {
		t.Errorf("final event count = %d, want at least 6", counts[len(counts)-1])
	}
}
