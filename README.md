# MetaDonalds

Search Nyaa or JavDB, inspect items and magnet resources, and submit a chosen
resource to PikPak through rclone. One Go service provides a browser UI, a
terminal client, and a read-only WebDAV view of online JavDB media.

Source and downloader plugins are compiled into the service. There is no download
job database or submission queue. WebDAV has a directory cache with background
refresh and optional disk persistence. An accepted submission means rclone
accepted the command, not that the download finished; check the downloader
before retrying an unknown outcome.

## Quick start with Docker Compose

Configure a PikPak remote named `pikpak` using `rclone config`, then:

```sh
cp config.example.toml config.toml
mkdir -p rclone
# Copy your rclone configuration to ./rclone/rclone.conf.
chmod 600 rclone/rclone.conf
docker compose pull
docker compose up -d --remove-orphans
```

The example sends downloads to `pikpak:My Pack`; adjust the remote/folder in
`config.toml`. Open <http://127.0.0.1:8080/>. Only listed plugin instances are
enabled; remove the downloader entry for search-only operation.

The service has no authentication. Compose exposes it on loopback; use a trusted
network or an authenticated reverse proxy for access from other devices.

## Documentation

| Task | Read |
|---|---|
| Configure instances, run locally or in Docker, set proxies, inspect logs | [Setup and operations](docs/setup.md) |
| Use the browser or terminal client, understand service ownership | [Interfaces](docs/interfaces.md) |
| Browse or mount JavDB media, manage caches, diagnose playback | [WebDAV](docs/webdav.md) |
| Call HTTP endpoints or add a compiled plugin | [API and plugins](docs/api.md) |
| Build, test, develop the frontend, or publish an image | [Development](docs/development.md) |

Configuration examples live in [config.example.toml](config.example.toml).
The running service exposes interactive API documentation at `/docs` and schemas
at `/openapi.json` and `/openapi.yaml`. Repository agent guidance is in
[AGENTS.md](AGENTS.md).
