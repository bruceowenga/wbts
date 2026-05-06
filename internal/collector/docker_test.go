package collector

import (
	"bufio"
	"os"
	"testing"
	"time"

	"github.com/bruceowenga/wbts/pkg/event"
)

func TestParseDockerEvent_OOM(t *testing.T) {
	line := `{"Type":"container","Action":"oom","Actor":{"ID":"890c1b29","Attributes":{"image":"my-app:latest","name":"app_web_1"}},"scope":"local","time":1746413233,"timeNano":1746413233000000000}`
	e, ok := parseDockerEvent([]byte(line))
	if !ok {
		t.Fatal("expected ok=true for OOM event")
	}
	if e.Level != event.Critical {
		t.Errorf("OOM level = %v, want Critical", e.Level)
	}
	if e.Category != event.Container {
		t.Errorf("OOM category = %v, want Container", e.Category)
	}
	assertContains(t, e.Summary, "app_web_1")
	assertContains(t, e.Summary, "my-app:latest")
	assertContains(t, e.Summary, "OOM")
}

func TestParseDockerEvent_DieNonZero(t *testing.T) {
	line := `{"Type":"container","Action":"die","Actor":{"ID":"890c1b29","Attributes":{"exitCode":"137","image":"my-app:latest","name":"app_web_1"}},"scope":"local","time":1746413233,"timeNano":1746413233100000000}`
	e, ok := parseDockerEvent([]byte(line))
	if !ok {
		t.Fatal("expected ok=true for die with non-zero exit")
	}
	if e.Level != event.Error {
		t.Errorf("die(137) level = %v, want Error", e.Level)
	}
	assertContains(t, e.Summary, "app_web_1")
	assertContains(t, e.Summary, "137")
}

func TestParseDockerEvent_DieCleanExit_NotEmitted(t *testing.T) {
	line := `{"Type":"container","Action":"die","Actor":{"ID":"def456","Attributes":{"exitCode":"0","image":"backup:latest","name":"backup-cron"}},"scope":"local","time":1746413250,"timeNano":1746413250000000000}`
	_, ok := parseDockerEvent([]byte(line))
	if ok {
		t.Error("clean exit (code 0) should not be emitted")
	}
}

func TestParseDockerEvent_Restart(t *testing.T) {
	line := `{"Type":"container","Action":"restart","Actor":{"ID":"890c1b29","Attributes":{"image":"my-app:latest","name":"app_web_1"}},"scope":"local","time":1746413236,"timeNano":1746413236000000000}`
	e, ok := parseDockerEvent([]byte(line))
	if !ok {
		t.Fatal("expected ok=true for restart")
	}
	if e.Level != event.Warn {
		t.Errorf("restart level = %v, want Warn", e.Level)
	}
	assertContains(t, e.Summary, "restarted")
}

func TestParseDockerEvent_Kill(t *testing.T) {
	line := `{"Type":"container","Action":"kill","Actor":{"ID":"aabbcc","Attributes":{"image":"redis:7","name":"cache","signal":"15"}},"scope":"local","time":1746413260,"timeNano":1746413260000000000}`
	e, ok := parseDockerEvent([]byte(line))
	if !ok {
		t.Fatal("expected ok=true for kill")
	}
	if e.Level != event.Warn {
		t.Errorf("kill level = %v, want Warn", e.Level)
	}
	assertContains(t, e.Summary, "cache")
	assertContains(t, e.Summary, "15") // signal number
}

func TestParseDockerEvent_HealthUnhealthy(t *testing.T) {
	line := `{"Type":"container","Action":"health_status: unhealthy","Actor":{"ID":"ghi789","Attributes":{"image":"postgres:15","name":"db"}},"scope":"local","time":1746413270,"timeNano":1746413270000000000}`
	e, ok := parseDockerEvent([]byte(line))
	if !ok {
		t.Fatal("expected ok=true for unhealthy health status")
	}
	if e.Level != event.Error {
		t.Errorf("health_status:unhealthy level = %v, want Error", e.Level)
	}
	assertContains(t, e.Summary, "db")
	assertContains(t, e.Summary, "health")
}

func TestParseDockerEvent_HealthHealthy(t *testing.T) {
	line := `{"Type":"container","Action":"health_status: healthy","Actor":{"ID":"ghi789","Attributes":{"image":"postgres:15","name":"db"}},"scope":"local","time":1746413280,"timeNano":1746413280000000000}`
	e, ok := parseDockerEvent([]byte(line))
	if !ok {
		t.Fatal("expected ok=true for healthy health status")
	}
	if e.Level != event.Info {
		t.Errorf("health_status:healthy level = %v, want Info", e.Level)
	}
	assertContains(t, e.Summary, "recovered")
}

func TestParseDockerEvent_ImagePull(t *testing.T) {
	line := `{"Type":"image","Action":"pull","Actor":{"ID":"nginx:1.25","Attributes":{"name":"nginx:1.25"}},"scope":"local","time":1746413290,"timeNano":1746413290000000000}`
	e, ok := parseDockerEvent([]byte(line))
	if !ok {
		t.Fatal("expected ok=true for image pull")
	}
	if e.Level != event.Info {
		t.Errorf("image pull level = %v, want Info", e.Level)
	}
	assertContains(t, e.Summary, "nginx:1.25")
}

func TestParseDockerEvent_NetworkEvent_Ignored(t *testing.T) {
	line := `{"Type":"network","Action":"connect","Actor":{"ID":"net123","Attributes":{"container":"app_web_1","name":"bridge"}},"scope":"local","time":1746413295,"timeNano":1746413295000000000}`
	_, ok := parseDockerEvent([]byte(line))
	if ok {
		t.Error("network events should not be emitted")
	}
}

func TestParseDockerEvent_Timestamp(t *testing.T) {
	line := `{"Type":"container","Action":"oom","Actor":{"ID":"abc","Attributes":{"image":"app:1","name":"myapp"}},"scope":"local","time":1746413233,"timeNano":1746413233500000000}`
	e, ok := parseDockerEvent([]byte(line))
	if !ok {
		t.Fatal("expected ok=true")
	}
	want := time.Unix(1746413233, 500000000).UTC()
	if !e.Timestamp.Equal(want) {
		t.Errorf("timestamp = %v, want %v", e.Timestamp, want)
	}
}

func TestParseDockerEvent_MissingContainerName_UsesShortID(t *testing.T) {
	line := `{"Type":"container","Action":"die","Actor":{"ID":"890c1b2952f0d6965867","Attributes":{"exitCode":"1","image":"unknown:latest"}},"scope":"local","time":1746413233,"timeNano":1746413233000000000}`
	e, ok := parseDockerEvent([]byte(line))
	if !ok {
		t.Fatal("expected ok=true")
	}
	// Should fall back to short ID (first 12 chars)
	assertContains(t, e.Summary, "890c1b2952f0")
}

func TestParseDockerEvent_InvalidJSON(t *testing.T) {
	_, ok := parseDockerEvent([]byte(`not json`))
	if ok {
		t.Error("invalid JSON should return ok=false")
	}
}

func TestDockerEventsFromFixture(t *testing.T) {
	f, err := os.Open("../../testdata/docker/events.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	var events []event.Event
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		e, ok := parseDockerEvent(scanner.Bytes())
		if ok {
			events = append(events, e)
		}
	}

	// die(exitCode=0) and network events should be filtered — fixture has 11 lines,
	// 2 should be dropped: clean exit and network connect
	if len(events) != 9 {
		t.Errorf("got %d events from fixture, want 9 (clean exit + network dropped)", len(events))
	}

	// Verify level distribution
	levels := map[event.Level]int{}
	for _, e := range events {
		levels[e.Level]++
	}
	if levels[event.Critical] != 1 { // OOM
		t.Errorf("Critical count = %d, want 1", levels[event.Critical])
	}
	if levels[event.Error] != 3 { // die(137), die(1), health:unhealthy
		t.Errorf("Error count = %d, want 3", levels[event.Error])
	}
	if levels[event.Warn] != 2 { // restart, kill
		t.Errorf("Warn count = %d, want 2", levels[event.Warn])
	}
	if levels[event.Info] != 3 { // start, health:healthy, image pull
		t.Errorf("Info count = %d, want 3", levels[event.Info])
	}

	// All emitted events should have source="docker" and category=Container
	for _, e := range events {
		if e.Source != "docker" {
			t.Errorf("event source = %q, want docker", e.Source)
		}
		if e.Category != event.Container {
			t.Errorf("event category = %v, want Container", e.Category)
		}
	}
}

func assertContains(t *testing.T, s, substr string) {
	t.Helper()
	if !contains(s, substr) {
		t.Errorf("%q does not contain %q", s, substr)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(substr); i++ {
				if s[i:i+len(substr)] == substr {
					return true
				}
			}
			return false
		}())
}
