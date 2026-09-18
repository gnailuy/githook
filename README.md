# Githook

Githook receives signed GitHub Actions notifications through a loopback listener and deploys verified immutable artifacts to a configured release directory. It was created for `gnailuy.com`, but the receiver, queue, worker, and deployment contracts are host- and proxy-independent. One installation can preserve the legacy static-site profile or route multiple repositories to isolated typed adapters, including a verified paired backend/frontend Sudoku release.

See [`.aidoc/INDEX.md`](.aidoc/INDEX.md) for architecture, installation, and operations.

## Development

Requires Go 1.25 or newer. Run `go test ./...`, `go vet ./...`, and `go build ./cmd/githook` before review.
