# HTTP API and plugins

[Project overview](../README.md)

## Routes and plugin boundary

| Method | Path | Result |
|---|---|---|
| GET | `/api/v1/plugins` | Enabled instances and declared capabilities/resource types |
| GET | `/api/v1/sources/{source}/search?q=...&cursor=...` | `items`, optional `next_cursor` |
| GET | `/api/v1/sources/{source}/items/{id}` | One `Item` |
| GET | `/api/v1/sources/{source}/items/{id}/resources` | `resources` |
| POST | `/api/v1/downloaders/{downloader}/submissions` | Submission outcome |

Nyaa search also accepts per-request `f` and `c`; other sources reject these
filters. Cursors belong to the source and query that produced them.

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

## Requests and errors

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
