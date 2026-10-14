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

### Plugin instances

Configure sources and downloaders as explicit instances in
[config.example.toml](config.example.toml):

```toml
[server]
listen_addr = "127.0.0.1:8080"

[[plugins.sources]]
id = "nyaa"
type = "nyaa"
base_url = "https://sukebei.nyaa.si/"
f = 0
c = "0_0"

[[plugins.sources]]
id = "catalog"
type = "javdb"
# base_url defaults to https://jdforrepam.com/api.

[[plugins.downloaders]]
id = "pikpak"
type = "pikpak-rclone"
remote = "pikpak:My Pack"
# Optional absolute rc_socket; omitted uses a private per-user temporary directory.
```

Only listed instances are enabled. IDs are globally unique; `enabled = false`
disables an instance, and enabled defaults to true. Add entries for another site,
remote or folder. Downloaders sharing a socket reuse one rclone process. For
search-only operation omit `plugins.downloaders`; an empty `[plugins]` table
selects no instances. Unknown fields, invalid types, duplicate IDs, invalid URLs,
filters, categories and socket paths fail startup.

Nyaa uses `f = 0` and `c = "0_0"` when omitted. Search requests can override either
value; `q` comes from the request and `page` is always `rss`. Nyaa base URLs must
not contain query parameters. Restart MetaDonalds after configuration changes.

JavDB is an anonymous adapter to the inspected public browser client's protocol,
not a supported or versioned upstream contract. The endpoint, signature protocol
and fields may change. Its API integration includes search, textual details and
magnets; the read-only WebDAV view below also serves online pictures and video.
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

`internal/core` owns content/resource/submission models and source/downloader
capabilities. `internal/plugins/{nyaa,javdb,pikpak}` owns provider protocols;
`internal/backend/handler.go` composes the configured instances. New compiled
plugins add an implementation and configuration/registration, without modifying
generic HTTP routes. Plugins are trusted in-process code, not dynamically loaded
or sandboxed. The WebDAV view uses the enabled JavDB source's media details and
resolves online video separately from downloader resources.

### Read-only WebDAV for online JavDB media

With an enabled JavDB source, connect a WebDAV file browser to
`http://127.0.0.1:8080/dav/` (substitute the service's reachable address and
port). In macOS Finder use **Go → Connect to Server** (`Cmd+K`) and enter that
URL. Finder can browse and copy files; playback of `.m3u8` files needs a player
that understands HLS and its relative segment paths. Finder itself was not
tested on macOS. If you listen on a LAN address, this same unauthenticated,
read-only DAV endpoint is reachable by other devices on that LAN; use a trusted
network or an authenticated reverse proxy for broader exposure.

The default `catalog` source appears at `/dav/catalog/` (explicit JavDB instance
IDs appear under their configured names):

```text
/dav/catalog/latest/0001-SSIS-006/
/dav/catalog/search/SSIS/0001-SSIS-006/
/dav/catalog/categories/censored/0001-SSIS-006/
/dav/catalog/categories/censored/tags/<group-name [ID]>/<tag-name [ID]>/0001-SSIS-006/
/dav/catalog/rankings/playback/all/daily/0001-SSIS-006/
/dav/catalog/rankings/top250/all/0001-SSIS-006/
/dav/catalog/actors/censored/0001-<actor-name [ID]>/0001-SSIS-006/
/dav/catalog/series/censored/0001-<series-name [ID]>/0001-SSIS-006/
/dav/catalog/makers/censored/0001-<maker-name [ID]>/0001-SSIS-006/
```

`latest/` combines up to 10 upstream pages and `search/<query>/` combines up
to 99 into a single directory. Open the query folder directly; URL-encode the
query as one path segment, for example `search/SSIS-006/` or
`search/%E6%A4%9C%E7%B4%A2/`. `search/README.txt` describes this on the
mount. Movie folders are numbered in upstream order, such as `0001-SSIS-006/`.
They use the JavDB code (番号), falling back to its ID if the code is missing,
unsafe, duplicated in the listing, or clashes with another item's ID. The old
page folders, `items/<ID>/` route, and unnumbered ID aliases are unavailable.
Categories contain four
types (`censored`, `uncensored`, `western`, `fc2`) and browsable tag groups.
Rankings contain `playback` and `score` periods (`daily`, `weekly`, `monthly`)
plus the public TOP250 snapshot. Playback offers `all` and `high_score`
filters; score and TOP250 use movie types. Category and tag folders combine up
to 99 pages; TOP250 combines up to five. Actor, series, and maker directories
list numbered named folders and expose each entity's movies directly. These browse folders read online
data and may be empty or unavailable when the public upstream is unavailable.
An item contains `metadata.json`, `cover.jpg`, `images/` when available,
`preview.m3u8` when JavDB supplies a preview, `actors/` when the movie lists
actors, and `stream/` for online video resolution. Each
`actors/<actor-name [ID]>/` folder contains `metadata.json` with the public
actor profile (including upstream null fields) and `avatar.jpg` when the movie
supplies an avatar. Actor folders use the same safe naming as the browse tree.
Actor profiles load only when their metadata or folder contents are requested;
an unavailable profile does not prevent browsing the movie or playing its preview.
`stream/` resolves through the reference site's public
`/api/v/resolve` endpoint when opened; a failed or unavailable resolution gives
an error there while metadata, images and previews remain browsable. A resolved
HLS source is a real `.m3u8` playlist with local relative links to playlists,
segments, keys and maps; a source shown as `.mp4` is exposed only when the
upstream supplies an actual MP4 with a known length. No magnet is treated as a
video file. Browsing DAV never submits to PikPak or downloads a magnet.

All DAV media bytes pass through MetaDonalds: the server fetches and decodes
pictures, rewrites HLS playlists to local DAV links, and proxies video, segments,
keys, and maps. DAV clients connect to MetaDonalds rather than fetching media
directly from the external providers; playback therefore uses the server's
incoming and outgoing bandwidth. Actor photos use the same image path.
Pictures are decoded from JavDB's public image encoding before serving. Image
property listings retain content checksums instead of all decoded image bodies.
Media-folder listings probe segment sizes with at most eight concurrent requests
under the existing 30-second deadline. Media links are short-lived upstream URLs, so reopen a playlist if a segment expires;
generated local links stay in memory for up to four hours after their last use
and are lost on service restart. MetaDonalds does not store complete videos or
transcode HLS. An unavailable upstream returns an error instead of an empty
file. The reference site's public resolver is an unversioned dependency; full
video availability can change independently of JavDB's `can_play` flag.
Successful JavDB API JSON responses and TOP250 snapshots are cached in memory
for five minutes (at most 128 responses and 16 MiB total); errors and invalid
responses are not cached. Listings and details may lag changes upstream by up
to five minutes, and the cache clears on service restart. Cover and preview
URLs contained in cached detail JSON follow the same five-minute TTL. Resolver
responses, video bytes, and HLS playlists are outside this API cache. The
flattened DAV directories have a separate five-minute snapshot cache capped at
32 listings, 16 MiB, and 9,999 entries per listing. After five minutes, a request
returns the complete old snapshot immediately and starts a refresh. Old snapshots
are usable for at most two hours from their fetch time. With no usable snapshot,
the request waits for a refresh (up to 60 seconds); failure returns an error.
A failed refresh never publishes a partial directory or extends its deadline.
Each DAV request pins the snapshots it reads, so a refresh cannot mix directory
versions within that request. Numbered movie/entity paths may still change
between requests after a refresh; reopen the parent listing if a path disappears.

A single background worker checks every 30 seconds for expired directories used
by clients in the last 30 minutes. It shares in-flight reads with requests,
yields to existing directory fetches, and backs off failed refreshes from 30
seconds to 15 minutes. Background work does not extend the activity window.
It refreshes complete directories within the existing page/size/time limits;
it does not crawl unseen directories or prefetch media.

Complete directory snapshots are persisted on Unix systems under
`<OS user cache directory>/metadonalds/dav` (typically
`~/.cache/metadonalds/dav` on Linux). Disk storage is also capped at 32 snapshots
and 16 MiB, with an atomic replacement per successful refresh. Snapshots retain
their original age after restart and are discarded if corrupt, expired, or
from a different source configuration. The dedicated directory is private
(0700, files 0600) and permits one writer. Lock/write failures degrade to memory
caching and emit a warning. On other platforms persistence is unavailable and
the service uses memory caching.

```toml
[dav_cache]
# Optional absolute dedicated directory; empty disables persistence.
# dir = "/var/cache/metadonalds/dav"
refresh = true
```

`METADONALDS_DAV_CACHE_DIR` and `METADONALDS_DAV_CACHE_REFRESH` override these
settings. Disabling periodic refresh still allows request-triggered refreshes.
Compose mounts a `dav-cache` volume at `/cache` and uses `/cache/dav`.
Persisted snapshots contain directory metadata and search paths, not credentials,
media tokens, images, videos, or complete movie details. Cached listings therefore
do not make movie files available offline. Stop the service before removing its
cache directory. The worker and in-flight directory requests stop with the owning
service; attaching a TUI to an existing service does not start another worker.

Static DAV directories use the service start time as their modification time;
loaded browse directories use their snapshot time. Movie folders and their
metadata, cover, sample images, playlists, and streamed files display the
movie's release date when JavDB supplies a valid date, falling back to the
listing snapshot time. Generated files have a content-based ETag on GET, HEAD,
and PROPFIND, so a changed file can be detected even if its displayed date and
size stay the same. For generated files, an HTTP `Last-Modified` header is
omitted when the displayed date is in the future. Date-only
`If-Modified-Since`, `If-Unmodified-Since`, and `If-Range` requests do not use
the historical release date as a content validator. Video and HLS segment
proxy responses retain the upstream HTTP validators.
If an upstream video or HLS body stops delivering bytes for 30 seconds, the
service cancels that fetch. An upstream read failure after response headers
have been sent aborts the client connection, so a partial stream is not
reported as a completed file. The request log records this as `outcome=aborted`
with the same trace ID as the failed proxy copy.

Each DAV response has an `X-Trace-ID` header. The JSON request log uses that
`trace_id` with a root `span_id`; DAV listing, JavDB API cache, upstream HTTP,
response validation, image, HLS, resolver, and proxy stages log their own
`span_id`, `parent_span_id`, outcome, duration in milliseconds, and relevant
status, byte count, or item count. For example, copy an `X-Trace-ID` value from
a failed response and search the service logs for that exact `trace_id`. A
cache wait can contain `linked_trace_id`: follow that ID to see the separate
shared fetch, which may continue after the original request ends. The request
summary reports the numbered movie folder, section, file class, response
status and bytes, `dav_listing_cache` outcome, and safe failure category.
Search terms, media tokens, signed URLs, authorization headers, and raw
upstream errors are omitted from trace and request logs. The
anonymous JavDB upstream can return HTTP 403, in which case browse folders
cannot load; this service does not have credentials to bypass that response.
After installing or building a new MetaDonalds binary, restart the service to
serve these paths.

rclone can consume this DAV view as a separate WebDAV remote. For an ad hoc
read-only check without editing your rclone configuration:

```sh
rclone --webdav-url http://127.0.0.1:8080/dav/ --webdav-vendor other lsf :webdav:catalog/latest/
rclone --webdav-url http://127.0.0.1:8080/dav/ --webdav-vendor other cat :webdav:catalog/latest/0001-SSIS-006/metadata.json
```

To mount it, configure a WebDAV remote with URL ending in `/dav/`, then run
`rclone mount --read-only <remote>: <mountpoint>` on a system with FUSE support.
The rclone RC `operations/list` command can inspect the same configured remote;
it does not need access to MetaDonalds' private PikPak RC socket. rclone's
[WebDAV backend](https://rclone.org/webdav/) reads a WebDAV endpoint, while
[RC `serve/start`](https://rclone.org/rc/#serve-start-create-a-new-server)
serves an existing rclone filesystem and cannot create the JavDB catalog.
The test suite runs real rclone `lsf`, `cat` (including decoded images and HLS
keys/segments), RC `operations/list`, and `rclone serve http` through its VFS
against a local fake DAV server, including a nested HLS segment. This workspace
has no `/dev/fuse`, so an actual `rclone mount` was not tested here.

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

These settings cover MetaDonalds' Nyaa and JavDB API requests and rclone's PikPak
requests. The DAV media fetcher connects directly and checks the resolved public
address at each connection and redirect, so media fetches do not use these proxy
settings.
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

When `rc_socket` is omitted from a `[[plugins.downloaders]]` entry, it defaults to
`<OS temporary directory>/metadonalds-<uid>/rclone.sock`. To reuse a particular
socket, set `rc_socket` to an absolute path in a writable directory. Compose
mounts `/tmp` as a temporary filesystem for the private socket directory.

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

Cobra provides two commands: `metadonalds serve` and `metadonalds tui`.
Use `metadonalds serve --open` to start the service and open the browser.
Run `metadonalds --help` or a subcommand's `--help` without a configuration file. Flags use two leading hyphens.

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
never fall back to automatic discovery. An invalid or unreadable selected file
fails startup.

When no explicit config path is given and every automatic candidate is absent,
MetaDonalds initializes `$XDG_CONFIG_HOME/metadonalds/config.toml` (using the
platform's user config directory when the variable is unset) and logs the actual
path. New directories use mode `0700` and the file uses `0600` on Unix. Existing
files are never overwritten, including when two commands start simultaneously.
An existing system config still takes precedence over creating a new user config.

The generated configuration lists Nyaa, JavDB and PikPak instances and listens
on `127.0.0.1:8080`; the Compose example
instead listens on `0.0.0.0:8080`. Search remains available if rclone is missing;
configure the PikPak remote before submitting downloads. A missing explicit
`--config` or `METADONALDS_CONFIG` path remains an error. Help commands and clients
that reuse an already healthy service do not create configuration files.

Environment overrides are:

| Variable | Setting |
|---|---|
| `METADONALDS_SERVER_LISTEN_ADDR` | `server.listen_addr` (`serve --listen` overrides it) |
| `METADONALDS_DAV_CACHE_DIR` | `dav_cache.dir` |
| `METADONALDS_DAV_CACHE_REFRESH` | `dav_cache.refresh` |
| `METADONALDS_SERVER` | Client server origin (`tui --server` overrides it) |

The selected or newly initialized file is validated before starting a backend. Unknown file keys and
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

The terminal command checks `/healthz` at startup. If the service is already
running, it reuses it without reading local configuration. If a local HTTP
service is unavailable, it starts the same Go backend inside the client process,
using the configuration discovery order above. No separate server executable
or Go toolchain is needed by the built client.

Terminal automatic startup binds only to the loopback address and port in `--server`,
overriding `server.listen_addr` for this run without changing the file. It
supports `localhost` and loopback IP addresses. Unavailable remote or HTTPS
origins return an error; an occupied port is never replaced. The client waits
for readiness before opening its interface. Invalid configuration is reported at startup; absent automatic configuration
is initialized as described above. Use an absolute `--config` path when launching outside
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
./bin/metadonalds serve --open --config /path/to/config.toml
```

`serve --open` starts its own HTTP service, prints the URL once HTTP is ready,
then opens it using `open` on macOS, `xdg-open` on Linux, or `rundll32` on Windows.
It uses `server.listen_addr` (or `--listen`), and an occupied port is an error;
it does not reuse another service. Wildcard addresses open their matching
loopback address (`0.0.0.0` → `127.0.0.1`, `[::]` → `[::1]`); a specific LAN IP
is preserved. With `--listen 127.0.0.1:0`, the browser uses the actual assigned
port. Browser requests go directly to the service; there is no native window,
proxy or separate desktop build.

The command runs until Ctrl+C or SIGTERM, then stops its server. Closing the
browser does not stop the service, and stopping the command does not close the
browser. If browser opening fails (for example, in a headless environment), it
reports the error and URL and keeps the service available for manual access.
Unsupported operating systems get the same manual-open fallback.
`serve --help` does not load configuration or start a service.

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
at least 60 columns wide and 20 rows tall. The TUI’s `--server` option
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
Huma's `$schema` link. Cross-origin protection errors use plain text.

Search a source using its configured instance ID. Nyaa results are sorted by
seeders, trusted status and remake status. The trimmed query must be 1–200 UTF-8
bytes; the response contains `items` and an optional `next_cursor`:

```sh
curl 'http://127.0.0.1:8080/api/v1/sources/nyaa/search?q=Frieren'
curl 'http://127.0.0.1:8080/api/v1/sources/nyaa/search?q=Frieren&f=2&c=1_2'
```

Omitted Nyaa filters use configured values. Explicit `f=0` and `c=0_0` override
them. Empty or malformed filters receive 422; `f` must be non-negative and `c`
must use the `digits_digits` format. Other sources reject these Nyaa filters.

Open an item’s resources, then submit a chosen magnet to a downloader instance:

```sh
curl 'http://127.0.0.1:8080/api/v1/sources/nyaa/items/0123456789abcdef0123456789abcdef01234567/resources'
curl -X POST http://127.0.0.1:8080/api/v1/downloaders/pikpak/submissions \
  -H 'Content-Type: application/json' \
  -d '{"resource":{"id":"chosen","title":"Chosen resource","kind":"magnet","info_hash":"0123456789abcdef0123456789abcdef01234567"}}'
```

Submission bodies are limited to 8192 bytes, reject unknown fields, and require
a magnet with a 40-character hexadecimal `info_hash`. Outcomes are `accepted`,
`rejected`, or `unknown`, with a normalized lowercase hash. Acceptance means
rclone accepted the command. Check the downloader before manually retrying an
unknown outcome. There are no automatic submission retries.

Cross-origin browser submissions are rejected with HTTP 403. Same-origin browser
requests and non-browser clients remain supported. This protection does not add
authentication; use a trusted network or authenticated reverse proxy when making
the service reachable by other devices. `GET /healthz` returns plain text `ok`.

## Logs

The standalone MetaDonalds server writes JSON logs to stderr with timestamps
and severity levels. An automatically started backend writes the same logs to
a private `metadonalds-server-*.log` file in the OS temporary directory; the
client prints its path at startup. This avoids disrupting terminal rendering.
Startup and fatal errors are logged, and each completed HTTP request records
its method, matched route, status, and duration in milliseconds. HTTP 4xx
responses use WARN; 5xx responses use ERROR. Unmatched routes have an empty
route field. Logs omit search terms, full request URLs, bodies, headers and raw
upstream errors. A 502 from a source route indicates an upstream source failure.
Downloader transport failures return an `unknown` submission outcome because
rclone may have accepted the command before the response was lost.

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


The [justfile](justfile) is the shared task entry point for local development
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
TypeScript stays on version 6 because the current Svelte checker requires an
experimental dual-version setup for TypeScript 7. Tests use fake HTTP responses and
temporary files; they need no running services or account credentials.
Repository-specific agent guidance is in [AGENTS.md](AGENTS.md).
