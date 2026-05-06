// Package event defines the core types shared across all wbts components.
// Third-party collectors import this package to implement the Collector interface.
package event

import "time"

// Level represents the severity of an event, ordered from least to most severe.
type Level int

const (
	Info Level = iota
	Warn
	Error
	Critical
)

func (l Level) String() string {
	switch l {
	case Info:
		return "INFO"
	case Warn:
		return "WARN"
	case Error:
		return "ERRO"
	case Critical:
		return "CRIT"
	default:
		return "????"
	}
}

// Category classifies which subsystem produced the event.
type Category int

const (
	Kernel Category = iota
	Service
	Container
	Package
	Auth
	Cron
	Disk
)

func (c Category) String() string {
	switch c {
	case Kernel:
		return "KERNEL"
	case Service:
		return "SERVICE"
	case Container:
		return "DOCKER"
	case Package:
		return "PACKAGE"
	case Auth:
		return "AUTH"
	case Cron:
		return "CRON"
	case Disk:
		return "DISK"
	default:
		return "UNKNOWN"
	}
}

// Event is a single normalized log entry from any collector.
type Event struct {
	Timestamp time.Time
	Source    string
	Level     Level
	Category  Category
	Summary   string
	Raw       string // original log line, shown on expand
}
