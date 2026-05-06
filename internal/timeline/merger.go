package timeline

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/bruceowenga/wbts/pkg/event"
)

const (
	incidentWindowDuration = 60 * time.Second
	incidentEventThreshold = 3 // min error/crit events to declare a window
)

// IncidentWindow marks a dense cluster of errors/criticals within a short time span.
type IncidentWindow struct {
	Start           time.Time
	End             time.Time
	EventCount      int
	Categories      []string
	FirstFaultIndex int // index into Timeline.Events; -1 if none found
}

// Timeline is the merged, filtered, annotated result of a collection run.
type Timeline struct {
	Events          []event.Event
	IncidentWindows []IncidentWindow
	SkippedSources  map[string]error
}

// Build runs all available collectors concurrently, merges their output,
// applies noise filtering, and annotates incident windows.
func Build(ctx context.Context, collectors []event.Collector, opts event.Options) (*Timeline, error) {
	skipped := make(map[string]error)
	var active []event.Collector
	for _, c := range collectors {
		if err := c.Available(); err != nil {
			skipped[c.Name()] = err
		} else {
			active = append(active, c)
		}
	}

	// Fan out to all active collectors concurrently
	channels := make([]<-chan event.Event, len(active))
	for i, c := range active {
		ch, err := c.Collect(ctx, opts)
		if err != nil {
			skipped[c.Name()] = err
			continue
		}
		channels[i] = ch
	}

	// Drain all channels — bounded by the 24h limit enforced in main
	var raw []event.Event
	for _, ch := range channels {
		if ch == nil {
			continue
		}
		for e := range ch {
			raw = append(raw, e)
		}
	}

	sort.Slice(raw, func(i, j int) bool {
		return raw[i].Timestamp.Before(raw[j].Timestamp)
	})

	// Suppress known-routine events. ERROR and CRITICAL always pass through.
	filtered := raw[:0]
	for _, e := range raw {
		if isNoise(e.Level, e.Summary) {
			continue
		}
		filtered = append(filtered, e)
	}

	// Collapse repeated errors from the same service into annotated single events.
	// This prevents a service in a persistent error loop (e.g. cloudflared reconnects,
	// Docker DNS retries) from flooding incident window detection.
	deduped := deduplicate(filtered)

	tl := &Timeline{
		Events:         deduped,
		SkippedSources: skipped,
	}
	tl.detectIncidentWindows()
	return tl, nil
}

// detectIncidentWindows scans the sorted event list for dense clusters of
// error-or-above events within a sliding 60-second window.
func (tl *Timeline) detectIncidentWindows() {
	events := tl.Events
	i := 0
	for i < len(events) {
		// Skip until we hit an error or critical event to anchor a window
		if events[i].Level < event.Error {
			i++
			continue
		}

		windowEnd := events[i].Timestamp.Add(incidentWindowDuration)
		j := i
		errorCount := 0
		firstFaultIdx := -1
		catSet := make(map[string]struct{})

		for j < len(events) && !events[j].Timestamp.After(windowEnd) {
			if events[j].Level >= event.Error {
				errorCount++
				catSet[events[j].Category.String()] = struct{}{}
				if firstFaultIdx == -1 {
					firstFaultIdx = j
				}
			}
			j++
		}

		if errorCount >= incidentEventThreshold {
			cats := make([]string, 0, len(catSet))
			for k := range catSet {
				cats = append(cats, k)
			}
			sort.Strings(cats)
			tl.IncidentWindows = append(tl.IncidentWindows, IncidentWindow{
				Start:           events[i].Timestamp,
				End:             events[j-1].Timestamp,
				EventCount:      errorCount,
				Categories:      cats,
				FirstFaultIndex: firstFaultIdx,
			})
			i = j // advance past the window
		} else {
			i++
		}
	}
}

// categorySummary returns a human-readable list of categories for display.
func categorySummary(cats []string) string {
	return strings.Join(cats, ", ")
}
