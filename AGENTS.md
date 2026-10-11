# Working on MetaDonalds

MetaDonalds is a single Go service that searches Nyaa RSS and submits a chosen
infoHash to PikPak through rclone. It has no database or background queue.

## Read as needed

- For setup and API usage, use [README.md](README.md).
- For HTTP contracts, use `internal/backend/api.go`; for ranking and RSS parsing,
  use `internal/backend/nyaa.go`.
- For PikPak submission, use `internal/backend/pikpak.go`. The rclone RC client connects over a
  Unix socket, and rclone owns credentials and token refresh.
- For commands, use `cmd/metadonalds`; for startup and shutdown, use `internal/app`.
- For configuration loading, use `internal/app/config.go`; for validation, use
  `internal/backend/config.go`. The example file is `config.example.toml`. For container or release
  changes, use `compose.yaml`, `Dockerfile`, and `.github/workflows/docker.yml`.

## Behavior and validation

A successful submission means rclone accepted the command, not that a download
finished. A timeout can leave the outcome uncertain; do not add automatic
submission retries without accounting for duplicate requests.

`just test` builds frontend assets and runs frontend tests and `go test -race ./...`.
Go tests use in-memory HTTP fakes and temporary files, with no live Nyaa,
rclone, or PikPak access. Run relevant tests, fix failures caused by the change,
and rerun them without seeking approval at each step. Documentation-only edits
normally need link and accuracy checks, not the Go suite.

Carry the requested change through relevant validation and report any remaining
blocker. Local tests do not require live submissions or image publishing; perform
those external actions only when they are part of the user's requested scope.
