package event

import (
	"context"
	"time"
)

// Filter narrows collection to events involving a specific container or service.
// Empty strings mean no filter.
type Filter struct {
	Container string
	Service   string
}

// Options controls the time range and optional filters for a collection run.
type Options struct {
	Since  time.Time
	Until  time.Time
	Filter Filter
}

// Collector is the interface all log source adapters must implement.
// The Available method is called before Collect; if it returns an error,
// the collector is skipped and the error is reported via SkippedSources.
type Collector interface {
	Name() string
	// Available returns nil if this collector can run, or an error describing
	// why it cannot (e.g. missing binary, insufficient permissions).
	Available() error
	Collect(ctx context.Context, opts Options) (<-chan Event, error)
}
