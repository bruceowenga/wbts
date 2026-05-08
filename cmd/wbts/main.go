package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/bruceowenga/wbts/internal/collector"
	"github.com/bruceowenga/wbts/internal/output"
	"github.com/bruceowenga/wbts/internal/timeline"
	"github.com/bruceowenga/wbts/pkg/event"
)

const maxDefaultRange = 24 * time.Hour

// Injected at build time by GoReleaser ldflags.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	var (
		since     string
		until     string
		container string
		noColor   bool
		asJSON    bool
		summary   bool
		force     bool
		noTUI     bool
	)

	cmd := &cobra.Command{
		Use:     "wbts",
		Short:   "what broke the server — forensic incident timeline for Linux/Docker",
		Version: fmt.Sprintf("%s (commit: %s, built: %s)", version, commit, date),
		Long: `wbts correlates logs from journald, dmesg, Docker events, apt, auth, and cron
into a single chronological timeline. Run it after an incident to reconstruct what happened.

Examples:
  wbts --since 2h
  wbts --since "2026-05-05 02:00" --until "2026-05-05 04:00"
  wbts --since 1h --container app_web_1
  wbts --since 30m --summary`,
		RunE: func(cmd *cobra.Command, args []string) error {
			sinceTime, err := parseSince(since)
			if err != nil {
				return fmt.Errorf("--since: %w", err)
			}

			untilTime := time.Now()
			if until != "" {
				untilTime, err = parseTimestamp(until)
				if err != nil {
					return fmt.Errorf("--until: %w", err)
				}
			}

			if sinceTime.After(untilTime) {
				return fmt.Errorf("--since (%s) is after --until (%s)",
					sinceTime.Format(time.RFC3339), untilTime.Format(time.RFC3339))
			}

			if !force && untilTime.Sub(sinceTime) > maxDefaultRange {
				return fmt.Errorf("time range exceeds 24h; use --force to override")
			}

			opts := event.Options{
				Since: sinceTime,
				Until: untilTime,
				Filter: event.Filter{
					Container: container,
				},
			}

			collectors := []event.Collector{
				collector.NewJournaldCollector(),
				collector.NewDmesgCollector(),
				collector.NewDockerCollector(),
				collector.NewKubeCollector(),
				collector.NewAptCollector(),
				collector.NewDnfCollector(),
				collector.NewAuthCollector(),
			}

			renderOpts := output.Options{
				NoColor: noColor,
				JSON:    asJSON,
				Summary: summary,
				NoTUI:   noTUI,
			}

			// TUI path: stream events as each collector finishes
			if output.ShouldUseTUI(os.Stdout, renderOpts) {
				progressCh := timeline.BuildStreaming(context.Background(), collectors, opts)
				return output.RunTUI(progressCh)
			}

			// Plain renderer path: wait for all collectors before rendering
			tl, err := timeline.Build(context.Background(), collectors, opts)
			if err != nil {
				return fmt.Errorf("build timeline: %w", err)
			}
			return output.Render(os.Stdout, tl, renderOpts)
		},
	}

	cmd.Flags().StringVar(&since, "since", "", "start of time range: duration (2h, 30m) or timestamp (2006-01-02 15:04:05)")
	cmd.Flags().StringVar(&until, "until", "", "end of time range (default: now)")
	cmd.Flags().StringVar(&container, "container", "", "filter events involving this container name")
	cmd.Flags().BoolVar(&noColor, "no-color", false, "disable ANSI colours")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output events as JSON array")
	cmd.Flags().BoolVar(&summary, "summary", false, "show only incident window summaries")
	cmd.Flags().BoolVar(&force, "force", false, "allow time ranges exceeding 24h")
	cmd.Flags().BoolVar(&noTUI, "no-tui", false, "disable interactive TUI, use plain output")
	_ = cmd.MarkFlagRequired("since")

	cmd.AddCommand(newCheckPermsCmd())
	return cmd
}

func newCheckPermsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "check-perms",
		Short: "show which log sources wbts can access on this system",
		RunE: func(cmd *cobra.Command, args []string) error {
			collectors := []event.Collector{
				collector.NewJournaldCollector(),
				collector.NewDmesgCollector(),
				collector.NewDockerCollector(),
				collector.NewKubeCollector(),
				collector.NewAptCollector(),
				collector.NewDnfCollector(),
				collector.NewAuthCollector(),
			}

			fmt.Printf("%-20s  %s\n", "COLLECTOR", "STATUS")
			fmt.Printf("%-20s  %s\n", strings.Repeat("-", 9), strings.Repeat("-", 6))
			for _, c := range collectors {
				if err := c.Available(); err != nil {
					fmt.Printf("%-20s  ✗ %s\n", c.Name(), err)
				} else {
					fmt.Printf("%-20s  ✓ available\n", c.Name())
				}
			}
			return nil
		},
	}
}

// parseSince accepts either a Go duration ("2h", "30m") or a timestamp.
func parseSince(s string) (time.Time, error) {
	if d, err := time.ParseDuration(s); err == nil {
		if d <= 0 {
			return time.Time{}, fmt.Errorf("duration must be positive, got %s", s)
		}
		return time.Now().Add(-d), nil
	}
	return parseTimestamp(s)
}

// parseTimestamp tries multiple human-friendly formats.
func parseTimestamp(s string) (time.Time, error) {
	formats := []string{
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"2006-01-02",
	}
	for _, f := range formats {
		if t, err := time.ParseInLocation(f, s, time.Local); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("expected duration (2h, 30m) or timestamp (2006-01-02 15:04:05), got %q", s)
}
