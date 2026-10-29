# Design: YouTube playlist import

## Technical Approach

Pasting a playlist link in `#musica` expands it over HTTP first, then sends **one** WebSocket
control. The hub never waits on the network.

- **Server, HTTP (`cmd/chatroom/playlist.go`, new).** `GET /playlist?list=ID` follows the shape
  of `gifs.go` closely: a `playlistImport` value built in `run()` only when `YOUTUBE_API_KEY` is
  set, a no-redirect `http.Client`, a 1 MiB body cap, sanitized errors, and a mutex-guarded TTL
  cache. It makes two upstream calls (`playlistItems.list`, then `videos.list`), each costing 1
  quota unit. It returns `{"videos":[…]}` with ≤ 50 playable 11-char ids, in playlist order.
- **Server, hub (`internal/chat`).** There is a new action `add_many {videos:[…]}`. `parseMusic`
  validates it strictly, as it does `add`. `player.apply` applies it atomically and produces one
  frame. `maxMusicQueue` goes from 30 to 50. `room.go` and `client.go` don't change: `add_many` is
  just another `inboundMusic` and spends one token of the existing music gate.
- **Page (`app.js`).** `parseYouTubeId` becomes `parseYouTubeLink`, which returns
  `{video, list}`. The submit handler gets an import path. It is guarded by a token, disables
  Poner while in flight, truncates to the free slots it knows about, and shows fixed Spanish
  texts. `MUSIC_QUEUE_MAX` goes to 50 and `MUSIC_ADD_MANY_MAX = 50` is new. Both are guarded by a
  mirror test.

The change adds no dependencies, no new server→client frame and no CSP change (`connect-src
'self'` already covers a same-origin `fetch`).

## YouTube Data API v3 contract (verified 2026-10-29)

Sources: developers.google.com/youtube/v3/docs/{playlistItems/list, playlistItems, videos/list,
videos, errors}, the "getting-started" page (`fields`), and docs.cloud.google.com
"api-keys-use".

| Fact | Source says | What we rely on |
|------|-------------|-----------------|
| `playlistItems.list` `part` | `contentDetails`, `id`, `snippet`, `status` | `part=contentDetails` only |
| `playlistItems.list` filter | exactly one of `id` / `playlistId` | `playlistId=<validated id>` |
| `maxResults` | 0..50, default 5 | `maxResults=50` (first page; `nextPageToken` ignored) |
| `contentDetails.videoId` | "The ID that YouTube uses to uniquely identify a video" | the only field read per item |
| Private/deleted items in a playlist | **Not documented** (no "Private video"/"Deleted video" titles, no `privacyStatus` values listed for playlist items) | **we do not rely on it** (see the decision below) |
| `playlistItems.list` errors | 404 `playlistNotFound`; 403 `playlistItemsNotAccessible` ("not properly authorized to retrieve the specified playlist"), `watchHistoryNotAccessible`, `watchLaterNotAccessible`; 400 `playlistOperationUnsupported` | 404, or 403 with `playlistItemsNotAccessible` → our `not_found` |
| `videos.list` `part` | includes `status` | `part=status` |
| `videos.list` `id` | comma-separated list; no max documented (the de facto limit is 50) | ≤ 50 ids, each re-validated, so no comma can enter one |
| `videos.list` missing ids | not documented whether they are omitted | **an id absent from `items` is dropped** (fail closed) |
| `status.privacyStatus` | `private`, `public`, `unlisted` | keep `public`, `unlisted` |
| `status.uploadStatus` | `deleted`, `failed`, `processed`, `rejected`, `uploaded` | keep only `processed` |
| `status.embeddable` | bool; embedding may still be blocked (Content ID) | keep only `true` |
| Quota | 1 unit per call (`fields` does not change it; even invalid requests cost ≥ 1) | 2 units per uncached successful import, 1 per not-found |
| `fields` | partial response: `a,b`, `a(b,c)`, `a/b` | `items/contentDetails/videoId`; `items(id,status(uploadStatus,privacyStatus,embeddable))` |
| `quotaExceeded` | 403 | → 502 `unavailable` |
| Error envelope | not shown in the YouTube docs; Google APIs use `{"error":{"code","message","errors":[{"domain","reason","message"}]}}` | only `error.errors[].reason` is read, and only on a 403 |
| API key transport | "use the `x-goog-api-key` HTTP header"; `key=` query param only as a fallback ("exposing your key … through URL scans") | **header**, never the URL |

Requests (`base` = `https://www.googleapis.com/youtube/v3`, all params built with `url.Values`):

```
GET {base}/playlistItems?part=contentDetails&playlistId=ID&maxResults=50&fields=items%2FcontentDetails%2FvideoId
GET {base}/videos?part=status&id=V1,V2,…&fields=items(id,status(uploadStatus,privacyStatus,embeddable))
    header on both: X-Goog-Api-Key: <key>
```

Upstream → our response:

| Upstream | `/playlist` answers |
|----------|---------------------|
| playlistItems 200, videos 200 | 200 `{"videos":[…]}` (possibly `[]`) |
| playlistItems 200 with no valid ids | 200 `{"videos":[]}`, **without** the videos call |
| playlistItems 404 (any reason) | 404 `{"error":"not_found"}` |
| playlistItems 403 whose reasons include `playlistItemsNotAccessible` | 404 `{"error":"not_found"}` |
| any other non-200 from either call (403 `quotaExceeded`/`forbidden`, 400 `keyInvalid`, 5xx, 30x), transport error, timeout, undecodable or over-cap body | 502 `{"error":"unavailable"}` (logged, sanitized) |

## Architecture Decisions

### Decision: Filter only on `videos.list`, the single authoritative check

**Choice**: `playlistItems.list` is asked only for `contentDetails`. Each `videoId` is
re-validated (`^[A-Za-z0-9_-]{11}$`) and de-duplicated, preserving order. Then **one**
`videos.list part=status` call runs for those ids, and a video is kept only if it is **present**
in `items` with `uploadStatus == "processed"`, `privacyStatus ∈ {public, unlisted}` and
`embeddable == true`. The result is the playlist-order list filtered by that set. Ids that
`videos.list` returns but we didn't ask for are ignored by construction.

**Alternatives considered**: (a) The proposal's two-stage filter, which also drops playlist
items whose `status.privacyStatus` isn't public/unlisted. The docs don't list the values for
playlist items, nor how private or deleted items appear, so that check would rest on
undocumented behavior. It is also redundant: `videos.list` is documented and covers privacy too.
(b) Filtering on `snippet.title == "Private video"/"Deleted video"`. That's undocumented and
locale-dependent.

**Rationale**: One documented check, which fails closed. A missing, deleted, private or
non-embeddable video can't reach the queue, where it would stall playback (the track only advances
on `ended`). The quota stays at 2 units. **This refines the proposal** (same outcome, one filter
point).

### Decision: The API key goes in the `X-Goog-Api-Key` header; keep every gifs.go guard anyway

**Choice**: `req.Header.Set("X-Goog-Api-Key", p.key)`. No `key=` in the URL. All of the gifs.go
hygiene stays: transport errors are replaced with fixed strings, `CheckRedirect` returns
`http.ErrUseLastResponse`, and the key is never logged or echoed.

**Alternatives considered**: `key=` in the query string, as Giphy's `api_key` does.

**Rationale**: Google's own guidance prefers the header. With the key out of the URL, a
`*url.Error` can't carry it, which is defense in depth on top of the sanitized errors. The
no-redirect policy is **more** important here than for Giphy: Go's client forwards custom headers
(unlike `Authorization`/`Cookie`) on a cross-host redirect, so following one would ship the key.
**This refines the proposal**, which assumed the key in the query string.

### Decision: `playlist.go` is a sibling of `gifs.go`; no shared upstream helper

**Choice** (`cmd/chatroom/playlist.go`):

```go
const (
	youtubeAPI       = "https://www.googleapis.com/youtube/v3"
	playlistMax      = 50 // playlistItems.list maxResults ceiling; first page only
	maxYouTubeBody   = 1 << 20
	playlistCacheTTL = 10 * time.Minute
	playlistCacheMax = 100
	youtubeTimeout   = 5 * time.Second // the whole import (both calls), not each call
)

var (
	playlistID     = regexp.MustCompile(`^[A-Za-z0-9_-]{10,64}$`) // mirrored by YT_LIST_ID in app.js
	youtubeVideoID = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)    // same rule as chat's videoID
)

var errPlaylistNotFound = errors.New("playlist not found")

type playlistCacheEntry struct {
	videos  []string // never mutated after insertion
	expires time.Time
}

// playlistImport proxies YouTube playlist expansion. Same concurrency contract as gifSearch.
type playlistImport struct {
	baseURL string
	key     string           // never logged, never returned, never in a URL
	client  *http.Client     // no redirects; Timeout youtubeTimeout as a backstop
	timeout time.Duration    // deadline for the whole import; tests shorten it
	now     func() time.Time

	mu    sync.Mutex
	cache map[string]playlistCacheEntry // key: the list id, case-sensitive
}

func newPlaylistImport(baseURL, key string) *playlistImport
func (p *playlistImport) handle(c *gin.Context)                                            // GET /playlist
func (p *playlistImport) fetch(ctx context.Context, list string) ([]string, error)          // both calls + filter
func (p *playlistImport) get(ctx context.Context, path string, q url.Values, out any) error // one call: header, status, decode
func (p *playlistImport) cached(list string) ([]string, bool)
func (p *playlistImport) store(list string, videos []string)
```

`handle`: `setStaticHeaders(c)`. If `list := c.Query("list")` fails `playlistID` → 400
`{"error":"invalid_list"}`. On a cache hit → 200. Otherwise `fetch`:
`errors.Is(err, errPlaylistNotFound)` → 404 `{"error":"not_found"}`. Any other error →
`log.Printf("playlist import: %v", err)` and 502 `{"error":"unavailable"}`. On success it stores
when `len > 0` (an empty result may be transient, as with gifs), then
`c.JSON(200, struct{ Videos []string `json:"videos"` }{videos})`. `videos` is built with
`make([]string, 0, …)`, so it is never `null`.

`get`: `http.NewRequestWithContext` → `errors.New("youtube request failed")`. `client.Do` error →
the same fixed string, never wrapped. On non-200 it decodes the error envelope (through the same
`LimitReader`, best effort) and returns `youtubeStatusError{code, reasons}`. Its `Error()` prints
`youtube status 403` with no upstream text. On 200 it decodes via
`io.LimitReader(resp.Body, maxYouTubeBody)` and returns `errors.New("youtube response invalid")`
on error. `fetch` maps the playlistItems status error to `errPlaylistNotFound` (404, or 403 with
`playlistItemsNotAccessible`). The videos call never maps to not-found.

`fetch` wraps both calls in `context.WithTimeout(ctx, p.timeout)`, so the user waits ≤ 5 s
total, not 5 s per call. `client.Timeout` stays as a backstop.

Response headers match `/gifs` exactly: `Content-Type: application/json; charset=utf-8`,
`Cache-Control: no-cache`, `X-Content-Type-Options: nosniff`.

**Alternatives considered**: (a) Extracting a shared `upstreamClient` (no-redirect client,
capped decode, sanitized errors, TTL cache) used by both gifs.go and playlist.go. The overlap is
real but small (about 15 lines of client and cache code), and the two differ where it matters:
key placement (query vs header), error mapping (status-only vs reason-aware), cache key (lowercased
vs case-sensitive) and the timeout scope. A generic helper would need options for each difference,
which is a framework for two callers. (b) Putting the playlist code inside `gifSearch`. That
mixes two upstreams and two keys.

**Rationale**: The project's rule is to extract once a pattern is stable *and* identical. The two
cache implementations are identical today, and the cache is the one candidate. It is
deliberately left duplicated, and the risk is noted: if a third proxy appears, extract
`ttlCache[V]` then. The only shared test code is the existing `upstream` recorder (below).

### Decision: `add_many` is parsed strictly and applied atomically in `player.apply`

**Choice** (`music.go`):

```go
const (
	maxMusicQueue   = 50 // was 30; mirrored by MUSIC_QUEUE_MAX
	maxMusicAddMany = 50 // ids per add_many; mirrored by MUSIC_ADD_MANY_MAX
	musicAddMany    = "add_many"
)

type musicCmd struct {
	action string
	video  string   // add only
	videos []string // add_many only: 1..maxMusicAddMany valid ids, repeats allowed (as with add)
	pos, id, seq int64
}
```

`parseMusic` (`message.go`), new case:

```
add_many: raw := env["videos"]; it must start with '[' (rejects a missing key, null, a string, an object)
          json.Unmarshal(raw, &[]json.RawMessage) ok; 1 <= len <= maxMusicAddMany
          every element: jsonString ok (rejects null, numbers, nested arrays) && videoID.MatchString
          → musicCmd{action, videos}. Any failure → invalid (CHAT-21 2a: silent discard).
```

The frame is already ≤ 4096 bytes (`defaultMaxMessageSize`), so the decode is bounded. 50 ids
take about 750 bytes. Extra keys are ignored, as for `add`.

`apply`, new case:

```go
case musicAddMany:
	vids := c.videos
	idle := p.video == "" || (p.finished && len(p.queue) == 0) // same condition as add
	free := maxMusicQueue - len(p.queue)
	if !idle && free <= 0 {
		return false // nothing fits: no frame
	}
	if idle {
		p.start(queueItem{Video: vids[0], By: by}, now) // seq++ once, by = sender, finished cleared
		vids = vids[1:]
	}
	for _, v := range vids[:min(len(vids), free)] {
		p.nextID++
		p.queue = append(p.queue, queueItem{p.nextID, v, by})
	}
	return true
```

Semantics:
- **Idle** (no video, or finished with an empty queue): the first id becomes current, so `seq`
  goes up by exactly 1. The rest are queued. Since the queue is empty when idle, `free = 50`, and
  at most 49 remain, so all fit.
- **Playing, paused or finished with a non-empty queue** (the last is unreachable, as in
  music-queue): `seq`, `by`, `pos` and `at` are untouched. It appends `min(len, free)` items with
  consecutive fresh ids. Overflow is dropped.
- **Nothing fits** → `false`. The state is untouched, there is no frame, and the gate token is
  already spent (same as a refused `add`).
- One `apply` produces one `Room.music` publish, so there is exactly one frame per batch.

`room.go` and `client.go` don't change. `add_many` is an `inboundMusic`, and the `readPump`
switch sends everything but `ended` to the music gate, so it costs **one** 260 ms token. The
`videos` slice is owned by the `inbound` and copied by value into `queueItem`s, so nothing aliases.

**Consequence**: a `[]string` field makes `musicCmd`, and so `inbound`, non-comparable.
`TestParseInbound` (`client_test.go:866`, `got != tt.want`) must switch to `reflect.DeepEqual`.
No production code compares either type (verified).

**Alternatives considered**: (a) A fixed array `[50]string` plus a count, which keeps `==`. It's
awkward, and it carries 800 bytes on every inbound. (b) Reusing `add` in a loop inside the hub.
That's the same code with N frames' worth of `seq` risk. (c) Rejecting the whole batch when it
doesn't fit (all-or-nothing). The user chose "fill free slots".

**Rationale**: It mirrors `add` exactly (same idle condition, same `start`), so the state machine
stays table-testable in `TestPlayerApply`. Validation stays in the pure parse.

### Decision: The client truncates to `min(MUSIC_ADD_MANY_MAX, MUSIC_QUEUE_MAX − queued)`; the server cap is authoritative

**Choice**: `freeSlots() = Math.min(MUSIC_ADD_MANY_MAX, MUSIC_QUEUE_MAX - (music.srv ? music.srv.queue.length : 0))`.

The proposal's "+1 for the current track when idle" is **dropped as moot**. Idle implies an empty
queue (nothing is ever queued while `video == ""`, and a finished track with items can't occur),
so `MUSIC_QUEUE_MAX - 0 = 50` already equals the `add_many` ceiling. The client can't see the
server's `finished` flag, and it doesn't need to:
- Client thinks "playing", server is "finished + empty": the client sends ≤ 50, and the server
  starts 1 and queues ≤ 49. Everything fits.
- Client is stale (someone queued meanwhile): the server drops the overflow. The client's
  "Se agregaron N de M" can overstate. This is accepted per the proposal (races are benign).

The watch `?v=X&list=Y` merge can produce 51 ids (X plus 50), so the cap of 50 on the client
matters there.

**Alternatives considered**: A server→client result frame with the accepted count. Rejected in the
proposal (no new frame).
**Rationale**: It is the simplest formula that is never wrong about what the server *would* accept
on a quiet queue.

### Decision: Mirror tests live next to the Go constants

**Choice**:
- `internal/chat/room_test.go` → `TestMusicLimitsMirroredInJS` reads
  `filepath.Join("..", "..", "cmd", "chatroom", "app.js")` (`go test` runs in the package dir) and
  asserts `const MUSIC_QUEUE_MAX = <maxMusicQueue>;` and `const MUSIC_ADD_MANY_MAX = <maxMusicAddMany>;`.
- `cmd/chatroom/main_test.go` → `TestPlaylistIDMirroredInJS` asserts that
  `const YT_LIST_ID = /<playlistID.String()>/;` appears in `appJS`.

**Alternatives considered**: Exporting `chat.MaxMusicQueue`/`MaxMusicAddMany` so the test fits
next to the other mirror tests in `main_test.go`, as the proposal said. That widens the package API
only for a test, and renames a constant used by about 8 test sites.
**Rationale**: The constants stay unexported, and each test sits in the package that owns the Go
side. **This refines the proposal** (test location only).

### Decision: Page import flow is a token-guarded async step; no AbortController

**Choice** (`app.js`):

Constants (next to `YT_ID`, `app.js:1075-1092`):

```js
const YT_LIST_ID = /^[A-Za-z0-9_-]{10,64}$/; // mirrors playlistID in playlist.go
const YT_MIX_PREFIX = 'RD';                  // auto-generated mixes: not readable by the API
const MUSIC_QUEUE_MAX = 50;                  // mirrors maxMusicQueue (was 30)
const MUSIC_ADD_MANY_MAX = 50;               // mirrors maxMusicAddMany
const PLAYLIST_TEXT = {
  loading:  'Importando playlist…',
  disabled: 'Importar playlists no está disponible.',
  notFound: 'No encontré esa playlist (¿es privada?).',
  empty:    'Esa playlist no tiene videos reproducibles.',
  failed:   'No se pudo leer la playlist.',
  mix:      'Las mezclas automáticas de YouTube no se pueden importar.',
  onlyVideo: ' Puse solo el video.',
};
```

`music` gains `importing: false` and `importSeq: 0`. `resetMusic` (`app.js:1653`, called on
leave at `app.js:336`) does `music.importSeq++` and `music.importing = false`, so a fetch that
resolves after the user left `musica` is discarded. Switching panes does **not** reset: the user
is still joined (global music), and the import still completes and sends.

`parseYouTubeLink(text)` replaces `parseYouTubeId` (`app.js:1138`). The same URL guards apply
(https, no port or credentials). It returns `{video, list}` or `null`:

| Form | Result |
|------|--------|
| `YT_WATCH_HOSTS` + `pathname === '/playlist'` + `YT_LIST_ID.test(list)` | `{video:null, list}` |
| same, list missing or invalid | `null` → "Ese link no es de YouTube." |
| `YT_WATCH_HOSTS` + `/watch`, valid `v`, valid `list` | `{video:v, list}` (a mix prefix is still returned, and the handler decides) |
| `/watch`, valid `v`, list missing or invalid | `{video:v, list:null}` (today's behavior) |
| `youtu.be/…`, `/shorts/…` | `{video, list:null}` (unchanged; `?list=` ignored) |

Submit handler (`app.js:1689`):

```
if music.importing → return                                  (Poner is disabled anyway)
link = parseYouTubeLink(value); null → 'Ese link no es de YouTube.'
if freeSlots() <= 0 → 'La cola está llena' (existing text), return   (also saves quota)
if !link.list → add link.video exactly as today
if link.list.startsWith(YT_MIX_PREFIX):
    link.video ? add it, then show PLAYLIST_TEXT.mix + onlyVideo : (unreachable: /playlist?list=RD… → notFound path via server)
else importPlaylist(link)
```

`importPlaylist({video, list})`:

```
const seq = ++music.importSeq; music.importing = true; setMusicError(loading); renderMusic()
let ids = null, fail = null
try {
  const res = await fetch('/playlist?list=' + encodeURIComponent(list))
  const body = await res.json().catch(() => null)
  if seq !== music.importSeq → return
  if res.ok && body && Array.isArray(body.videos) → ids = body.videos.filter(v => typeof v === 'string' && YT_ID.test(v))
  else if body && body.error === 'not_found' → fail = notFound
  else if res.status === 404 → fail = disabled        (route missing: gin's text/plain 404, no JSON)
  else → fail = failed
} catch { if seq === music.importSeq → fail = failed }
finally { if seq === music.importSeq → music.importing = false; renderMusic() }
if seq !== music.importSeq → return
if !fail && ids.length === 0 && !video → fail = empty
if fail && !video → setMusicError(fail); return       (the link stays in the box)
all = dedupe([video, ...(ids || [])].filter(Boolean))  (X first, X removed from the rest)
slots = freeSlots(); if slots <= 0 → 'La cola está llena'; return
send = all.slice(0, slots)
if sendMusic('add_many', { videos: send }):
   clear the input
   setMusicError(fail ? fail + onlyVideo : send.length < all.length ? `Se agregaron ${send.length} de ${all.length}.` : '')
else if $('music-error').textContent === loading → setMusicError('')   (sendMusic already showed its wait text, or we're offline; the link stays)
```

`renderMusic` (`app.js:1392`): `$('music-load').disabled = !online || music.importing;`.

The numbers in "Se agregaron N de M" are computed by the page, and every text is a constant. No
server string ever reaches the DOM (`textContent` only, as today).

**Alternatives considered**: (a) An `AbortController` with a client timeout. The server already
bounds the request (5 s import deadline, cache hits are instant), and the token makes a late
answer harmless. Aborting adds a code path and no user value. (b) Keeping Poner enabled and
queuing imports. That invites double-clicks and duplicate quota spend. (c) Sending X with `add`
and the list with a separate `add_many`. That's two gate tokens, and the second send would hit
the 300 ms client gap.

**Rationale**: It has the same structure as `loadGifs` (a sequence token, 404 → disabled), the
smallest state (two fields), and one WS send per import.

## Data Flow

```
Page A                         GET /playlist (gin handler goroutine)            YouTube API
 │ Poner "music.youtube.com/playlist?list=PLx"
 │ parseYouTubeLink → {video:null, list:PLx}; freeSlots()=48
 │ importing=true, Poner disabled, "Importando playlist…"
 │ fetch /playlist?list=PLx ──▶ playlistID ok → cache miss
 │                              ctx 5 s ─▶ playlistItems?part=contentDetails&playlistId=PLx&maxResults=50
 │                                         X-Goog-Api-Key ─────────────────────────▶ 200 items[50]
 │                                       re-validate + dedupe ids (≤50)
 │                                       ─▶ videos?part=status&id=… ───────────────▶ 200 items[47]
 │                                       keep processed ∧ public|unlisted ∧ embeddable, in playlist order
 │                                       store(PLx, 46 ids)
 │ ◀── 200 {"videos":[46 ids]} ──────────┘
 │ seq still current → send = 46.slice(0, 48)
 │ WS {"type":"music","action":"add_many","videos":[46]}
 ▼
readPump A: parseMusic(add_many) ok → music gate (1 token) → Room.broadcast
Room.Run: player.apply(add_many): playing → append 46 (ids n+1…n+46), seq unchanged
          publish ONE music frame ───────────────────────────▶ Pages A, B: renderQueue (48 items)
```

Watch link with a list, and a fallback:

```
watch?v=X&list=PLy  → fetch /playlist?list=PLy
   200 [A,X,B]      → send [X,A,B]                        (X first, deduped)
   404 not_found    → send [X], "No encontré esa playlist (¿es privada?). Puse solo el video."
   404 (disabled)   → send [X], "Importar playlists no está disponible. Puse solo el video."
watch?v=X&list=RDz  → no fetch; add X, "Las mezclas automáticas de YouTube no se pueden importar. Puse solo el video."
```

## Wiring and config (`main.go`, README)

- `config` gains `youtubeKey string // YOUTUBE_API_KEY; "" disables /playlist`. `main()` reads it
  with `strings.TrimSpace(os.Getenv("YOUTUBE_API_KEY"))`.
- `run()`: `var playlists *playlistImport`. If the key is set → `newPlaylistImport(youtubeAPI,
  cfg.youtubeKey)`. Otherwise it logs `YOUTUBE_API_KEY not set: playlist import disabled`.
- `newRouter(chatSrv, gifs, playlists)` registers `GET /playlist` only when it is non-nil. The
  doc comment is updated. The call sites are `main.go:226`, `main_test.go:28` and
  `gifs_test.go:48` (both tests pass `nil`).
- README: an env table row for `YOUTUBE_API_KEY` (what it enables, 404 without it, never reaches
  the browser or the logs, and the recommendation to restrict the key to "YouTube Data API v3" in
  Google Cloud Console). An HTTP table row for `GET /playlist?list=<id>` (200/400/404/502, ≤50,
  first page only, filtered, 10 min cache, 5 s). In the music section: the cap is 50, a new
  `add_many` protocol row, and the accepted link forms. The `.env` example, if any, gets the key
  commented out.

## Test plan

**`cmd/chatroom/playlist_test.go` (new)**. It reuses `upstream`, `testKey`, `okJSON`,
`requireJSONHeaders` and `lockedWriter` from `gifs_test.go` (same package). `fakeGiphy` is
renamed `fakeUpstream`, since both files use it now (a trivial rename). New helpers:
`setupPlaylist(t, h)`, `getPlaylist(t, ts, query)`, and a `youtubeFake` handler that routes on
`r.URL.Path` (`/playlistItems`, `/videos`) to canned bodies built by `itemsJSON(ids...)` and
`videosJSON(entries...)`.

| Test | Asserts |
|------|---------|
| `TestPlaylistDisabledWithoutKey` | `newRouter(…, nil, nil)` → `/playlist?list=PL…` is 404 |
| `TestPlaylistRequestParams` | playlistItems: `part=contentDetails`, `playlistId`, `maxResults=50`, `fields`. videos: `part=status`, `id` joined in order, `fields`. **No `key`/`api_key` query param on either.** Header `X-Goog-Api-Key == testKey`. Exactly 2 requests |
| `TestPlaylistInvalidList` | missing, 9 chars, 65 chars, `PL x`, `PL%0A…`, `..`, `PL,abc…` → 400 `invalid_list`, **0 upstream requests** |
| `TestPlaylistFilters` | playlist `[A,B,C,D,E,F,A,bad!,G]`. videos: A ok, B private, C unlisted ok, D `embeddable:false`, E `uploadStatus:"uploaded"`, F missing from items, G ok, plus an extra unrequested id Z ok → `{"videos":[A,C,G]}`. JSON headers. The videos request's `id` has no duplicates and no invalid id |
| `TestPlaylistEmpty` | playlistItems `items:[]` → 200 `{"videos":[]}` (literally `[]`, not `null`), **no** videos call, not cached (a second GET hits upstream again) |
| `TestPlaylistNotFound` | playlistItems 404; 403 with reason `playlistItemsNotAccessible` → 404 `{"error":"not_found"}` |
| `TestPlaylistUpstreamError` | playlistItems 403 `quotaExceeded`, 400 `keyInvalid`, 500, malformed JSON, body > 1 MiB; videos 403/500 → 502 `{"error":"unavailable"}` |
| `TestPlaylistRedirectNotFollowed` | upstream 302 to a second recorder server → 502, and the second server got **0** requests (the key header never leaves) |
| `TestPlaylistTimeout` | `p.timeout = 50ms`, the upstream sleeps on `/videos` → 502 within ≈ the deadline (the whole-import deadline, not per call) |
| `TestPlaylistKeyNeverLeaks` | secret key, captured `log` output (`lockedWriter`). Cases: transport error (closed server / bad base), 403, timeout, redirect, success → neither the response body nor the log contains the secret |
| `TestPlaylistCache` | the second GET of the same id → 0 new upstream requests. An id differing only in case → a miss (case-sensitive). After `now` advances past 10 min → a miss. Errors are not cached |
| `TestPlaylistCacheBound` | 101 distinct ids → the map stays ≤ 100 (same pattern as `TestGifsCacheBound`) |
| `TestPlaylistCap` | playlistItems returns 60 ids (an upstream ignoring maxResults) → videos `id` has ≤ 50, and the response is ≤ 50 |

**`internal/chat`**:

| Test | Change |
|------|--------|
| `TestParseInbound` (`client_test.go`) | comparison → `reflect.DeepEqual`. Valid rows: `add_many` with 1 id, 50 ids, repeated ids, and extra keys ignored. Invalid rows: missing `videos`, `null`, `[]`, 51 ids, `"abc"` (string), `{}`, `[null]`, `[123]`, `[["x"]]`, one bad id among good ones, an id with a trailing `\n`, a URL instead of an id |
| `TestPlayerApply` (`room_test.go`) | new subtests. **idle**: `add_many [v1,v2,v3]` → v1 current, seq 1, by sender, queue ids [1,2] by sender. **while playing**: seq/by/pos unchanged, ids continue from `nextID`. **finished + empty**: first becomes current, seq+1, finished cleared. **partial fit**: 48 queued + 4 ids → 2 appended, the rest dropped. **full**: 50 queued → `false`, state untouched (DeepEqual against the snapshot). **idle with 50 ids** → 1 current + 49 queued |
| `queue cap` subtest (`room_test.go:1795-1816`) | uses `maxMusicQueue`, so it auto-adapts to 50. No literal change |
| `TestMusicQueueCap` (`room_test.go:2059-2076`) | the comment "30" → "maxMusicQueue". The loop is constant-driven. 51 frames still fit the client buffer of 64 |
| `TestMusicAddManyBroadcast` (new, hub) | A and B joined. `add_many` of 3 → **exactly one** frame each (`expectMusic` then `expectEmpty`) with queue of 2 |
| `TestMusicAddManyFull` (new, hub) | queue full → `add_many` → no frame (`broadcast` marker + `expectEmpty`) |
| `TestMusicAddManyOverWebSocket` (new, `client_test.go`) | real socket: one frame, then a second `add_many` within 260 ms is dropped (one gate token per batch) |
| `TestMusicLimitsMirroredInJS` (new) | `MUSIC_QUEUE_MAX` and `MUSIC_ADD_MANY_MAX` in app.js equal the Go constants |

**`cmd/chatroom/main_test.go`**: the `newRouter` call gets a third `nil`.
`TestPlaylistIDMirroredInJS` (new) is added. Existing music tests are unaffected (they don't
assert 30).

Gates: `/snap/go/current/bin/gofmt -l .`, `go vet ./...`, `go test -race ./...`.

**Manual checklist** (with a real key):
1. `music.youtube.com/playlist?list=PL…` (≥ 50 items) on an empty room → the first plays, 49 are
   queued, devtools shows one WS send, and no message appears (nothing was cut).
2. With 45 queued, import a 10-item list → "Se agregaron 5 de 10.", and the queue shows 50.
3. `watch?v=X&list=PL…` → X plays or is queued first, and the list follows without X duplicated.
4. `watch?v=X&list=RD…` → only X, plus the mix message.
5. A private or invented list → the not-found text. Unset the key → the disabled text. A watch
   link with a list then adds X alone, plus the "Puse solo el video." suffix.
6. Click Poner twice fast → one request. Leave `musica` mid-import → nothing is sent.
7. The server log never shows the key, and the network panel shows no YouTube API host (only
   `/playlist`).

## File Changes

| File | Action | Description |
|------|--------|-------------|
| `cmd/chatroom/playlist.go` | Create | `playlistImport`: validation, two header-keyed upstream calls under one 5 s deadline, the `videos.list` filter, not-found mapping, sanitized errors, TTL cache |
| `cmd/chatroom/playlist_test.go` | Create | Fake YouTube upstream. Covers params, filtering, empty, not_found, 502, redirect, timeout, key leaks (body and logs), cache, bound, cap, disabled route |
| `cmd/chatroom/gifs_test.go` | Modify | `fakeGiphy` → `fakeUpstream`. The `newRouter` call gets a third `nil` |
| `cmd/chatroom/main.go` | Modify | `config.youtubeKey`, reads `YOUTUBE_API_KEY`, builds `playlistImport` or logs disabled, `newRouter(…, playlists)` registers `GET /playlist` when non-nil |
| `cmd/chatroom/main_test.go` | Modify | The `newRouter` call. `TestPlaylistIDMirroredInJS` |
| `internal/chat/music.go` | Modify | `maxMusicQueue` 50, `maxMusicAddMany`, `musicAddMany`, `musicCmd.videos`, the `apply` `add_many` case |
| `internal/chat/message.go` | Modify | The `parseMusic` `add_many` case (array of strings, 1..50, each `videoID`) |
| `internal/chat/client_test.go` | Modify | `TestParseInbound` uses DeepEqual plus the add_many rows. `TestMusicAddManyOverWebSocket` |
| `internal/chat/room_test.go` | Modify | `TestPlayerApply` add_many subtests, the hub broadcast/full tests, `TestMusicLimitsMirroredInJS`, the comment at line 2059 |
| `cmd/chatroom/app.js` | Modify | `YT_LIST_ID`, `YT_MIX_PREFIX`, `MUSIC_QUEUE_MAX` 50, `MUSIC_ADD_MANY_MAX`, `PLAYLIST_TEXT`; `music.importing/importSeq`; `parseYouTubeLink`; `freeSlots`; `importPlaylist`; submit handler; the `renderMusic` Poner disable; `resetMusic` invalidation |
| `README.md` | Modify | `YOUTUBE_API_KEY` row, `GET /playlist` row, cap 50, the `add_many` protocol row, accepted link forms |
| `CLAUDE.md` | Modify | "En curso: youtube-playlist-import" while in progress; `playlist.go` in the structure; the Giphy key-hygiene rule extended to the YouTube key |
| `openspec/specs/{chat,server}/spec.md` | Modify at archive | Merge the CHAT-21/30/31 deltas and the new SRV requirement |

## Open Risks

- **Undocumented upstream behavior.** How private or deleted playlist items appear, and whether
  `videos.list` omits unknown ids, are not in the docs. The design fails closed (absent → dropped),
  so the worst case is fewer imported videos, never a stalled queue. One caveat: region-blocked or
  Content-ID-blocked videos still pass (`embeddable` doesn't cover them), and the existing player
  error handling applies.
- **The key header is untested against the real API in CI.** Google documents `x-goog-api-key`
  generally, but the YouTube page doesn't mention it. Manual checklist item 1 validates it. The
  fallback is a one-line change to the `key=` query param (the guards already cover it).
- **Quota burn via `/playlist`.** It is unauthenticated: random valid-looking ids cost 1 unit
  each, and not-found answers aren't cached. Rate limiting stays in the backlog (pragmatic scope).
- **The cache duplicates gifs.go's cache** (about 30 lines). Extract it if a third proxy appears.
- **Optimistic "N de M".** It can overstate when another user queues concurrently. Accepted.
- **`youtu.be/X?list=Y`** (the share button inside a playlist) keeps single-video behavior, per the
  proposal's link list. It is a candidate follow-up if users expect the import.
- **One import fills the queue for everyone.** Accepted for a friends chat.
