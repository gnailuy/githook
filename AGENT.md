# Agent Instructions

- Read `.aidoc/INDEX.md` before changing runtime behavior.
- Preserve the queue-service/worker privilege boundary: the loopback queue service owns only the webhook secret and embedded queue; the worker owns only GitHub artifact-read and release activation authority.
- Treat webhook payloads as notifications. Re-read deployment facts from GitHub before downloading or activating anything.
- Select each source and webhook secret from an exact route before parsing payload JSON; never use an unverified repository field to choose a secret or target.
- Keep target adapters confined to their configured release roots and state; a Sudoku failure must never change another target pointer or files.
- Keep the configured listener and every queue maintenance endpoint loopback-only; an external reverse proxy may forward only the exact webhook path. Keep secrets out of source, arguments, logs, fixtures, and documentation.
- Run `go test ./...`, `go vet ./...`, and `go build ./cmd/githook` before opening a pull request.
- Update `.aidoc/` whenever architecture, security invariants, or operator workflows change.
