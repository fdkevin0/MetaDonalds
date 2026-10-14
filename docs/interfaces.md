# Browser and terminal interfaces

[Project overview](../README.md)

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

## Service ownership

The terminal command checks `/healthz` at startup. If the service is already
running, it reuses it without reading local configuration. If a local HTTP
service is unavailable, it starts the same Go backend inside the client process,
using the [configuration discovery order](setup.md#local-go-process). No separate server executable
or Go toolchain is needed by the built client.

Terminal automatic startup binds only to the loopback address and port in `--server`,
overriding `server.listen_addr` for this run without changing the file. It
supports `localhost` and loopback IP addresses. Unavailable remote or HTTPS
origins return an error; an occupied port is never replaced. The client waits
for readiness before opening its interface. Invalid configuration is reported at startup; absent automatic configuration
is initialized as described in [setup](setup.md#local-go-process). Use an absolute `--config` path when launching outside
the project directory.

The client stops only the backend it started when it exits. Existing services
remain running. If a second client reuses the first client's backend, closing
the first also disconnects the second; run `metadonalds serve` or Compose separately
when you need a server with an independent lifetime. An automatically started
backend also starts or reuses rclone for enabled downloader instances as described in [setup](setup.md#local-go-process). rclone owns PikPak
credentials and token refresh. `/healthz` checks the MetaDonalds HTTP server,
not current rclone or PikPak availability.

## Launching the browser

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

## Terminal controls

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

## Submission outcomes

All interfaces report command acceptance, not download completion. They do not
automatically retry submissions. An interrupted or failed submission can still
have reached PikPak: check there before confirming another attempt. Accepted
hashes are blocked from repeat submission within the current UI session;
this is not durable server-side deduplication.
