# wbts — what broke the server?

```
curl -fsSL https://raw.githubusercontent.com/bruceowenga/wbts/main/scripts/install.sh | bash
```

```
$ wbts --since 3h

2026-05-06 18:13:42  [SERVICE]  ERRO  k3s.service: E0506 "Housekeeping took longer than expected"  ◄── FIRST FAULT?
2026-05-06 18:14:13  [KERNEL ]  WARN  workqueue: pci_pme_list_scan hogged CPU for >10000us 128 times
2026-05-06 18:14:35  [SERVICE]  ERRO  k3s.service: E0506 "Housekeeping took longer than expected"
2026-05-06 18:14:39  [SERVICE]  CRIT  alertmanager-ntfy-bridge.service: Forwarded alert: CRITICAL: SystemLoadCritical
2026-05-06 18:15:32  [SERVICE]  ERRO  cloudflared.service: ERR failed to run the datagram handler [×20]
2026-05-06 18:20:44  [SERVICE]  INFO  docker.service: restarting container 890c1b29 (my-app:latest)
2026-05-06 18:20:44  [DOCKER ]  WARN  container app_web_1 (my-app:latest) killed (code 137 — OOM or SIGKILL)
2026-05-06 18:12:00  [PACKAGE]  WARN  apt upgrade: nginx (1.18.0 → 1.18.1), curl (7.81.0 → 7.82.0)
2026-05-06 18:30:01  [AUTH   ]  INFO  sshd: Accepted publickey for bruce from 192.168.100.27 port 52341 ssh2

▶ INCIDENT WINDOW: 18:13:42–18:14:39 (4 events — KERNEL, SERVICE)
```

`wbts` correlates logs from journald, dmesg, Docker events, apt, auth, and cron into a
single chronological timeline. Run it after an incident to reconstruct what happened
without manually cross-referencing `journalctl`, `dmesg`, `docker events`, and `auth.log`.

---

## Installation

```bash
# One-liner (once v0.1.0 is released)
curl -fsSL https://raw.githubusercontent.com/bruceowenga/wbts/main/scripts/install.sh | bash

# Build from source
git clone https://github.com/bruceowenga/wbts
cd wbts && go build -o wbts ./cmd/wbts
```

## Usage

```bash
# Last 2 hours
wbts --since 2h

# Specific time range
wbts --since "2026-05-05 02:00" --until "2026-05-05 04:00"

# Filter to events involving a specific container
wbts --since 1h --container app_web_1

# Show only incident window summaries
wbts --since 4h --summary

# JSON output for piping to jq
wbts --since 1h --json | jq '.[] | select(.Level >= 2)'

# Check which log sources are accessible
wbts check-perms
```

## Collectors

| Collector | Source | What it captures |
|---|---|---|
| `journald` | `journalctl` | service starts/stops/crashes, systemd failures, embedded log levels |
| `dmesg` | kernel ring buffer | OOM kills, kernel panics, disk I/O errors, CPU hogging |
| `docker` | Docker socket API (`/events`) | container die/OOM/restart, health check failures, image pulls |
| `apt` | `/var/log/apt/history.log` | package installs, upgrades, removals with version arrows |
| `auth` | `/var/log/auth.log` | failed logins, accepted sessions, sudo commands, root sessions |

> **Docker events buffer:** The Docker events API stores events in an in-memory ring buffer
> (~1024 events). On busy systems running k3s or many containers, events older than
> 30–60 minutes may no longer be available. Journald fills the gap for older container
> activity via the `journald` collector.

## Permissions

`wbts` reads only — it never writes to logs or modifies system state.

```bash
# Check what's accessible with your current user
wbts check-perms

# For full access without sudo, add yourself to these groups:
sudo usermod -aG systemd-journal,adm,docker $USER
# Then log out and back in (or: newgrp docker)
```

## Embedded log level detection

Many services route all output to journald at INFO priority but embed their own severity
in the message body. `wbts` detects and elevates these automatically:

| Pattern | Example | Elevated to |
|---|---|---|
| `level=error` / `"level":"error"` | Docker daemon, Logrus, Zap | `ERRO` |
| ` ERR ` | cloudflared, HashiCorp tools | `ERRO` |
| ` WRN ` | cloudflared | `WARN` |
| `E0506 HH:MM:SS` | Kubernetes / k3s (klog) | `ERRO` |
| `W0506 HH:MM:SS` | Kubernetes / k3s (klog) | `WARN` |
| `[GIN] \| 5xx \|` | Gin HTTP framework (ollama, etc.) | `ERRO` |
| `Forwarded alert: CRITICAL` | Alertmanager bridge | `CRIT` |

## Contributing

See [docs/collectors.md](docs/collectors.md) to learn how to write a collector.
The `pkg/event` package is the stable public API — import it to build a third-party collector.

## License

MIT
