# MetaDonalds

Search Nyaa RSS and submit a chosen torrent infoHash to PikPak. No database or background queue: a successful submission means PikPak accepted the magnet, not that its download finished.

## Setup

Configure a PikPak remote named `pikpak` with `rclone config`. The checked-in
[config.toml](config.toml) is ready for Compose and uses `pikpak:My Pack` as the
destination. Change it to match your remote and folder. Unknown configuration
keys are rejected.

### Docker Compose

Create `./rclone` and copy your rclone config into `./rclone/rclone.conf`, then run:

```sh
chmod 600 rclone/rclone.conf
docker compose up --build
```

The directory mount lets rclone persist refreshed tokens. The app and rclone
sidecar share a Unix socket; the API is exposed at `127.0.0.1:8080`. Protect it
with authentication if you expose it through a reverse proxy.

### Local Go process

Use the Go version declared in [go.mod](go.mod) and install rclone. In
`config.toml`, set `server.listen_addr` to `127.0.0.1:8080` and
`pikpak.rc_socket` to an absolute path in a writable directory. Start rclone
with that same socket path:

```sh
rclone rcd --rc-addr unix:///path/to/rclone.sock
```

In another terminal, start the app:

```sh
go run . -config config.toml
```

## Container publishing

The `Docker` GitHub Actions workflow builds the existing Dockerfile and publishes
`ghcr.io/fdkevin0/metadonalds` using the repository's automatic `GITHUB_TOKEN`;
no additional registry secrets are needed. Pushes to `main` publish `latest`,
`main`, and a `sha-<commit>` tag. Pushing a `v*` tag (for example, `v1.0.0`)
publishes that exact tag and a commit tag. Pull requests targeting `main` build
without publishing. You can also run the workflow manually from the Actions tab.

After the first successful publish:

```sh
docker pull ghcr.io/fdkevin0/metadonalds:latest
```

To use it with Compose, replace the app's `build: .` with
`image: ghcr.io/fdkevin0/metadonalds:latest`, keeping the existing config and socket
mounts. For anonymous pulls, set the package visibility to public in GitHub's
package settings; private packages require authentication to `ghcr.io`.

## API

Search Nyaa's English-translated anime RSS feed (no pagination), sorted by a
score based on seeders, trusted status, and remake status. The trimmed query
must be 1–200 bytes; the response is `{"results":[...]}`:

```sh
curl 'http://127.0.0.1:8080/api/v1/search?q=Frieren'
```

Choose an `info_hash` from the results and submit that exact hash:

```sh
curl -X POST http://127.0.0.1:8080/api/v1/submit \
  -H 'Content-Type: application/json' \
  -d '{"info_hash":"0123456789abcdef0123456789abcdef01234567"}'
```

`POST /api/v1/submit` accepts only a 40-character hexadecimal infoHash. On success it returns `{"status":"submitted","info_hash":"..."}`. `GET /healthz` returns `ok`. An HTTP timeout can leave submission status uncertain; check PikPak before retrying.

## Development

Run `go test ./...` for the local suite. Tests use fake HTTP responses and
temporary files; they need no running services or account credentials.
Repository-specific agent guidance is in [AGENTS.md](AGENTS.md).
