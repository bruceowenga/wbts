package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/bruceowenga/wbts/pkg/event"
)

// KubeCollector surfaces Kubernetes Warning events from the cluster using
// kubectl get events. crictl events only streams live events without
// historical query support, so kubectl is the right tool here.
// Works with both kubectl and k3s (which bundles kubectl as: k3s kubectl).
//
// Note: k8s events have a default TTL of 1h — events older than that
// may not be returned even if they fall within --since.
type KubeCollector struct {
	cmd string // "kubectl" or "k3s"
}

func NewKubeCollector() *KubeCollector {
	for _, cmd := range []string{"kubectl", "k3s"} {
		if _, err := exec.LookPath(cmd); err == nil {
			return &KubeCollector{cmd: cmd}
		}
	}
	return &KubeCollector{cmd: "kubectl"}
}

func (k *KubeCollector) Name() string { return "kubernetes" }

func (k *KubeCollector) Available() error {
	if _, err := exec.LookPath(k.cmd); err != nil {
		return fmt.Errorf("kubectl not found in PATH (k3s includes it as: k3s kubectl)")
	}
	return nil
}

// args returns the correct argument list for kubectl or k3s kubectl.
func (k *KubeCollector) args(extra ...string) []string {
	if k.cmd == "k3s" {
		return append([]string{"kubectl"}, extra...)
	}
	return extra
}

func (k *KubeCollector) Collect(ctx context.Context, opts event.Options) (<-chan event.Event, error) {
	cmd := exec.CommandContext(ctx, k.cmd,
		k.args("get", "events", "-A", "-o", "json")...)

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("kubernetes: kubectl get events: %w", err)
	}

	ch := make(chan event.Event, 256)
	go func() {
		defer close(ch)

		events, err := parseKubeEvents(out, opts.Since, opts.Until)
		if err != nil {
			return
		}
		for _, e := range events {
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

// kubeEventList mirrors the shape of `kubectl get events -o json`.
type kubeEventList struct {
	Items []kubeEvent `json:"items"`
}

type kubeEvent struct {
	InvolvedObject struct {
		Kind      string `json:"kind"`
		Namespace string `json:"namespace"`
		Name      string `json:"name"`
	} `json:"involvedObject"`
	Reason         string    `json:"reason"`
	Message        string    `json:"message"`
	Type           string    `json:"type"` // "Normal" or "Warning"
	Count          int32     `json:"count"`
	FirstTimestamp time.Time `json:"firstTimestamp"`
	LastTimestamp  time.Time `json:"lastTimestamp"`
	// EventTime is used by newer k8s event API (v1beta1 Events)
	EventTime *time.Time `json:"eventTime,omitempty"`
}

// effectiveTime returns the best available timestamp for an event.
func (e kubeEvent) effectiveTime() time.Time {
	if e.EventTime != nil && !e.EventTime.IsZero() {
		return *e.EventTime
	}
	if !e.LastTimestamp.IsZero() {
		return e.LastTimestamp
	}
	return e.FirstTimestamp
}

func parseKubeEvents(data []byte, since, until time.Time) ([]event.Event, error) {
	var list kubeEventList
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("kubernetes: parse events: %w", err)
	}

	var events []event.Event
	for _, item := range list.Items {
		ts := item.effectiveTime()
		if ts.IsZero() || ts.Before(since) || ts.After(until) {
			continue
		}

		lvl := kubeEventLevel(item.Type, item.Reason)

		// Skip routine Normal events — only keep those with incident signal
		if lvl == event.Info {
			continue
		}

		cat := event.Container
		if item.InvolvedObject.Kind == "Node" {
			cat = event.Kernel // Node events relate to host-level issues
		}

		summary := buildKubeSummary(
			item.InvolvedObject.Kind,
			item.InvolvedObject.Namespace,
			item.InvolvedObject.Name,
			item.Reason,
			item.Message,
			item.Count,
		)

		events = append(events, event.Event{
			Timestamp: ts,
			Source:    "kubernetes",
			Level:     lvl,
			Category:  cat,
			Summary:   summary,
			Raw:       item.Message,
		})
	}
	return events, nil
}

// kubeEventLevel maps k8s event type + reason to wbts severity.
func kubeEventLevel(eventType, reason string) event.Level {
	if eventType == "Warning" {
		switch reason {
		case "OOMKilling":
			return event.Critical
		case "BackOff", "CrashLoopBackOff", "Failed", "FailedCreate",
			"FailedScheduling", "Unhealthy", "NodeNotReady", "NodeMemoryPressure",
			"NodeDiskPressure", "NodePIDPressure", "FailedMount", "FailedAttachVolume":
			return event.Error
		default:
			return event.Warn
		}
	}
	// Normal events: only surface ones with clear incident signal
	switch reason {
	case "Killing", "Evicted", "Preempting":
		return event.Warn
	default:
		return event.Info // will be filtered out in parseKubeEvents
	}
}

func buildKubeSummary(kind, namespace, name, reason, message string, count int32) string {
	obj := kind + " " + name
	if namespace != "" {
		obj += " (" + namespace + ")"
	}
	s := fmt.Sprintf("%s: %s — %s", obj, reason, truncate(message, 80))
	if count > 1 {
		s += fmt.Sprintf(" [×%d]", count)
	}
	return s
}
