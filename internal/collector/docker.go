package collector

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/bruceowenga/wbts/pkg/event"
)

const dockerSocket = "/var/run/docker.sock"

// DockerCollector reads container lifecycle events from the Docker socket API.
// It requires no Docker SDK — the events endpoint is three HTTP calls over a
// Unix socket.
type DockerCollector struct {
	socketPath string
}

func NewDockerCollector() *DockerCollector {
	return &DockerCollector{socketPath: dockerSocket}
}

func (d *DockerCollector) Name() string { return "docker" }

func (d *DockerCollector) Available() error {
	if _, err := os.Stat(d.socketPath); err != nil {
		return fmt.Errorf("docker socket not found at %s (is Docker running?)", d.socketPath)
	}
	resp, err := d.httpClient().Get("http://localhost/_ping")
	if err != nil {
		return fmt.Errorf("docker socket not accessible: %w (try: sudo usermod -aG docker $USER)", err)
	}
	resp.Body.Close()
	return nil
}

func (d *DockerCollector) Collect(ctx context.Context, opts event.Options) (<-chan event.Event, error) {
	url := fmt.Sprintf("http://localhost/events?since=%d&until=%d",
		opts.Since.Unix(), opts.Until.Unix())

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("docker: build request: %w", err)
	}

	resp, err := d.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("docker: events request: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("docker: events API returned HTTP %d", resp.StatusCode)
	}

	ch := make(chan event.Event, 256)
	go func() {
		defer close(ch)
		defer resp.Body.Close()

		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 64*1024), 64*1024)
		for scanner.Scan() {
			e, ok := parseDockerEvent(scanner.Bytes())
			if !ok {
				continue
			}
			if opts.Filter.Container != "" && !strings.Contains(e.Summary, opts.Filter.Container) {
				continue
			}
			select {
			case ch <- e:
			case <-ctx.Done():
				return
			}
		}
	}()

	return ch, nil
}

func (d *DockerCollector) httpClient() *http.Client {
	socketPath := d.socketPath
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
			},
		},
	}
}

// dockerRawEvent mirrors the Docker events API response shape.
type dockerRawEvent struct {
	Type     string      `json:"Type"`
	Action   string      `json:"Action"`
	Actor    dockerActor `json:"Actor"`
	Time     int64       `json:"time"`
	TimeNano int64       `json:"timeNano"`
}

type dockerActor struct {
	ID         string            `json:"ID"`
	Attributes map[string]string `json:"Attributes"`
}

func parseDockerEvent(data []byte) (event.Event, bool) {
	var raw dockerRawEvent
	if err := json.Unmarshal(data, &raw); err != nil {
		return event.Event{}, false
	}

	ts := time.Unix(raw.Time, raw.TimeNano%1_000_000_000).UTC()

	switch raw.Type {
	case "container":
		return parseContainerEvent(ts, raw)
	case "image":
		return parseImageEvent(ts, raw)
	default:
		return event.Event{}, false
	}
}

func parseContainerEvent(ts time.Time, raw dockerRawEvent) (event.Event, bool) {
	attrs := raw.Actor.Attributes
	name := attrs["name"]
	if name == "" {
		// Fall back to short ID (12 chars) when name is absent
		id := raw.Actor.ID
		if len(id) > 12 {
			id = id[:12]
		}
		name = id
	}
	image := attrs["image"]

	switch raw.Action {
	case "oom":
		return dockerEvent(ts, event.Critical,
			fmt.Sprintf("container %s (%s) OOM killed", name, image), raw), true

	case "die":
		exitCode := attrs["exitCode"]
		if exitCode == "0" {
			return event.Event{}, false // clean exit is not signal
		}
		summary := fmt.Sprintf("container %s (%s) exited (code %s)", name, image, exitCode)
		if exitCode == "137" {
			summary = fmt.Sprintf("container %s (%s) killed (code 137 — OOM or SIGKILL)", name, image)
		}
		return dockerEvent(ts, event.Error, summary, raw), true

	case "kill":
		sig := attrs["signal"]
		return dockerEvent(ts, event.Warn,
			fmt.Sprintf("container %s (%s) sent signal %s", name, image, sig), raw), true

	case "restart":
		return dockerEvent(ts, event.Warn,
			fmt.Sprintf("container %s (%s) restarted", name, image), raw), true

	case "start":
		return dockerEvent(ts, event.Info,
			fmt.Sprintf("container %s (%s) started", name, image), raw), true

	case "health_status: unhealthy":
		return dockerEvent(ts, event.Error,
			fmt.Sprintf("container %s (%s) health check failing", name, image), raw), true

	case "health_status: healthy":
		return dockerEvent(ts, event.Info,
			fmt.Sprintf("container %s (%s) health check recovered", name, image), raw), true

	default:
		return event.Event{}, false
	}
}

func parseImageEvent(ts time.Time, raw dockerRawEvent) (event.Event, bool) {
	if raw.Action != "pull" {
		return event.Event{}, false
	}
	image := raw.Actor.Attributes["name"]
	if image == "" {
		image = raw.Actor.ID
	}
	return dockerEvent(ts, event.Info,
		fmt.Sprintf("image pulled: %s", image), raw), true
}

func dockerEvent(ts time.Time, lvl event.Level, summary string, raw dockerRawEvent) event.Event {
	encoded, _ := json.Marshal(raw)
	return event.Event{
		Timestamp: ts,
		Source:    "docker",
		Level:     lvl,
		Category:  event.Container,
		Summary:   summary,
		Raw:       string(encoded),
	}
}
