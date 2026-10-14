# Working on MetaDonalds

MetaDonalds is one Go service with compiled Nyaa/JavDB source plugins and a
PikPak/rclone downloader. It has no download job database or submission queue;
WebDAV directory caching has background refresh and optional disk persistence.

## Read as needed

- Setup and user-facing behavior: [README.md](README.md) routes to topic guides.
- HTTP contracts: `internal/backend/api.go`, `internal/backend/plugins_api.go`;
  shared models and capabilities: `internal/core`; plugin assembly:
  `internal/backend/handler.go`. Provider protocols live in `internal/plugins`.
- WebDAV paths/media: `internal/backend/dav*.go`; directory caching and persistence:
  `internal/backend/dav_flat*.go`; operational behavior: [docs/webdav.md](docs/webdav.md).
- Commands and service ownership: `cmd/metadonalds`, `internal/app`;
  browser launching: `internal/web`; clients: `internal/tui`, `frontend/src`.
- Configuration: `internal/app/config.go`, `internal/backend/config.go`,
  [config.example.toml](config.example.toml). Build/release: `justfile`,
  `compose.yaml`, `Dockerfile`, `.github/workflows/docker.yml`.

## Invariants and validation

Submission acceptance is not download completion. A timeout may leave the outcome
unknown; automatic retries can create duplicates. rclone owns credentials and
refreshes tokens; the RC client connects through a private Unix socket.

Only `tui` reuses a healthy service; `serve` owns its listener. Browser lifetime
is independent of the service. DAV browsing never submits downloader jobs.

`just test` builds frontend assets, runs frontend tests and `go test -race ./...`.
Tests use local HTTP fakes and temporary files without live provider access.
WebDAV integration tests use real rclone against fakes; CI requires rclone, while
local runs skip those tests if it is absent. Run affected checks, fix failures
caused by the change, and rerun without approval at each step. Documentation-only
changes need accuracy/link checks rather than the Go suite.

Complete the requested work through relevant validation and report remaining
blockers. Live submissions and image publishing belong only to tasks that request
those actions.
