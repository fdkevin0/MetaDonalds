# MetaDonalds

Search Nyaa RSS and submit a chosen torrent infoHash to PikPak. No database or background queue: a successful submission means PikPak accepted the magnet, not that its download finished.

## Setup

Configure a PikPak remote named `pikpak` with `rclone config`. The checked-in
[config.toml](config.toml) is ready for Compose and uses `pikpak:My Pack` as the
destination. Change it to match your remote and folder. Unknown configuration
keys are rejected.

Nyaa search parameters are configured in `[nyaa]`:

```toml
[nyaa]
base_url = "https://nyaa.si/"
f = 0
c = "0_0"
```

`f` is the integer filter parameter and `c` is the category string sent to Nyaa.
Omitting them keeps the defaults above. These settings override any `f` or `c`
in `base_url`; search requests can override either value with `f` and `c`.
`q` comes from the search request and `page` is always `rss`.
Restart MetaDonalds after changing the configuration.

### Docker Compose

Create `./rclone` and copy your rclone config into `./rclone/rclone.conf`, then run:

```sh
chmod 600 rclone/rclone.conf
docker compose pull
docker compose up -d
```

The directory mount lets rclone persist refreshed tokens. The app and rclone
sidecar share a Unix socket; the API is exposed at `127.0.0.1:8080`. Protect it
with authentication if you expose it through a reverse proxy.

rclone uses `--rc-no-auth` to permit `backend/command` over the private shared
Unix socket. Keep access to the socket restricted to trusted processes; this
configuration does not expose an RC TCP port. The `rc/noopauth` health check
also checks this authorization gate, unlike `rc/noop`. If you change RC to a
network listener, configure authentication and update the client accordingly.

### Outbound HTTP/HTTPS proxy

Both services support `HTTP_PROXY`, `HTTPS_PROXY`, and `NO_PROXY`. Merge the
following into their existing Compose configuration, replacing the example
address with a proxy reachable from the containers:

```yaml
services:
  metadonalds:
    environment:
      HTTP_PROXY: "http://192.168.1.10:7890"
      HTTPS_PROXY: "http://192.168.1.10:7890"
      NO_PROXY: "localhost,127.0.0.1,::1"
  rclone:
    environment:
      RCLONE_CONFIG: /config/rclone/rclone.conf
      HTTP_PROXY: "http://192.168.1.10:7890"
      HTTPS_PROXY: "http://192.168.1.10:7890"
      NO_PROXY: "localhost,127.0.0.1,::1"
```

`HTTPS_PROXY` selects the proxy for HTTPS destinations; its URL scheme describes
the connection to the proxy itself, so `http://` is common. See the
[rclone proxy documentation](https://rclone.org/faq/#can-i-use-rclone-with-an-http-proxy)
and [Go HTTP transport documentation](https://pkg.go.dev/net/http#DefaultTransport).
If the proxy runs on the host, use a host address reachable from the containers
and have it listen on that interface; `127.0.0.1` inside a container refers to
the container itself. Apply changes with `docker compose up -d`.

These settings cover MetaDonalds' Nyaa requests and rclone's PikPak requests.
The Unix socket connection between the services stays local. Downloads performed
in PikPak's cloud do not pass through this proxy. Container environment variables
also do not configure Docker's own GHCR image pulls.

### Local Go process

Use the Go version declared in [go.mod](go.mod) and install rclone. In
`config.toml`, set `server.listen_addr` to `127.0.0.1:8080` and
`pikpak.rc_socket` to an absolute path in a writable directory. Start rclone
with that same socket path:

```sh
rclone rcd --rc-addr unix:///path/to/rclone.sock --rc-no-auth
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

Compose uses `ghcr.io/fdkevin0/metadonalds:latest` by default. To build locally,
replace the `metadonalds` service's `image` with `build: .` and run
`docker compose up --build`.
For anonymous pulls, set the package visibility to public in GitHub's
package settings; private packages require authentication to `ghcr.io`.

## API

Search Nyaa's RSS feed using the configured filter and category (default:
all categories, no pagination), sorted by a
score based on seeders, trusted status, and remake status. The trimmed query
must be 1–200 bytes; the response is `{"results":[...]}`:

```sh
curl 'http://127.0.0.1:8080/api/v1/search?q=Frieren'
```

Override the configured filter and category for one request:

```sh
curl 'http://127.0.0.1:8080/api/v1/search?q=Frieren&f=2&c=1_2'
```

Omitted parameters use the configured values. Explicit `f=0` and `c=0_0`
override them too. Empty or malformed values return HTTP 400: `f` must be a
non-negative integer and `c` must use the `digits_digits` format. Overrides
do not change the configuration or affect subsequent requests.

Choose an `info_hash` from the results and submit that exact hash:

```sh
curl -X POST http://127.0.0.1:8080/api/v1/submit \
  -H 'Content-Type: application/json' \
  -d '{"info_hash":"0123456789abcdef0123456789abcdef01234567"}'
```

`POST /api/v1/submit` accepts only a 40-character hexadecimal infoHash. On success it returns `{"status":"submitted","info_hash":"..."}`. `GET /healthz` returns `ok`. An HTTP timeout can leave submission status uncertain; check PikPak before retrying.

## Logs

MetaDonalds writes JSON logs to stderr with timestamps and severity levels.
Startup and fatal errors are logged, and each completed HTTP request records
its method, matched route, status, and duration in milliseconds. HTTP 4xx
responses use WARN; 5xx responses use ERROR. Unmatched routes have an empty
route field. Separate operation logs record validated search keywords (`query`)
at start and completion or failure, plus `result_count` on success. Download
submission logs record the normalized `info_hash` at start and acceptance or
failure. Acceptance means the command was accepted, not that the cloud download
finished; a failed or timed-out submission can still have reached PikPak.
Logs omit full request URLs, bodies, headers, and raw upstream errors.
A 502 on the search route
indicates a Nyaa failure; a 502 on submit indicates an rclone/PikPak failure.

```sh
docker compose logs -f --tail=100 metadonalds rclone
```

For an existing deployment using the old `app` service name, run
`docker compose up -d --remove-orphans` to replace it with `metadonalds`.
Source changes to logging require a rebuilt image; the published `latest` image
includes them only after the publishing workflow completes.

## Development

Run `go test ./...` for the local suite. Tests use fake HTTP responses and
temporary files; they need no running services or account credentials.
Repository-specific agent guidance is in [AGENTS.md](AGENTS.md).
