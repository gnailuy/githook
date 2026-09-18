---
domain: Architecture
status: Active
entry_points:
  - internal/githook/service.go
  - internal/githook/worker.go
  - internal/githook/deploy.go
dependencies: []
---

# Architecture

Githook turns signed `workflow_run` notifications into verified immutable releases without trusting webhook claims or building source on the deployment host. One loopback queue service and one globally serial worker can serve one legacy static target or multiple isolated source/target adapters while preserving the public-input/deployment-authority boundary.

## Related Docs

| Document | Relationship |
|---|---|
| [Operations](operations.md) | Host installation, queue maintenance, and recovery workflow |
| [INDEX](INDEX.md) | Documentation discovery |

## Why Githook Uses Two Daemons

The queue service processes attacker-controlled Internet requests forwarded by one exact reverse-proxy route. The deployment worker reads a GitHub token and changes the active release. Separate units load different credential files and receive different writable paths, so the public-facing process does not inherit deployment configuration and the deployment process does not become a network service. When both units run under one service account, this is process and sandbox separation rather than a separate Unix-identity boundary.

The embedded SQLite file gives the low-volume queue durable transactions without Redis or a database server. SQLite remains an implementation detail owned by the queue service and worker; no separate database lifecycle exists.

The webhook is only a wake-up signal because signed payloads can be valid yet stale, duplicated, or delivered out of order. `Worker.ProcessRun` and `GitHubPairResolver.Resolve` re-read run and artifact facts from GitHub before deployment.

## What the Queue Service Owns

`Service.ServeHTTP` exposes exact configured source routes and local maintenance paths on one loopback listener. Each route selects its source identity and HMAC secret before payload parsing; an unverified repository field never chooses a secret or target. The host's reverse proxy forwards only configured webhook routes, so maintenance operations never cross the public routing boundary. Githook does not own or configure that proxy.

`Receiver.ServeHTTP` verifies the raw-body HMAC-SHA256 signature before JSON parsing, enforces the request envelope, and deduplicates delivery and run identifiers. Every webhook response body is the same dummy value, `42`, while HTTP status codes retain operational meaning.

`Queue.Claim` permits only one `processing` record across all targets. Each durable job carries source, target, repository, workflow run, and head commit identities. `AdapterRegistry.Process` rejects unregistered source/target combinations, while target-specific deployment state prevents an older run from replacing that target without coupling one target's pointer to another.

## What the Worker Owns

`StaticAdapter.Process` preserves the original single-static-target contract: `Worker.ProcessRun` accepts only configured authoritative metadata and one matching artifact, `VerifyBundle` checks both checksums and archive safety, and `Deployer.Deploy` atomically activates the release. Legacy environment configuration remains supported as an implicit static target.

`SudokuAdapter.Process` asks `GitHubPairResolver.Resolve` for the triggered trusted component plus the newest eligible successful component from the other repository. The resolver verifies GitHub digests, schema-versioned manifests, repository/workflow/run/commit identity, complete file inventories, checksums, and path safety. `SudokuDirectoryActivator.Activate` stages one backend/frontend pair, changes only the configured Sudoku pointer, and restarts one validated user-service name without invoking configurable shell text. A restart or smoke failure restores the prior Sudoku pointer and restarts the same bounded service; fixture tests prove a Sudoku failure leaves a neighboring pointer unchanged.

`Deployer.Deploy` activates a complete release with an atomic symlink replacement. Post-activation smoke failure restores the previous link rather than leaving a partly trusted release active.

`Worker.Run` separates permanent validation failures from transient operational failures so invalid input is not retried forever while temporary outages can recover. The executable retry policy remains canonical in `Worker.Run` and its tests.

## Invariants

- The queue service MUST NOT receive a GitHub API token or release-directory write access.
- An exact webhook route MUST select the source and secret before untrusted JSON is parsed.
- A source MUST dispatch only to its registered target adapter.
- The worker MUST NOT receive the webhook secret or expose an HTTP listener.
- The configured listener and all maintenance endpoints MUST remain loopback-only; an external reverse proxy MUST forward only the exact webhook path.
- A webhook payload MUST NOT directly authorize a deployment.
- At most one queue record may have `processing` status.
- Queue maintenance MUST NOT delete the request currently being processed.
- Permanent failures and exhausted transient retries MUST remain inspectable as `failed` queue records rather than loop forever or disappear.
- Release archives MUST contain only relative regular files and directories; links and traversal paths are rejected.
- A release directory MUST be immutable after creation, and activation MUST use one atomic link replacement.
- Duplicate delivery IDs, duplicate run IDs, and older releases MUST NOT replace newer per-target deployed state.
- A Sudoku activation MUST NOT write another target's release tree, pointer, service, or files.
