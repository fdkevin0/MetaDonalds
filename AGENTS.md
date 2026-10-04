# Working on MetaDonalds

MetaDonalds is a single Go service that searches Nyaa RSS and submits a chosen
infoHash to PikPak through rclone. It has no database or background queue.

## Read as needed

- For setup and API usage, use [README.md](README.md).
- For HTTP contracts, use `api.go`; for ranking and RSS parsing, use `nyaa.go`.
- For PikPak submission, use `pikpak.go`. The rclone RC client connects over a
  Unix socket, and rclone owns credentials and token refresh.
- For configuration, use `config.go` and `config.toml`; for container or release
  changes, use `compose.yaml`, `Dockerfile`, and `.github/workflows/docker.yml`.

## Behavior and validation

A successful submission means rclone accepted the command, not that a download
finished. A timeout can leave the outcome uncertain; do not add automatic
submission retries without accounting for duplicate requests.

`go test ./...` uses in-memory HTTP fakes and temporary files, with no live Nyaa,
rclone, or PikPak access. Run relevant tests, fix failures caused by the change,
and rerun them without seeking approval at each step. Documentation-only edits
normally need link and accuracy checks, not the Go suite.

Carry the requested change through relevant validation and report any remaining
blocker. Local tests do not require live submissions or image publishing; perform
those external actions only when they are part of the user's requested scope.
