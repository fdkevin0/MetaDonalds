# Setup and operations

[Project overview](../README.md)

Configure a PikPak remote named `pikpak` with `rclone config`. Copy the checked-in
[config.example.toml](../config.example.toml) to your local configuration:

```sh
cp config.example.toml config.toml
```

`config.toml` is ignored by Git. It is ready for Compose and uses `pikpak:My Pack`
as the destination. Change it to match your remote and folder. Unknown
configuration keys are rejected.

## Plugin instances

Configure sources and downloaders as explicit instances in
[config.example.toml](../config.example.toml):

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
magnets; the [read-only WebDAV view](webdav.md) also serves online pictures and video.
Magnet `size` is displayed with `upstream units` because its unit is not established.
A full upstream search page offers a next cursor; an extra empty last page is possible.
Nyaa RSS has no durable details endpoint: its item identity is the infoHash, and
clients preserve search metadata when opening the resource view.

## Docker Compose

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

## Outbound HTTP/HTTPS proxy

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

## Local Go process

Use the Go version declared in [go.mod](../go.mod) and install rclone. In
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
