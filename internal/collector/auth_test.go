package collector

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bruceowenga/wbts/pkg/event"
)

func TestParseAuthTimestamp(t *testing.T) {
	cases := []struct {
		input   string
		wantErr bool
		wantMon time.Month
		wantDay int
		wantHr  int
	}{
		{"May  6 18:30:00", false, time.May, 6, 18},
		{"Jan 16 03:00:00", false, time.January, 16, 3},
		{"Dec 31 23:59:59", false, time.December, 31, 23},
		{"not a date   ", true, 0, 0, 0},
	}
	for _, c := range cases {
		ts, err := parseAuthTimestamp(c.input, 2026)
		if (err != nil) != c.wantErr {
			t.Errorf("parseAuthTimestamp(%q) err=%v, wantErr=%v", c.input, err, c.wantErr)
			continue
		}
		if c.wantErr {
			continue
		}
		if ts.Month() != c.wantMon || ts.Day() != c.wantDay || ts.Hour() != c.wantHr {
			t.Errorf("parseAuthTimestamp(%q) = %v, want month=%v day=%d hour=%d",
				c.input, ts, c.wantMon, c.wantDay, c.wantHr)
		}
	}
}

func TestParseAuthLine_FailedPassword(t *testing.T) {
	line := "May  6 18:30:00 odin sshd[1234]: Failed password for bruce from 192.168.100.27 port 52341 ssh2"
	e, ok := parseAuthLine(line, 2026)
	if !ok {
		t.Fatal("expected ok=true for failed password")
	}
	if e.Level != event.Warn {
		t.Errorf("level = %v, want Warn", e.Level)
	}
	if e.Category != event.Auth {
		t.Errorf("category = %v, want Auth", e.Category)
	}
	if !strings.Contains(e.Summary, "bruce") {
		t.Errorf("summary missing user 'bruce': %s", e.Summary)
	}
	if e.Source != "auth" {
		t.Errorf("source = %q, want auth", e.Source)
	}
}

func TestParseAuthLine_AcceptedPublickey(t *testing.T) {
	line := "May  6 18:30:01 odin sshd[1234]: Accepted publickey for bruce from 192.168.100.27 port 52341 ssh2: ED25519 SHA256:abc123"
	e, ok := parseAuthLine(line, 2026)
	if !ok {
		t.Fatal("expected ok=true for accepted publickey")
	}
	if e.Level != event.Info {
		t.Errorf("level = %v, want Info", e.Level)
	}
	if !strings.Contains(e.Summary, "bruce") {
		t.Errorf("summary missing user: %s", e.Summary)
	}
}

func TestParseAuthLine_SudoCommand(t *testing.T) {
	line := "May  6 18:30:05 odin sudo[5678]:    bruce : TTY=pts/0 ; PWD=/home/bruce ; USER=root ; COMMAND=/usr/bin/systemctl restart nginx"
	e, ok := parseAuthLine(line, 2026)
	if !ok {
		t.Fatal("expected ok=true for sudo command")
	}
	if e.Level != event.Info {
		t.Errorf("level = %v, want Info", e.Level)
	}
	if !strings.Contains(e.Summary, "COMMAND=") {
		t.Errorf("summary missing COMMAND: %s", e.Summary)
	}
}

func TestParseAuthLine_InvalidUser(t *testing.T) {
	line := "May  6 18:31:00 odin sshd[9999]: Invalid user admin from 185.0.0.1 port 54321"
	e, ok := parseAuthLine(line, 2026)
	if !ok {
		t.Fatal("expected ok=true for invalid user")
	}
	if e.Level != event.Warn {
		t.Errorf("level = %v, want Warn", e.Level)
	}
}

func TestParseAuthLine_MaxAuthAttempts(t *testing.T) {
	line := "May  6 18:32:00 odin sshd[1111]: error: maximum authentication attempts exceeded for invalid user root from 185.0.0.1 port 33333 ssh2 [preauth]"
	e, ok := parseAuthLine(line, 2026)
	if !ok {
		t.Fatal("expected ok=true for max auth attempts")
	}
	if e.Level != event.Error {
		t.Errorf("level = %v, want Error", e.Level)
	}
}

func TestParseAuthLine_SessionOpenedRoot(t *testing.T) {
	line := "May  6 18:30:06 odin sudo[5679]: pam_unix(sudo:session): session opened for user root(uid=0) by bruce(uid=1000)"
	e, ok := parseAuthLine(line, 2026)
	if !ok {
		t.Fatal("expected ok=true for root session opened")
	}
	if e.Level != event.Warn {
		t.Errorf("root session level = %v, want Warn", e.Level)
	}
}

func TestParseAuthLine_UninterestingLine_Skipped(t *testing.T) {
	// Connection closed lines and preauth failures are noisy — skip them
	line := "May  6 18:31:01 odin sshd[9999]: Connection closed by invalid user admin 185.0.0.1 port 54321 [preauth]"
	_, ok := parseAuthLine(line, 2026)
	if ok {
		t.Error("connection closed line should not be emitted")
	}
}

func TestParseAuthLine_ShortLine_Skipped(t *testing.T) {
	_, ok := parseAuthLine("too short", 2026)
	if ok {
		t.Error("short line should not be emitted")
	}
}

func TestAuthCollectorFromFixture(t *testing.T) {
	f, err := os.Open("../../testdata/auth/auth.log")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	since := time.Date(2026, 5, 6, 18, 0, 0, 0, time.Local)
	until := time.Date(2026, 5, 6, 19, 0, 0, 0, time.Local)

	events, err := parseAuthLog(f, since, until, 2026)
	if err != nil {
		t.Fatal(err)
	}

	if len(events) == 0 {
		t.Fatal("no events parsed from fixture")
	}

	levels := map[event.Level]int{}
	for _, e := range events {
		levels[e.Level]++
		if e.Category != event.Auth {
			t.Errorf("event category = %v, want Auth", e.Category)
		}
		if e.Source != "auth" {
			t.Errorf("event source = %q, want auth", e.Source)
		}
	}

	// Fixture has: failed password(W), accepted(I), sudo cmd(I), root session(W),
	// invalid user(W), max attempts(E), accepted(I), sudo cmd(I), auth failure(W)
	if levels[event.Error] < 1 {
		t.Error("expected at least one Error event (max auth attempts)")
	}
	if levels[event.Warn] < 2 {
		t.Error("expected at least two Warn events (failed password, invalid user)")
	}
	if levels[event.Info] < 2 {
		t.Error("expected at least two Info events (accepted publickey, sudo)")
	}
}
