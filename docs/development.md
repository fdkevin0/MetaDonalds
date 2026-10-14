# Development and publishing

[Project overview](../README.md)

Build frontend assets before the local Go suite (also required on a fresh checkout):

```sh
just test
```

Requires `just`, Bun 1.4.2, and the Go version in [go.mod](../go.mod). `just build` produces
`bin/metadonalds` with `serve` (including `--open`) and `tui`. The build embeds the
frontend first. `just test` also runs the Go race detector.

Use `metadonalds --version` to print the version without loading configuration or
starting services. Without an injected version, it uses Go module build information
when available (including a generated development version), otherwise `dev`.
This also applies to `go install`. Release builds can inject a version:

```sh
go build -ldflags="-X main.version=v1.2.3" -o bin/metadonalds ./cmd/metadonalds
# Or install using the same flag:
go install -ldflags="-X main.version=v1.2.3" ./cmd/metadonalds
```

Build frontend assets first (for example, `just frontend`). Docker accepts
`--build-arg VERSION=v1.2.3`; CI injects the Git tag for tag builds and the commit
SHA for other image builds. Local Docker builds default to `dev`.

The [justfile](../justfile) is the shared task entry point for local development
and CI. Install `just` with your package manager (on macOS: `brew install just`):

```sh
just           # List tasks
just build     # Server, browser launcher, and terminal executable
just install   # Install metadonalds with go install
just frontend  # Frontend dependencies, checks, and embedded assets
just test      # Frontend tests and Go race tests
```

Frontend installs use the committed `frontend/bun.lock` with `--frozen-lockfile`.
The frontend build includes Svelte/TypeScript checks and tests run with `bun test`.
Frontend tool versions are pinned in [package.json](../frontend/package.json).
Tests use fake HTTP responses and temporary files; they need no running services
or account credentials. Local WebDAV integration tests require rclone and skip
when it is absent; CI requires them to run.
Repository-specific agent guidance is in [AGENTS.md](../AGENTS.md).

## Frontend development

For frontend development, run the service on port 8080, then:

```sh
bun run --cwd frontend dev
```

Vite proxies `/api/` to the local service. Rebuild the frontend and Go binary
to include changes in the production WebUI.

## Container publishing

The `Docker` GitHub Actions workflow runs frontend and Go tests on Linux,
installs the Docker-pinned rclone for mandatory local WebDAV integration
tests, builds the executable, then builds the Dockerfile and publishes
`ghcr.io/fdkevin0/metadonalds` using the repository's automatic `GITHUB_TOKEN`;
no additional registry secrets are needed. Pushes to `main` publish `latest`,
`main`, and a `sha-<commit>` tag. Pushing a `v*` tag (for example, `v1.0.0`)
publishes that exact tag and a commit tag. Pull requests targeting `main` build
without publishing. You can also run the workflow manually from the Actions tab.

After the first successful publish:

```sh
docker pull ghcr.io/fdkevin0/metadonalds:latest
```

Compose uses `ghcr.io/fdkevin0/metadonalds:latest` by default. To build locally,
replace the `metadonalds` service's `image` with `build: .` and run
`docker compose up --build`.
For anonymous pulls, set the package visibility to public in GitHub's
package settings; private packages require authentication to `ghcr.io`.
