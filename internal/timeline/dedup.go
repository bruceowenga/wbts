package timeline

import (
	"fmt"
	"regexp"

	"github.com/bruceowenga/wbts/pkg/event"
)

const dedupWindow = 5 * 60 // seconds — same fingerprint within this window is collapsed

// timestampRe strips embedded timestamps from log message bodies so that
// repeated messages with different timestamps get the same fingerprint.
var timestampRe = regexp.MustCompile(
	`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?Z?` + // RFC3339
		`|\d{4}/\d{2}/\d{2} - \d{2}:\d{2}:\d{2}` + // GIN format
		`|\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2}`,   // plain datetime
)

// deduplicate collapses repeated events with the same fingerprint that fall
// within a rolling dedupWindow-second window. The first occurrence is kept and
// annotated with [×N] when N > 1; subsequent occurrences in the same window
// are dropped. A new window opens after dedupWindow seconds of silence.
//
// This prevents a single service in a persistent error loop from flooding
// incident window detection — the first representative event still appears,
// carrying the full count, so the information is not lost.
func deduplicate(events []event.Event) []event.Event {
	if len(events) == 0 {
		return events
	}

	type group struct {
		firstIdx    int
		count       int
		windowStart int64 // Unix seconds
	}

	groups := make(map[string]*group, 64)
	keepCount := make([]int, len(events)) // keepCount[i] = final count for event i, 0 = drop

	for i, e := range events {
		fp := fingerprint(e)
		ts := e.Timestamp.Unix()

		if g, ok := groups[fp]; ok && ts-g.windowStart < dedupWindow {
			g.count++
			keepCount[g.firstIdx] = g.count
			// keepCount[i] stays 0 → drop this event
			continue
		}
		// Start a new window for this fingerprint
		groups[fp] = &group{firstIdx: i, count: 1, windowStart: ts}
		keepCount[i] = 1
	}

	result := make([]event.Event, 0, len(events))
	for i, e := range events {
		n := keepCount[i]
		if n == 0 {
			continue
		}
		if n > 1 {
			e.Summary = fmt.Sprintf("%s [×%d]", tlTruncate(e.Summary, 100), n)
		}
		result = append(result, e)
	}
	return result
}

// fingerprint produces a stable key for grouping semantically identical events.
// Strips embedded timestamps and normalises whitespace so that repeated messages
// from chatty services (cloudflared, Docker resolver, etc.) map to the same key.
func fingerprint(e event.Event) string {
	s := timestampRe.ReplaceAllString(e.Summary, "")
	if len(s) > 80 {
		s = s[:80]
	}
	return fmt.Sprintf("%d|%s", int(e.Level), s)
}

func tlTruncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}
