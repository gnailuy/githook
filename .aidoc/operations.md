---
domain: Workflows
status: Active
entry_points:
  - cmd/githook/main.go
  - packaging/systemd/githook-receiver.service
  - packaging/systemd/githook-worker.service
dependencies:
  - .aidoc/architecture.md
---

# Operations

Githook day-two operations cover health checks, queue maintenance, credential rotation, replay, and recovery for an existing installation. Use Host Bootstrap—not this document—to install Githook on an empty host.

## Related Docs

| Document | Relationship |
|---|---|
| [Architecture](architecture.md) | Security and serialization boundaries the host configuration preserves |
| [Host Bootstrap](host-bootstrap.md) | Canonical empty-host installation and acceptance procedure |
| [INDEX](INDEX.md) | Documentation discovery |

## Why Runtime Files Stay User-Owned

The deployment host is an artifact consumer rather than a source checkout or build host. Installation copies a reviewed binary and user-level service definitions onto the host, while GitHub Actions remains the build environment for release artifacts. Root-owned storage is reserved for credentials; giving ordinary state, units, or deployed content root ownership would add privilege without protecting a secret.

The queue service has no application authentication because it listens only on loopback. A separately managed reverse proxy exposes exactly the configured webhook path; every maintenance path remains reachable only by a local operator. Githook accepts forwarded requests independently of the proxy implementation and does not own proxy configuration. Exposing the whole listener would violate the security model.

## Runtime Ownership

`~/.config/githook/runtime.conf` owns legacy non-secret deployment policy or points to the user-owned multi-target JSON. `packaging/config/targets.json.example` documents source routes, adapter kinds, credential environment names, and per-target release roots. The queue database defaults to `~/.githook/githook.db`; release directories and the `current` symlink default to `~/.local/share/githook/`. `OpenQueue` creates the private queue directory when needed. Repository ignore rules reject common SQLite database, journal, and WAL filenames, so runtime queue state cannot be added accidentally. A static-file server may read the configured release tree but does not belong to Githook and needs no write access.

`/etc/githook/receiver.conf` contains only webhook-secret variables; `/etc/githook/worker.conf` contains only GitHub token variables. Legacy installations use `GITHOOK_WEBHOOK_SECRET` and `GITHUB_TOKEN`; multi-target installations name one secret per source and may use distinct repository-scoped tokens. Each unit reads only its own credential file. Host Bootstrap is the canonical source for creating these files and installing the binary and user units.

Host-specific path permissions belong in user-level systemd drop-ins, not the credential files.

The external reverse proxy forwards only `POST` on each configured exact webhook path to the loopback listener. The proxy may be Caddy, Nginx, IIS, or another HTTP proxy; its configuration belongs to the consuming deployment, not this project. The proxy must not expose maintenance paths or receive write access to the queue, release directories, or credential files.

## Configuration Ownership

`cmd/githook.main` preserves host-neutral single-target defaults and activates multi-target mode only when `GITHOOK_TARGETS_FILE` is set. `MultiConfig.Validate` rejects duplicate paths, duplicate identities, unknown targets, incomplete adapter source sets, or secret values hidden in undeclared fields. `~/.config/githook/runtime.conf` owns non-secret deployment policy, while `/etc/githook/*.conf` contains only credentials. This keeps credential files from becoming duplicate application configuration.

`Worker.Run` is the source of truth for claim recovery, retry classification, and retry timing; `Queue` is the source of truth for serialization and retained failure state.

## Daily Maintenance Order

1. Check the receiver, worker, and reconciliation timer state, then confirm the configured public application's health endpoint still serves the active release.
2. Inspect the queue only when service state or release freshness indicates a problem. Use the local maintenance API defined by `Service.ServeHTTP`; do not expose it through the reverse proxy.
3. Follow **How to Recover** for replay or reconciliation. Follow Host Bootstrap only when the installation itself is missing or cannot be trusted.

## How to Inspect and Edit Pending Work

The loopback queue service provides local maintenance operations for listing, peeking, dropping, or coalescing queued work. `Service.ServeHTTP` is the source of truth for the methods and paths; queue deletion never removes the request currently being processed.

Stop the worker before maintenance when an operator needs a stable pending set. Restarting the worker requeues a request left in `processing`.

## How to Recover

`githook deploy-run --source <source-id> --sha <full-sha> <run-id>` performs the same authoritative verification and deployment path for manual replay. Reconciliation can independently deploy a newer eligible run without deleting failed evidence.

Rotate the webhook secret by installing the new receiver credential before updating the one existing GitHub webhook, then prove a signed `ping`. Rotate the GitHub token independently because the queue service never uses it.

Retain previous immutable release directories until a newer release survives smoke and restart checks. A failed smoke check restores the previous active symlink; host recovery may also repoint `current` to a retained release.

Use [Host Bootstrap](host-bootstrap.md) when no working installation exists. Host Bootstrap separates the repository-owned procedure from environment-specific network, path, and credential values.
