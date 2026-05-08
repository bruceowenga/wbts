# Writing a wbts Collector

A collector is any type that satisfies the `event.Collector` interface from
`github.com/bruceowenga/wbts/pkg/event`. That package is the only import you
need to build a third-party collector — it has no dependencies beyond the
standard library.

---

## The interface

```go
type Collector interface {
    Name() string
    Available() error
    Collect(ctx context.Context, opts Options) (<-chan Event, error)
}
```

**`Name() string`**
Short identifier shown in `wbts check-perms` output. Use lowercase with no spaces:
`"nginx"`, `"postgres"`, `"haproxy"`.

**`Available() error`**
Called before `Collect`. Return `nil` if your collector can run on this system.
Return an error with a human-readable fix hint if it cannot:
```go
func (n *NginxCollector) Available() error {
    if _, err := os.Stat(n.logPath); err != nil {
        return fmt.Errorf("nginx access log not found at %s", n.logPath)
    }
    return nil
}
```
If `Available()` returns an error, the collector is skipped and the error is
shown in the `! source unavailable: ...` footer. `Collect` is never called.

**`Collect(ctx context.Context, opts Options) (<-chan Event, error)`**
Return a channel of events. Emit all events in the `[opts.Since, opts.Until]`
time range. Close the channel when done. Respect `ctx.Done()` to avoid leaking
goroutines when the caller cancels early.

---

## The Event struct

```go
type Event struct {
    Timestamp time.Time  // when the event occurred (UTC)
    Source    string     // your collector's name
    Level     Level      // Info, Warn, Error, or Critical
    Category  Category   // what subsystem produced this
    Summary   string     // one-line human-readable description (≤120 chars)
    Raw       string     // original log line — shown when user presses 'e' in TUI
}
```

**Level guidance:**

| Level | When to use |
|---|---|
| `Critical` | System is down or data loss is occurring (OOM kill, kernel panic) |
| `Error` | A service or request failed (non-zero exit, 5xx, connection refused) |
| `Warn` | Something abnormal that may be precursor to a failure |
| `Info` | Context events (service started, login accepted, package installed) |

Prefer `Warn` over `Error` when the event is expected to recover on its own.
Prefer `Error` over `Warn` when human attention is required.

**Category guidance:**

| Category | Use for |
|---|---|
| `Kernel` | Kernel messages, hardware errors, kernel module events |
| `Service` | systemd services, application-level events |
| `Container` | Docker/containerd/k8s container lifecycle |
| `Package` | Package manager installs, upgrades, removals |
| `Auth` | Login attempts, sudo, session events |
| `Cron` | Scheduled job execution and failures |
| `Disk` | I/O errors, filesystem events, storage failures |

---

## Minimal working example

A collector that reads nginx access logs and surfaces 5xx errors:

```go
package main

import (
    "bufio"
    "context"
    "fmt"
    "os"
    "strings"
    "time"

    "github.com/bruceowenga/wbts/pkg/event"
)

type NginxCollector struct {
    path string
}

func NewNginxCollector() *NginxCollector {
    return &NginxCollector{path: "/var/log/nginx/access.log"}
}

func (n *NginxCollector) Name() string { return "nginx" }

func (n *NginxCollector) Available() error {
    if _, err := os.Stat(n.path); err != nil {
        return fmt.Errorf("nginx access log not found at %s", n.path)
    }
    return nil
}

func (n *NginxCollector) Collect(ctx context.Context, opts event.Options) (<-chan event.Event, error) {
    f, err := os.Open(n.path)
    if err != nil {
        return nil, fmt.Errorf("nginx: open log: %w", err)
    }

    ch := make(chan event.Event, 256)
    go func() {
        defer close(ch)
        defer f.Close()

        scanner := bufio.NewScanner(f)
        for scanner.Scan() {
            e, ok := parseNginxLine(scanner.Text(), opts.Since, opts.Until)
            if !ok {
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

// parseNginxLine parses combined log format and emits 5xx responses.
// Combined log format: 127.0.0.1 - - [06/May/2026:18:13:42 +0000] "GET / HTTP/1.1" 500 1234 "-" "curl/7.81"
func parseNginxLine(line string, since, until time.Time) (event.Event, bool) {
    // ... parse timestamp, status code ...
    // Only emit 5xx responses as errors; skip everything else.
    // Return event.Event{}, false for lines to skip.
    return event.Event{}, false // replace with real parser
}
```

---

## Contracts your collector must honour

**1. Only emit events in `[opts.Since, opts.Until]`.**
The timeline merger does not re-filter by time — if you emit events outside the
range, they will appear in the output.

**2. Close the channel when done.**
`for e := range ch` in the merger depends on the channel being closed. A
goroutine that never closes the channel will deadlock the build.

**3. Respect `ctx.Done()`.**
Check `ctx.Done()` in your send loop. If the context is cancelled (user presses
`q` in the TUI, or the process receives a signal), your goroutine should exit
promptly.

**4. Never block in `Available()`.**
`Available()` is called sequentially for all collectors before any `Collect`
call. A slow `Available()` (network call, slow disk probe) delays startup for
every other collector. Do the minimum needed to determine availability — check
file existence, binary in PATH, socket stat.

**5. `Collect` errors go into `SkippedSources`.**
If `Collect` returns an error, the collector is reported as unavailable in the
`! source unavailable` footer. This is the same place as `Available()` errors —
from the user's perspective they're equivalent.

---

## Reading rotated log files

If your collector reads a file that logrotate manages (`.1`, `.2.gz`,
date-based variants), you can read across rotations by building a multi-file
reader over all rotated paths. The built-in collectors in
`internal/collector/rotate.go` expose helpers for this, but since that's an
internal package you'll need to implement equivalent logic in your own package:

```go
import (
    "compress/gzip"
    "io"
    "os"
    "path/filepath"
    "strings"
)

// openLogWithRotations returns a reader covering the active log and any
// numbered or date-based rotated variants (auth.log.1, auth.log.2.gz, etc.)
func openLogWithRotations(base string) (io.Reader, func(), error) {
    dir, name := filepath.Dir(base), filepath.Base(base)

    var readers []io.Reader
    var closers []io.Closer

    // Try numbered rotations (.8.gz → .1)
    for i := 8; i >= 1; i-- {
        for _, p := range []string{
            filepath.Join(dir, fmt.Sprintf("%s.%d.gz", name, i)),
            filepath.Join(dir, fmt.Sprintf("%s.%d", name, i)),
        } {
            r, c, ok := tryOpen(p)
            if ok {
                readers = append(readers, r)
                closers = append(closers, c...)
            }
        }
    }

    // Active log last
    f, err := os.Open(base)
    if err != nil {
        return nil, func() {}, err
    }
    readers = append(readers, f)
    closers = append(closers, f)

    closeAll := func() {
        for _, c := range closers {
            c.Close()
        }
    }
    return io.MultiReader(readers...), closeAll, nil
}

func tryOpen(path string) (io.Reader, []io.Closer, bool) {
    f, err := os.Open(path)
    if err != nil {
        return nil, nil, false
    }
    if strings.HasSuffix(path, ".gz") {
        gz, err := gzip.NewReader(f)
        if err != nil {
            f.Close()
            return nil, nil, false
        }
        return gz, []io.Closer{gz, f}, true
    }
    return f, []io.Closer{f}, true
}
```

---

## Registering your collector

Third-party collectors are not registered inside the wbts binary — you build
your own binary that embeds wbts's timeline and output packages alongside your
collector:

```go
package main

import (
    "context"
    "os"

    "github.com/bruceowenga/wbts/internal/output"
    "github.com/bruceowenga/wbts/internal/timeline"
    "github.com/bruceowenga/wbts/pkg/event"
)

func main() {
    opts := event.Options{
        Since: time.Now().Add(-2 * time.Hour),
        Until: time.Now(),
    }

    collectors := []event.Collector{
        NewNginxCollector(),
        // add more collectors here
    }

    tl, err := timeline.Build(context.Background(), collectors, opts)
    if err != nil {
        fmt.Fprintln(os.Stderr, err)
        os.Exit(1)
    }

    output.Render(os.Stdout, tl, output.Options{})
}
```

> **Note:** `internal/timeline` and `internal/output` are internal packages.
> If you need them in an external binary, either vendor the wbts source or
> open an issue requesting that these packages be promoted to `pkg/`.

---

## Reference implementations

The built-in collectors are good examples of different patterns:

| Pattern | Reference file |
|---|---|
| Shell-out + stream JSON | `internal/collector/journald.go` |
| Read binary ring buffer once | `internal/collector/dmesg.go` |
| HTTP over Unix socket | `internal/collector/docker.go` |
| Parse block-structured file | `internal/collector/apt.go` |
| Parse line-by-line with year inference | `internal/collector/auth.go` |
| Shell-out + parse JSON output | `internal/collector/kube.go` |

---

## Opening issues and PRs

If you've built a collector you'd like bundled into wbts, open a PR with:

1. `internal/collector/<name>.go` — the implementation
2. `internal/collector/<name>_test.go` — fixture-based tests (no live system required)
3. `testdata/<name>/` — realistic fixture files
4. A line in `cmd/wbts/main.go` adding your collector to the default list
5. A row in the README collectors table

The bar for bundling is: fixture-based tests that run in CI without any
external service, and a clear `Available()` check with a useful error message.
