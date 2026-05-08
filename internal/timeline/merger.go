package timeline

import (
	"context"
	"sort"
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

// CollectorState records whether a collector has completed and any error it encountered.
type CollectorState struct {
	Name  string
	Done  bool
	Error error // nil = completed successfully; non-nil = skipped or failed
}

// ProgressUpdate is emitted on the BuildStreaming channel each time a collector
// finishes. The Timeline is a fully-processed snapshot of all events collected so far.
type ProgressUpdate struct {
	Timeline        *Timeline
	CollectorStates []CollectorState
	Done            bool // true on the final update (all collectors finished)
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

	channels := make([]<-chan event.Event, len(active))
	for i, c := range active {
		ch, err := c.Collect(ctx, opts)
		if err != nil {
			skipped[c.Name()] = err
			continue
		}
		channels[i] = ch
	}

	var raw []event.Event
	for _, ch := range channels {
		if ch == nil {
			continue
		}
		for e := range ch {
			raw = append(raw, e)
		}
	}

	return processRaw(raw, skipped), nil
}

// BuildStreaming fans out to all collectors concurrently and sends a ProgressUpdate
// on the returned channel each time any single collector finishes. Each update
// contains a fully-processed Timeline snapshot of all events collected so far.
// The channel is closed after the final update (Done=true).
func BuildStreaming(ctx context.Context, collectors []event.Collector, opts event.Options) <-chan ProgressUpdate {
	out := make(chan ProgressUpdate, len(collectors)+1)

	go func() {
		defer close(out)

		skipped := make(map[string]error)
		states := make([]CollectorState, 0, len(collectors))
		var active []event.Collector

		for _, c := range collectors {
			if err := c.Available(); err != nil {
				skipped[c.Name()] = err
				states = append(states, CollectorState{Name: c.Name(), Done: true, Error: err})
			} else {
				active = append(active, c)
				states = append(states, CollectorState{Name: c.Name(), Done: false})
			}
		}

		if len(active) == 0 {
			out <- ProgressUpdate{
				Timeline:        processRaw(nil, skipped),
				CollectorStates: copyStates(states),
				Done:            true,
			}
			return
		}

		type result struct {
			name   string
			events []event.Event
			err    error
		}
		results := make(chan result, len(active))

		for _, c := range active {
			c := c
			go func() {
				ch, err := c.Collect(ctx, opts)
				if err != nil {
					results <- result{name: c.Name(), err: err}
					return
				}
				var events []event.Event
				for e := range ch {
					events = append(events, e)
				}
				results <- result{name: c.Name(), events: events}
			}()
		}

		var accumulated []event.Event
		for i := 0; i < len(active); i++ {
			r := <-results

			// Update state for this collector
			for j := range states {
				if states[j].Name == r.name {
					states[j].Done = true
					states[j].Error = r.err
					break
				}
			}
			if r.err != nil {
				skipped[r.name] = r.err
			} else {
				accumulated = append(accumulated, r.events...)
			}

			out <- ProgressUpdate{
				Timeline:        processRaw(accumulated, skipped),
				CollectorStates: copyStates(states),
				Done:            i == len(active)-1,
			}
		}
	}()

	return out
}

// processRaw applies sorting, noise filtering, deduplication, and incident window
// detection to a raw event slice. Shared between Build and BuildStreaming.
func processRaw(raw []event.Event, skipped map[string]error) *Timeline {
	sort.Slice(raw, func(i, j int) bool {
		return raw[i].Timestamp.Before(raw[j].Timestamp)
	})

	filtered := make([]event.Event, 0, len(raw))
	for _, e := range raw {
		if !isNoise(e.Level, e.Summary) {
			filtered = append(filtered, e)
		}
	}

	deduped := deduplicate(filtered)

	tl := &Timeline{
		Events:         deduped,
		SkippedSources: skipped,
	}
	tl.detectIncidentWindows()
	return tl
}

func copyStates(states []CollectorState) []CollectorState {
	cp := make([]CollectorState, len(states))
	copy(cp, states)
	return cp
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

