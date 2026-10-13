# MetaDonalds

Search enabled Nyaa or JavDB data sources, inspect an item and its magnet resources, then submit a chosen resource to a PikPak/rclone downloader instance. A small Go core connects compiled source and downloader plugins. No database or background queue: an accepted submission means rclone accepted the command, not that its download finished.

## Setup

Configure a PikPak remote named `pikpak` with `rclone config`. Copy the checked-in
[config.example.toml](config.example.toml) to your local configuration:

```sh
cp config.example.toml config.toml
```

`config.toml` is ignored by Git. It is ready for Compose and uses `pikpak:My Pack`
as the destination. Change it to match your remote and folder. Unknown
configuration keys are rejected.

Nyaa search parameters are configured in `[nyaa]`:

```toml
[nyaa]
base_url = "https://sukebei.nyaa.si/"
f = 0
c = "0_0"
```

`f` is the integer filter parameter and `c` is the category string sent to Nyaa.
Omitting them keeps the defaults above. These settings override any `f` or `c`
in `base_url`; search requests can override either value with `f` and `c`.
`q` comes from the search request and `page` is always `rss`.
Restart MetaDonalds after changing the configuration.


### Plugin instances

The existing `[nyaa]` / `[pikpak]` configuration still works and creates instances
named `nyaa` and `pikpak`. Adding a `[plugins]` table switches entirely to explicit
instances; even an empty table disables the legacy fallback. A complete example is
[config.plugins.example.toml](config.plugins.example.toml):

```toml
[server]
listen_addr = "127.0.0.1:8080"

[plugins]
default_source = "nyaa"          # Optional: target for the legacy search endpoint.
default_downloader = "personal" # Optional: target for the legacy submit endpoint.

[[plugins.sources]]
id = "nyaa"
type = "nyaa"
base_url = "https://nyaa.si/"
f = 0
c = "0_0"

[[plugins.sources]]
id = "catalog"
type = "javdb"
# base_url defaults to https://jdforrepam.com/api

[[plugins.downloaders]]
id = "personal"
type = "pikpak-rclone"
rc_socket = "/tmp/metadonalds/rclone.sock"
remote = "pikpak:My Pack"
```

IDs are globally unique. `enabled = false` disables an instance; enabled defaults
to true. Add another source or downloader entry for another site, remote or folder.
Downloaders sharing a socket reuse one rclone process. Explicit downloader entries
require an absolute `rc_socket`; legacy environment settings only affect legacy
configuration. For search-only operation omit `plugins.downloaders` and
`default_downloader`. `[plugins]` with `sources = []` and `downloaders = []`
explicitly disables all plugins. Invalid types, duplicate IDs, unknown fields and
invalid default references fail startup. A legacy `default_source` must be Nyaa;
the new source endpoints work with every enabled source independently of defaults.

JavDB is an anonymous adapter to the inspected public browser client's protocol,
not a supported or versioned upstream contract. The endpoint, signature protocol
and fields may change. This integration includes search, textual details and
magnets only; no accounts, image decoding/proxy, video resolution or subtitles.
Magnet `size` is displayed with `upstream units` because its unit is not established.
A full upstream search page offers a next cursor; an extra empty last page is possible.
Nyaa RSS has no durable details endpoint: its item identity is the infoHash, and
clients preserve search metadata when opening the resource view.

### Plugin API and extension boundary

| Method | Path | Result |
|---|---|---|
| GET | `/api/v1/plugins` | Enabled instances and declared capabilities/resource types |
| GET | `/api/v1/sources/{source}/search?q=...&cursor=...` | `items`, optional `next_cursor` |
| GET | `/api/v1/sources/{source}/items/{id}` | One `Item` |
| GET | `/api/v1/sources/{source}/items/{id}/resources` | `resources` |
| POST | `/api/v1/downloaders/{downloader}/submissions` | Submission outcome |

Nyaa search also accepts per-request `f` and `c`; other sources reject these
filters. Cursors belong to the source and query that produced them. Submission
body example:

```json
{"resource":{"id":"chosen","kind":"magnet","title":"Chosen resource","info_hash":"0123456789abcdef0123456789abcdef01234567"}}
```

A submission returns `status`, `info_hash` and optionally `message`. `accepted`
is command acceptance, `rejected` is a confirmed refusal, and `unknown` requires
checking the downloader before manually retrying. Invalid resources receive 422
before any downloader request. Transport errors and ambiguous upstream responses
are `unknown`; redirects never replay the RC submission. There is no persisted
job, history, cross-restart deduplication or download-completion tracking.
The legacy `/api/v1/search` and `/api/v1/submit` keep their existing contracts;
without their configured defaults they return 503.

`internal/core` owns content/resource/submission models and source/downloader
capabilities. `internal/plugins/{nyaa,javdb,pikpak}` owns provider protocols;
`internal/backend/handler.go` composes the configured instances. New compiled
plugins add an implementation and configuration/registration, without modifying
generic HTTP routes. Plugins are trusted in-process code, not dynamically loaded
or sandboxed. A future player belongs in the client; future media resolution is
a separate backend capability. No placeholder playback implementation is installed.

### Docker Compose

Create `./rclone` and copy your rclone config into `./rclone/rclone.conf`, then run:

```sh
chmod 600 rclone/rclone.conf
docker compose pull
docker compose up -d --remove-orphans
```

The image includes rclone, which MetaDonalds starts as a child process. The
directory mount lets rclone persist refreshed tokens; the private socket lives
in a temporary filesystem. The API is exposed at `127.0.0.1:8080`. Protect it
with authentication if you expose it through a reverse proxy.

rclone uses `--rc-no-auth` to permit `backend/command` over the private
Unix socket. Keep access to the socket restricted to trusted processes; this
configuration does not expose an RC TCP port. The `rc/noopauth` health check
also checks this authorization gate, unlike `rc/noop`. If you change RC to a
network listener, configure authentication and update the client accordingly.

### Outbound HTTP/HTTPS proxy

MetaDonalds and its rclone child support `HTTP_PROXY`, `HTTPS_PROXY`, and
`NO_PROXY`. Merge the following into the existing Compose configuration,
replacing the example address with a proxy reachable from the container:

```yaml
services:
  metadonalds:
    environment:
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

These settings cover MetaDonalds' Nyaa and JavDB requests and rclone's PikPak requests.
The Unix socket connection between the processes stays local. Downloads performed
in PikPak's cloud do not pass through this proxy. Container environment variables
also do not configure Docker's own GHCR image pulls.

### Local Go process

Use the Go version declared in [go.mod](go.mod) and install rclone. In
`config.toml`, set `server.listen_addr` to `127.0.0.1:8080`, then start the app:

```sh
just build
./bin/metadonalds serve --config config.toml
```

When `pikpak.rc_socket` is omitted, it defaults to
`<OS temporary directory>/metadonalds-<uid>/rclone.sock`. To reuse a particular
socket, set an absolute path in a writable directory. Compose explicitly overrides
this with `/run/rclone/rclone.sock` inside the container.

On startup, the server reuses a healthy rclone at the configured socket or starts
`rclone rcd` from `PATH` and waits up to 10 seconds for its RC API. The child
inherits the environment, including `RCLONE_CONFIG` and proxy settings; without
an override, rclone uses its normal configuration discovery. Socket permissions
are restricted to `0600`. A stale socket is removed only when connecting returns connection refused.
Active listeners, inaccessible paths, and non-socket files are never replaced.

On normal shutdown, MetaDonalds drains HTTP requests before stopping and reaping
its own rclone children. A reused rclone is left running. Each distinct enabled
downloader socket is started once. Startup failure or unexpected child exit is
logged and leaves search available; submissions to that socket may be unavailable.
There is no automatic restart or submission retry. With no enabled downloaders,
the server does not require or launch rclone.
RC readiness does not verify PikPak credentials or remote availability.

Cobra provides one entry point: `metadonalds serve`, `metadonalds tui`, and
`metadonalds web`. Run `metadonalds --help` or a subcommand's `--help`
without a configuration file. Flags use two leading hyphens.

Viper loads TOML with precedence: explicit flags > environment variables >
file values > defaults. Configuration files are selected in this order:

1. Explicit `--config /path/to/config.toml`.
2. `METADONALDS_CONFIG`.
3. `config.toml` in the current working directory.
4. `$XDG_CONFIG_HOME/metadonalds/config.toml`.
5. `metadonalds/config.toml` in each `$XDG_CONFIG_DIRS` directory, in order.

XDG paths use [adrg/xdg](https://github.com/adrg/xdg)'s platform defaults when
unset: on Linux, the user directory is `~/.config` and the system directory is
`/etc/xdg`; on macOS, the user directory is `~/Library/Application Support`,
with additional platform search directories including `~/.config`.
Setting `XDG_CONFIG_HOME` explicitly works on both platforms. For example:

```sh
XDG_CONFIG_HOME="$HOME/.config" ./bin/metadonalds tui
# Reads ~/.config/metadonalds/config.toml when ./config.toml is absent.
```

Only the first matching file is loaded; files are not merged. Explicit paths
never fall back to automatic discovery. An invalid selected file fails startup;
configuration files and directories are not created automatically.

Environment overrides are:

| Variable | Setting |
|---|---|
| `METADONALDS_SERVER_LISTEN_ADDR` | `server.listen_addr` (`serve --listen` overrides it) |
| `METADONALDS_NYAA_BASE_URL` | `nyaa.base_url` |
| `METADONALDS_NYAA_F` | `nyaa.f` |
| `METADONALDS_NYAA_C` | `nyaa.c` |
| `METADONALDS_PIKPAK_RC_SOCKET` | `pikpak.rc_socket` |
| `METADONALDS_PIKPAK_REMOTE` | `pikpak.remote` |
| `METADONALDS_SERVER` | Client server origin (`tui/web --server` overrides it) |

The file is still required when starting a backend. Unknown file keys and
incorrect TOML value types are rejected even when overridden. Both standalone
and automatically started servers drain active requests for up to five seconds
on shutdown; the standalone server handles Ctrl+C and SIGTERM.

## Browser and terminal interfaces

Both interfaces discover enabled sources and downloaders. Select a source,
search, open an item for details/resources, choose a downloader and explicitly
confirm a resource submission. Without a downloader, browsing remains available.
The browser uses the Svelte UI and offers previous/next search pages.
In the terminal, `Ctrl+S` cycles sources and `Ctrl+D` cycles downloaders;
`Enter` opens an item/resource, `n` / `p` changes search pages, and `Esc` returns
from resources to items. `Ctrl+R` retries failed initial capability discovery.
Filters apply only to Nyaa. Accepted resources are deduplicated for the current
client session per downloader instance and normalized infoHash.

Open `http://127.0.0.1:8080/` for the Svelte + TypeScript + Tailwind interface.
It supports keyword search, per-request filter/category overrides, ranked
results, and an explicit confirmation before submitting to PikPak. Blank
filters use the service configuration. API documentation remains at `/docs`.
The production frontend is embedded in the Go binary; it needs no Node runtime
or CDN. The Docker build compiles and embeds it automatically.

The web and terminal commands check `/healthz` at startup. If the service is
already running, they reuse it without reading local configuration. If a local
HTTP service is unavailable, they start the same Go backend inside the client
process, using the configuration discovery order above. No separate server executable
or Go toolchain is needed by the built clients.

Automatic startup binds only to the loopback address and port in `--server`,
overriding `server.listen_addr` for this run without changing the file. It
supports `localhost` and loopback IP addresses. Unavailable remote or HTTPS
origins return an error; an occupied port is never replaced. The client waits
for readiness before opening its interface. Missing or invalid configuration
is reported at startup. Use an absolute `--config` path when launching outside
the project directory.

The client stops only the backend it started when it exits. Existing services
remain running. If a second client reuses the first client's backend, closing
the first also disconnects the second; run `metadonalds serve` or Compose separately
when you need a server with an independent lifetime. An automatically started
backend also starts or reuses rclone for enabled downloader instances as described above. rclone owns PikPak
credentials and token refresh. `/healthz` checks the MetaDonalds HTTP server,
not current rclone or PikPak availability.

Open the web interface in your system's default browser:

```sh
just build
./bin/metadonalds web --config /path/to/config.toml
```

`web` prints the server URL after readiness, then opens it using `open` on macOS,
`xdg-open` on Linux, or `rundll32` on Windows. Browser requests go directly to the
service; there is no native window, proxy or separate desktop build. The command
keeps running until Ctrl+C or SIGTERM. Closing the browser does not stop the
service, and stopping the command does not close the browser. Only a server
started by this command is stopped; a reused service remains running.

If automatic browser opening fails (for example, in a headless environment),
`web` reports the error and actual URL and keeps the service available for manual
access until you stop the command. Unsupported operating systems get the same
manual-open fallback. `web --help` does not load configuration or start a service.

Run the Bubble Tea v2 + Bubbles terminal client:

```sh
./bin/metadonalds tui --config config.toml
```

Use Tab to move between keyword, filter, and category fields; Enter searches.
Use arrow keys to select a result, Enter to review it, and `y` to confirm or
`n`/Esc to cancel. Mouse-enabled terminals also support clicking input fields
and `[ Search ]`, scrolling results with the wheel, clicking a result to review
it, and clicking the confirmation or cancel action. A result click never submits
on its own; actions are disabled while a request is in progress. `/` returns to the search fields. Ctrl+C quits. Use a terminal
at least 60 columns wide and 20 rows tall. The `--server` option on both clients
accepts an HTTP(S) origin without a path, query, or embedded credentials and
defaults to `http://127.0.0.1:8080`.

All interfaces report command acceptance, not download completion. They do not
automatically retry submissions. An interrupted or failed submission can still
have reached PikPak: check there before confirming another attempt. Accepted
hashes are blocked from repeat submission within the current UI session;
this is not durable server-side deduplication.

For frontend development, run the service on port 8080, then:

```sh
bun run --cwd frontend dev
```

Vite proxies `/api/` to the local service. Rebuild the frontend and Go binary
to include changes in the production WebUI.

## Container publishing

The `Docker` GitHub Actions workflow runs frontend and Go tests on Linux and
macOS, checks both build variants, then builds the Dockerfile and publishes
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

Huma generates OpenAPI 3.1 directly from the registered operations and Go types:

- Interactive documentation and request testing: `http://127.0.0.1:8080/docs`
- OpenAPI JSON: `http://127.0.0.1:8080/openapi.json`
- OpenAPI YAML: `http://127.0.0.1:8080/openapi.yaml`

The built-in Scalar page sends requests to the same service. Its JavaScript is
loaded from `unpkg.com`, so the browser needs access to that CDN. The OpenAPI
endpoints themselves do not need internet access. Executing a submit request
from the documentation creates a real PikPak submission.

Errors use Huma's `application/problem+json` format: parameter and schema
validation failures return 422, malformed JSON returns 400, oversized submit
bodies return 413, and upstream failures return 502. JSON responses may include
Huma's `$schema` link. Update clients that relied on the previous plain-text
errors or validation status codes.

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
override them too. Empty or malformed values return HTTP 422: `f` must be a
non-negative integer and `c` must use the `digits_digits` format. Overrides
do not change the configuration or affect subsequent requests.

Choose an `info_hash` from the results and submit that exact hash:

```sh
curl -X POST http://127.0.0.1:8080/api/v1/submit \
  -H 'Content-Type: application/json' \
  -d '{"info_hash":"0123456789abcdef0123456789abcdef01234567"}'
```

`POST /api/v1/submit` accepts only a 40-character hexadecimal infoHash, rejects
unknown fields, and limits the body to 1024 bytes. A successful response has
`status: "submitted"` and the lowercase `info_hash`. `GET /healthz` returns
plain text `ok`. An HTTP timeout can leave submission status uncertain; check
PikPak before retrying.

## Logs

The standalone MetaDonalds server writes JSON logs to stderr with timestamps
and severity levels. An automatically started backend writes the same logs to
a private `metadonalds-server-*.log` file in the OS temporary directory; the
client prints its path at startup. This avoids disrupting terminal rendering.
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
docker compose logs -f --tail=100 metadonalds
```

For an existing deployment using the old `app` service name, run
`docker compose up -d --remove-orphans` to replace it with `metadonalds`.
Source changes to logging require a rebuilt image; the published `latest` image
includes them only after the publishing workflow completes.

## Development

Build frontend assets before the local Go suite (also required on a fresh checkout):

```sh
just test
```

Requires `just`, Bun 1.4.2, and the Go version in `go.mod`. `just build` produces
`bin/metadonalds` with `serve`, `web`, and `tui`. The build embeds the
frontend first. `just test` also runs the Go race detector.

The [justfile](justfile) is the shared task entry point for local development
and CI. Install `just` with your package manager (on macOS: `brew install just`):

```sh
just           # List tasks
just build     # Server, browser launcher, and terminal executable
just frontend  # Frontend dependencies, checks, and embedded assets
just test      # Frontend tests and Go race tests
```

Frontend installs use the committed `frontend/bun.lock` with `--frozen-lockfile`.
The frontend build includes Svelte/TypeScript checks and tests run with `bun test`.
TypeScript stays on version 6 because the current Svelte checker requires an
experimental dual-version setup for TypeScript 7. Tests use fake HTTP responses and
temporary files; they need no running services or account credentials.
Repository-specific agent guidance is in [AGENTS.md](AGENTS.md).
