# Read-only WebDAV for JavDB media

[Project overview](../README.md)

With an enabled JavDB source, connect a WebDAV file browser to
`http://127.0.0.1:8080/dav/` (substitute the service's reachable address and
port). In macOS Finder use **Go → Connect to Server** (`Cmd+K`) and enter that
URL. Finder can browse and copy files; playback of `.m3u8` files needs a player
that understands HLS and its relative segment paths. Finder itself was not
tested on macOS. If you listen on a LAN address, this same unauthenticated,
read-only DAV endpoint is reachable by other devices on that LAN; use a trusted
network or an authenticated reverse proxy for broader exposure.

## Browse paths and files

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

## Media delivery

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
## Caching and refresh

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

## Persistent snapshots

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

## Dates and HTTP validators

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

## Tracing and upstream failures

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

## Using rclone

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
against a local fake DAV server, including a nested HLS segment. An actual FUSE mount and macOS Finder behavior are not covered by these tests.
