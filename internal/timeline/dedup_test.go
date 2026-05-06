package timeline

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/bruceowenga/wbts/pkg/event"
)

func TestDeduplicate_CollapsesRepeatedEvents(t *testing.T) {
	base := time.Now()
	events := []event.Event{
		makeEvent(base, event.Error, event.Service, "cloudflared.service: 2026-05-06T18:08:43Z ERR failed to run the datagram handler"),
		makeEvent(base.Add(30*time.Second), event.Error, event.Service, "cloudflared.service: 2026-05-06T18:08:73Z ERR failed to run the datagram handler"),
		makeEvent(base.Add(60*time.Second), event.Error, event.Service, "cloudflared.service: 2026-05-06T18:09:43Z ERR failed to run the datagram handler"),
	}

	result := deduplicate(events)

	if len(result) != 1 {
		t.Fatalf("got %d events, want 1 (duplicates should be collapsed)", len(result))
	}
	if !strings.Contains(result[0].Summary, "[×3]") {
		t.Errorf("expected [×3] annotation, got: %s", result[0].Summary)
	}
}

func TestDeduplicate_PreservesFirstOccurrence(t *testing.T) {
	base := time.Now()
	events := []event.Event{
		makeEvent(base, event.Error, event.Service, "svc: 2026-01-01T01:00:00Z ERR connection refused"),
		makeEvent(base.Add(10*time.Second), event.Error, event.Service, "svc: 2026-01-01T01:00:10Z ERR connection refused"),
	}

	result := deduplicate(events)

	if len(result) != 1 {
		t.Fatalf("got %d events, want 1", len(result))
	}
	if !result[0].Timestamp.Equal(base) {
		t.Error("expected first occurrence to be kept")
	}
}

func TestDeduplicate_NewWindowAfterExpiry(t *testing.T) {
	base := time.Now()
	// Second event is beyond the dedup window (>5 minutes later)
	events := []event.Event{
		makeEvent(base, event.Error, event.Service, "svc: 2026-01-01T01:00:00Z ERR connection refused"),
		makeEvent(base.Add(6*time.Minute), event.Error, event.Service, "svc: 2026-01-01T01:06:00Z ERR connection refused"),
	}

	result := deduplicate(events)

	// Each should start a new window — both kept
	if len(result) != 2 {
		t.Fatalf("got %d events, want 2 (separate windows)", len(result))
	}
}

func TestDeduplicate_DifferentFingerprintsKeptSeparate(t *testing.T) {
	base := time.Now()
	events := []event.Event{
		makeEvent(base, event.Error, event.Service, "cloudflared.service: 2026-05-06T18:00:00Z ERR datagram handler failed"),
		makeEvent(base.Add(5*time.Second), event.Error, event.Service, "docker.service: time=\"...\" level=error msg=\"DNS failed\""),
		makeEvent(base.Add(10*time.Second), event.Error, event.Service, "cloudflared.service: 2026-05-06T18:00:10Z ERR datagram handler failed"),
	}

	result := deduplicate(events)

	// cloudflared collapses to 1, docker stays separate = 2 total
	if len(result) != 2 {
		t.Fatalf("got %d events, want 2", len(result))
	}
}

func TestDeduplicate_DifferentLevelsSeparateFingerprints(t *testing.T) {
	base := time.Now()
	events := []event.Event{
		makeEvent(base, event.Error, event.Service, "svc: same message"),
		makeEvent(base.Add(5*time.Second), event.Warn, event.Service, "svc: same message"),
	}

	result := deduplicate(events)

	if len(result) != 2 {
		t.Errorf("got %d events, want 2 — different levels should not be grouped", len(result))
	}
}

func TestDeduplicate_EmptyInput(t *testing.T) {
	result := deduplicate(nil)
	if result != nil && len(result) != 0 {
		t.Errorf("expected empty result for nil input")
	}
}

func TestDeduplicate_SingleEvent(t *testing.T) {
	events := []event.Event{makeEvent(time.Now(), event.Error, event.Service, "svc: ERR something")}
	result := deduplicate(events)
	if len(result) != 1 {
		t.Fatalf("got %d events, want 1", len(result))
	}
	if strings.Contains(result[0].Summary, "[×") {
		t.Error("single event should not get count annotation")
	}
}

func TestFingerprint_StripsDifferentTimestampFormats(t *testing.T) {
	base := time.Now()

	// RFC3339: same message, different embedded timestamps → same fingerprint
	e1 := makeEvent(base, event.Error, event.Service, "svc: 2026-05-06T18:08:43Z ERR something bad happened")
	e2 := makeEvent(base.Add(30*time.Second), event.Error, event.Service, "svc: 2026-05-06T18:09:13Z ERR something bad happened")
	if fingerprint(e1) != fingerprint(e2) {
		t.Errorf("RFC3339 timestamps not stripped: %q vs %q", fingerprint(e1), fingerprint(e2))
	}

	// GIN format: same endpoint 500 with different timestamps → same fingerprint
	eGIN1 := makeEvent(base, event.Error, event.Service, "svc: 2026/05/06 - 18:08:43 | 500 | 2m0s | 127.0.0.1 | POST /api/generate")
	eGIN2 := makeEvent(base.Add(30*time.Second), event.Error, event.Service, "svc: 2026/05/06 - 18:09:13 | 500 | 2m0s | 127.0.0.1 | POST /api/generate")
	if fingerprint(eGIN1) != fingerprint(eGIN2) {
		t.Errorf("GIN timestamps not stripped: %q vs %q", fingerprint(eGIN1), fingerprint(eGIN2))
	}

	// Different errors → different fingerprints even with same timestamp
	eDiff := makeEvent(base, event.Error, event.Service, "svc: 2026-05-06T18:08:43Z ERR totally different error")
	if fingerprint(e1) == fingerprint(eDiff) {
		t.Error("different messages should produce different fingerprints")
	}
}

func TestDeduplicate_RealCloudflaredScenario(t *testing.T) {
	// Simulate cloudflared reconnecting every ~30 seconds for 10 minutes
	base := time.Now()
	var events []event.Event
	for i := 0; i < 20; i++ {
		ts := base.Add(time.Duration(i*30) * time.Second)
		ts2 := fmt.Sprintf("2026-05-06T18:%02d:%02dZ", 8, i*30%60)
		events = append(events,
			makeEvent(ts, event.Error, event.Service,
				"cloudflared.service: "+ts2+" ERR failed to run the datagram handler error=\"context canceled\" connIndex=0"),
			makeEvent(ts.Add(time.Second), event.Warn, event.Service,
				"cloudflared.service: "+ts2+" WRN failed to serve tunnel connection error=\"control stream\""),
		)
	}

	result := deduplicate(events)

	// 20 cycles × 2 event types = 40 events
	// With 5-min dedup windows: 20 cycles span 10 minutes = 2 complete windows per type
	// ERR: 2 representative events; WRN: 2 representative events = 4 total
	// (allowing ±1 for boundary effects)
	if len(result) > 8 {
		t.Errorf("too many events after dedup: %d (expected ≤8 for 20 reconnect cycles)", len(result))
	}
}
