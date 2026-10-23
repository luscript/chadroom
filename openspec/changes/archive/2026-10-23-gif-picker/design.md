# Design: GIF picker

## Technical Approach

A new same-origin `GET /gifs?q=` endpoint lives in a new file, `cmd/chatroom/gifs.go`. It is a
thin, stateless-looking proxy to the Giphy JSON API, with a small TTL cache behind its own mutex.
The page gets a search box and a results grid inside the existing `#gif-panel`, above the
paste-a-URL box, which stays as it is.

- **Server.** `run` builds a `*gifSearch` only when `GIPHY_API_KEY` is set and passes it to
  `newRouter`. A `nil` search means the route is not registered, so `/gifs` returns gin's 404
  and Giphy is never called. The handler trims `q`, rejects more than 50 runes, checks the cache,
  and on a miss calls `/v1/gifs/trending` or `/v1/gifs/search` with fixed parameters. It decodes
  only `data[].images.{fixed_height,fixed_width_small}.url`, re-validates and canonicalizes every
  URL, and answers `{"gifs":[{"url","preview"}]}`.
- **Page.** On the first open, the panel fetches trending. Typing re-queries after a 300 ms debounce,
  and a sequence counter drops stale responses. A 404 hides the search UI for the rest of the page's life.
  A click sends the GIF's `url` through the existing `sendText`.

This follows the proposal exactly. It adds no dependency, leaves the CSP unchanged and doesn't touch
`internal/chat` (the Room hub is not involved).

## Architecture Decisions

### Decision: A small `config` struct threads env into `serve`/`run`; `newRouter` takes the search

**Choice**:

```go
// config is what main reads from the environment.
type config struct {
	allowedOrigins string // ALLOWED_ORIGINS, comma-separated
	giphyKey       string // GIPHY_API_KEY; "" disables /gifs
}

func main()                                                  // serve(ctx, addr, config{os.Getenv("ALLOWED_ORIGINS"), strings.TrimSpace(os.Getenv("GIPHY_API_KEY"))})
func serve(ctx context.Context, addr string, cfg config) int // was (ctx, addr, allowedOrigins string)
func run(ctx context.Context, ln net.Listener, cfg config) int
func newRouter(chatSrv *chat.Server, gifs *gifSearch) *gin.Engine // gifs == nil → no /gifs route
```

`run` does `var gifs *gifSearch; if cfg.giphyKey != "" { gifs = newGifSearch(giphyAPI, cfg.giphyKey) } else { log.Println("GIPHY_API_KEY not set: GIF search disabled") }`.
`newRouter` adds `if gifs != nil { r.GET("/gifs", gifs.handle) }`.

Call sites that must change (all in `main_test.go`):

| Line | Today | After |
|------|-------|-------|
| `newTestServer` :26 | `newRouter(chatSrv)` | `newRouter(chatSrv, nil)` |
| `TestRunShutdownClosesAllRooms` :286 | `run(ctx, ln, "")` | `run(ctx, ln, config{})` |
| `TestRunGracefulShutdown` :335 | `run(ctx, ln, "")` | `run(ctx, ln, config{})` |
| `TestServeListenError` :390 | `serve(ctx, addr, "")` | `serve(ctx, addr, config{})` |

**Alternatives considered**: (a) A third positional string on `serve`/`run`. Two adjacent strings
are easy to swap by mistake, and the next env var would make three. (b) Registering `/gifs` always and
answering 404 from a nil-receiver method. That's more code for the same status, and the "never calls
Giphy" guarantee is less obvious. (c) Reading `os.Getenv` inside `gifs.go`. That breaks the "env only in `main()`" convention and is hard to test.
**Rationale**: A two-field struct is the smallest change that keeps env reading in `main()`. An
unregistered route makes "key unset → 404, no upstream call" true by construction.

### Decision: `gifSearch` owns the base URL, the key, the client, the clock and the cache

**Choice** (`gifs.go`):

```go
const (
	giphyAPI      = "https://api.giphy.com"
	gifLimit      = 24
	gifRating     = "pg-13"
	gifLang       = "es"
	maxGifQuery   = 50 // runes, after TrimSpace
	maxGiphyBody  = 1 << 20
	gifCacheTTL   = 10 * time.Minute
	gifCacheMax   = 100
	giphyTimeout  = 5 * time.Second
)

// gif is one picker result: url is sent in chat, preview is the thumbnail.
type gif struct {
	URL     string `json:"url"`
	Preview string `json:"preview"`
}

type gifCacheEntry struct {
	gifs    []gif // never mutated after insertion; shared by readers
	expires time.Time
}

// gifSearch proxies Giphy trending/search. Handlers run concurrently: every field is
// read-only after construction except cache, which only mu guards.
type gifSearch struct {
	baseURL string           // giphyAPI in production, an httptest URL in tests
	key     string           // never logged, never returned
	client  *http.Client     // Timeout: giphyTimeout; tests shorten it
	now     func() time.Time // time.Now; tests advance it

	mu    sync.Mutex
	cache map[string]gifCacheEntry // key: lowercased q ("" = trending)
}

func newGifSearch(baseURL, key string) *gifSearch
func (g *gifSearch) handle(c *gin.Context)
func (g *gifSearch) fetch(ctx context.Context, q string) ([]gif, error)
func (g *gifSearch) cached(key string) ([]gif, bool)
func (g *gifSearch) store(key string, gifs []gif)
func canonicalGIF(raw string) (string, bool)
```

**Alternatives considered**: (a) Package-level vars for the base URL and client, which tests would
overwrite. That's global mutable state and races under `t.Parallel`. (b) A `GifProvider` interface
for Tenor later. There's one provider, so that's YAGNI.
**Rationale**: Everything a test needs to swap is a field, so tests build a `gifSearch` literal
(or call `newGifSearch` and then override `client`/`now`). No globals change.

### Decision: Handler flow and status codes

**Choice**:

```go
func (g *gifSearch) handle(c *gin.Context) {
	setStaticHeaders(c) // nosniff + no-cache, as SRV-07
	q := strings.TrimSpace(c.Query("q"))
	if utf8.RuneCountInString(q) > maxGifQuery {
		c.JSON(http.StatusBadRequest, gin.H{"error": "query_too_long"})
		return
	}
	key := strings.ToLower(q)
	gifs, ok := g.cached(key)
	if !ok {
		var err error
		if gifs, err = g.fetch(c.Request.Context(), key); err != nil {
			log.Printf("gif search: %v", err) // err is one of fetch's own sanitized errors
			c.JSON(http.StatusBadGateway, gin.H{"error": "unavailable"})
			return
		}
		g.store(key, gifs)
	}
	c.JSON(http.StatusOK, struct {
		Gifs []gif `json:"gifs"`
	}{gifs})
}
```

`fetch` builds the request with `url.Values` (so `q` is escaped and the client can't inject any
parameter). It sends the **lowercased** key, so a cached entry is exactly what any casing of that
query would get:

```
trending: {base}/v1/gifs/trending?api_key=K&limit=24&rating=pg-13
search:   {base}/v1/gifs/search?api_key=K&lang=es&limit=24&q=<key>&rating=pg-13
```

It uses `http.NewRequestWithContext(ctx, GET, …)`, so a client that disconnects cancels the upstream
call. It returns **only its own errors**, never wrapping the transport error:
`errors.New("giphy request failed")`, `fmt.Errorf("giphy status %d", resp.StatusCode)` and
`errors.New("giphy response invalid")`. This matters because `*url.Error` embeds the full URL,
including `api_key`. It decodes through `io.LimitReader(resp.Body, maxGiphyBody)`, so a body
truncated at 1 MiB fails to decode and returns 502:

```go
var body struct {
	Data []struct {
		Images struct {
			FixedHeight     struct{ URL string `json:"url"` } `json:"fixed_height"`
			FixedWidthSmall struct{ URL string `json:"url"` } `json:"fixed_width_small"`
		} `json:"images"`
	} `json:"data"`
}
```

For each item: `u, ok := canonicalGIF(FixedHeight.URL)`, and the item is skipped if `!ok`. Then
`p, ok := canonicalGIF(FixedWidthSmall.URL)`, with `p = u` if `!ok`. The loop stops at `gifLimit`
items. The result starts as `make([]gif, 0, gifLimit)`, so an empty answer encodes as `[]`, not `null`.
Successful answers are cached, including empty ones. Errors are not cached.
**Alternatives considered**: Passing the client's original casing upstream. Different casings would
then share one cache slot that holds whichever casing came first, which is not deterministic.
**Rationale**: There are only three outcomes (200/400/502, plus 404 by absence). `c.JSON` gives the
same `application/json; charset=utf-8` that `/rooms` asserts. Sanitized errors make the "never log
the key" rule hold by construction, so it doesn't depend on every log call being careful.

### Decision: `canonicalGIF` re-validates and strips tracking

**Choice**:

```go
// canonicalGIF accepts only https URLs on imageHosts whose path ends in .gif, with no
// userinfo, port or opaque part, and returns them without query or fragment.
func canonicalGIF(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Opaque != "" || u.User != nil ||
		!slices.Contains(imageHosts, u.Host) || // exact: rejects "host:443", "host:", "HOST"
		!strings.HasSuffix(strings.ToLower(u.Path), ".gif") {
		return "", false
	}
	u.RawQuery, u.ForceQuery, u.Fragment, u.RawFragment = "", false, "", ""
	return u.String(), true
}
```

Matching `u.Host` exactly against `imageHosts` covers the port check as well (any `:` makes the
match fail). It also keeps the output acceptable to the page's `isAllowedImage`, which renders the message inline.
`String()` keeps `EscapedPath`, so the path round-trips.
**Alternatives considered**: Checking `u.Hostname()` and then `u.Port() == ""`. That accepts an
empty port (`host:`), which the browser normalizes differently. A regex is harder to read.
**Rationale**: The proxy is a trust boundary. Upstream data is relayed only after it passes the same
rules the page applies, and the server fails closed.

### Decision: Cache is a mutex-guarded map, and when full it evicts expired entries and then any one entry

**Choice**: `cached` locks `mu`, looks up the key and returns a hit only if `g.now().Before(e.expires)`.
`store` locks `mu`. If `len(cache) >= gifCacheMax` and the key is new, it deletes every expired
entry. If the map is still full, it deletes one arbitrary entry (the first in map iteration).
Then it inserts `{gifs, now+TTL}`. The lock is never held across the upstream call.
**Alternatives considered**: (a) An LRU with `container/list`. That's more code for 100 entries
in a friends chat. (b) `sync.Map`. It has no size bound. (c) Singleflight to collapse concurrent
misses on the same key. That's a rare, bounded duplicate call, so it's YAGNI.
**Rationale**: The cache exists to protect the shared key's quota. Its size is bounded and the code
is easy to read. This isn't the Room hub, so the "no locks" convention doesn't apply. The convention
covers the client registry only.

### Decision: Page states, debounce and stale-response guard

**Choice** (`app.js`, new block after the `#gif-send` handler, before `})();`):

```js
const GIF_DEBOUNCE_MS = 300;
const gifSearch = $('gif-search'), gifGrid = $('gif-grid'), gifStatus = $('gif-status');
let gifSeq = 0;          // id of the latest /gifs request; older responses are ignored
let gifTimer = 0;
let gifLoaded = false;   // a result list has rendered at least once
let gifSearchOff = false; // /gifs answered 404: key unset, use the paste box only

function openGifPanel()              // off → gifUrl.focus(); else gifSearch.focus(), and loadGifs(gifSearch.value.trim()) if !gifLoaded
function loadGifs(q)                 // seq = ++gifSeq; status 'Cargando…'; fetch('/gifs?q=' + encodeURIComponent(q))
                                     //   seq !== gifSeq → return. 404 → disableGifSearch(). !ok or bad JSON → showGifError()
                                     //   else renderGifs(body.gifs)
function renderGifs(list)            // keep items whose url and preview are strings passing isAllowedImage. replaceChildren with
                                     //   <button type=button class=gif-item aria-label="Enviar GIF"><img alt="" loading=lazy
                                     //   referrerPolicy=no-referrer decoding=async src=preview></button>. Click → if (sendText(url)) closePanels().
                                     //   Status: '' or 'Sin resultados'. gifLoaded = true
function showGifError()              // gifGrid.replaceChildren(); status 'No se pudieron cargar los GIF.'
function disableGifSearch()          // gifSearchOff = true; $('gif-search-box').hidden = true; if focus was in gifSearch → gifUrl.focus()
```

Hooks:

| Place | Change |
|-------|--------|
| `togglePanel` line `if (key === 'gif') $('gif-url').focus();` | becomes `if (key === 'gif') openGifPanel();` |
| `gifSearch` `input` | `clearTimeout(gifTimer); gifTimer = setTimeout(() => loadGifs(gifSearch.value.trim()), GIF_DEBOUNCE_MS)` |
| `gifSearch` `keydown` Enter | `e.preventDefault()` (the panel sits **inside `#send-form`**, so Enter would submit the draft), then clear the timer and `loadGifs` at once |
| `panels` map, `closePanels`, Escape, outside click | unchanged (`.popover` already covers clicks in the grid) |
| `updateSendState`, `#gif-url`, `#gif-send` | unchanged |

Every created button sets `type = 'button'`, because the default inside a form is `submit`. Grid
content uses `createElement` and properties only, never `innerHTML`. A failed first load leaves
`gifLoaded` false, so reopening the panel retries.
**Alternatives considered**: (a) `AbortController` to cancel superseded fetches. The sequence
counter is enough and also covers the JSON-parse step. (b) Probing `/gifs` on page load to decide
whether to show the box. That spends a Giphy call per visit even when the panel is never opened.
(c) Starting with the box hidden and revealing it on the first 200. Focus would then have to jump from
`#gif-url` to the search input mid-typing.
**Rationale**: The box is visible by default. A no-key deployment pays one "Cargando…" flash, once per page.

### Decision: Markup and CSS reuse the panel's patterns

**Choice** (`index.html`, first child of `#gif-panel`; the existing hint and row stay below):

```html
<div id="gif-search-box" class="gif-search">
  <input id="gif-search" class="input" type="search" placeholder="Buscá un GIF…" aria-label="Buscar GIF" maxlength="50">
  <div id="gif-status" class="gif-status" role="status"></div>
  <div id="gif-grid" class="gif-grid"></div>
  <div class="gif-credit">Powered by GIPHY</div>
</div>
```

The hint text becomes "O pegá la URL de un GIF de Giphy, Tenor o Imgur." `maxlength` counts UTF-16
units, and that count is always ≥ the rune count, so the page never sends a query the server rejects.

```css
.gif-search { display: flex; flex-direction: column; gap: var(--space-2); }
.gif-grid { display: grid; grid-template-columns: repeat(3, 1fr); gap: 4px; max-height: min(240px, 40vh); overflow-y: auto; }
.gif-item { aspect-ratio: 1; padding: 0; border: 0; border-radius: var(--radius-sm); overflow: hidden; cursor: pointer; background: var(--color-neutral-800); }
.gif-item img { display: block; width: 100%; height: 100%; object-fit: cover; }
.gif-status, .gif-credit { font-size: 12px; color: var(--color-neutral-400); }
.gif-status:empty { display: none; }
```

`[hidden] { display: none !important; }` (app.css:42) already beats `.gif-search`'s `display: flex`.
`max-height` in `vh` keeps the bottom-anchored popover usable on short viewports.
**Alternatives considered**: A Giphy logo image. It would be an extra asset, and the text line is
an accepted attribution form.
**Rationale**: Only existing tokens are used, there's nothing inline, and the CSP is unchanged.

## Data Flow

```
Page                    /gifs handler (gin goroutine)     gifSearch.cache (mu)      Giphy
 │ open panel / type ─300ms─▶ GET /gifs?q=Cat
 │                        trim, ≤50 runes? (no → 400)
 │                        key="cat" ─── cached("cat") ───▶ hit & fresh? ──┐
 │◀── 200 {"gifs":[…]} ◀────────────────────────────────── yes ───────────┘
 │                        miss → fetch(ctx,"cat") ───────────────────────────▶ GET /v1/gifs/search?api_key&q=cat&limit=24&rating=pg-13&lang=es
 │                                                                            (5 s timeout, ≤1 MiB)
 │                        non-200 / error / bad JSON ◀─────────────────────── │
 │◀── 502 {"error":"unavailable"}  (log: "giphy status 500" — no URL, no key)
 │                        200 → canonicalGIF each, skip bad, cap 24
 │                        store("cat", gifs) ── evict expired/any if full ─▶
 │◀── 200 {"gifs":[{url,preview}]}
 │ seq === gifSeq? render <button><img src=preview> ; click → sendText(url) → WS chat/dm
 │ 404 (no key: route absent) → hide #gif-search-box, paste box only
```

## Testing

New `cmd/chatroom/gifs_test.go`. The helpers:

- `fakeGiphy(t, h http.HandlerFunc) *httptest.Server`, which records each `*http.Request` (under a
  mutex, or by sending on a channel) so tests can count upstream calls and inspect path and query.
- `newGifTestServer(t, g *gifSearch) *httptest.Server`, which returns
  `httptest.NewServer(newRouter(newChatServer(nil), g))`. `/gifs` doesn't need the rooms running.
- `giphyJSON(urls ...[2]string) string`, which builds `{"data":[{"images":{"fixed_height":{"url":…},"fixed_width_small":{"url":…}}}]}`.

| Test | Spec (SRV-08) | Asserts |
|------|---------------|---------|
| `TestGifsDisabledWithoutKey` | key unset → 404 | `newTestServer` (nil search): `GET /gifs` and `/gifs?q=x` → 404 |
| `TestGifsTrending` | trending | `q` absent, `""` and `"   "` → path `/v1/gifs/trending`, query has `api_key=K`, `limit=24`, `rating=pg-13`, no `q`, no `lang`. 200, exact body, SRV-07 headers |
| `TestGifsSearchParams` | search, fixed params | `?q=%20Cat%20&limit=99&rating=r&api_key=x` → path `/v1/gifs/search`, upstream `q=cat`, `lang=es`, `limit=24`, `rating=pg-13`, `api_key=K` (client params are not forwarded) |
| `TestGifsQueryTooLong` | 400 | 50 runes `ñ…` → 200. 51 runes → 400 `{"error":"query_too_long"}`, and zero upstream calls |
| `TestGifsUpstreamError` | 502 | fake returns 500 → 502 `{"error":"unavailable"}`. Fake returns `not json` → 502. Fake returns a body over 1 MiB → 502 |
| `TestGifsUpstreamTimeout` | 502 on timeout | `g.client = &http.Client{Timeout: 50 * time.Millisecond}`. The fake blocks on `<-r.Context().Done()` or a `release` channel closed in Cleanup → 502 |
| `TestGifsKeyNeverLeaks` | never log key | `log.SetOutput(&buf)` (restored in Cleanup). Cases: base URL of a closed httptest server (transport error, the `*url.Error` path), a 500, and a fake item URL `…/a.gif?api_key=SECRET`. Neither `buf` nor any response body contains `SECRET` |
| `TestGifsFiltersAndCanonicalizes` | re-validate, strip query | items: valid `https://media2.giphy.com/media/x/200.gif?cid=1&rid=2#f` → `url` without `?…#…`. Bad preview → `preview == url`. Skipped: `http://`, `evil.com`, `.mp4`, `user@host`, `host:443`. Exact JSON `{"gifs":[…]}`. All bad → `{"gifs":[]}` |
| `TestGifsCap` | limit 24 | fake returns 30 valid items → 24 relayed |
| `TestCanonicalGIF` | URL rules | table: the accept/reject rows above plus `HTTPS://`-scheme casing, an `.GIF` suffix, an opaque URL, `media.giphy.com:` |
| `TestGifsCache` | cache | `Cat` then `cat` → one upstream call, same body. Advance `g.now` past 10 min → second call. An error isn't cached (500, then 200 → two calls) |
| `TestGifsCacheBound` | max 100 | `store` 100 keys, then 1 more → `len(cache) == 100`. With some entries expired → those are evicted first, and the fresh ones stay |
| `TestGifsConcurrent` | concurrency | 20 goroutines hit 5 keys under `-race` → all 200 |
| `TestIndexHasGifPicker` (main_test) | UI | `index.html` has `id="gif-search-box"`, `id="gif-search"`, `id="gif-grid"`, `Powered by GIPHY`, and still `id="gif-url"`, `id="gif-send"`. Search box index < `id="gif-url"` |
| existing suites | SRV-01…07 | Only the 4 call-site edits. The CSP string test and `TestImageHostsMirroredInJS` are unchanged and green |

Gates: `/snap/go/current/bin/gofmt -l .`, `go vet ./...`, `go test -race ./...`.

Manual checklist: (1) the key is set and opening the panel shows trending, with "Cargando…" first. (2) Typing
"gato" queries once, 300 ms after the last key. Typing fast and then deleting never shows stale
results. (3) Enter in the search box doesn't send the draft. (4) A click sends the GIF in the room
and in a DM, and the panel closes. (5) "zzzzqqq" shows "Sin resultados". Giphy blocked (a wrong key) shows
"No se pudieron cargar los GIF". (6) Without a key, the search box disappears, focus goes to `#gif-url` and paste still
works. (7) Both skins at 480 px: the grid scrolls inside the popover. (8) Devtools: no CSP violations,
and image requests carry no `Referer`.

## File Changes

| File | Action | Description |
|------|--------|-------------|
| `cmd/chatroom/gifs.go` | Create | Constants, `gif`, `gifCacheEntry`, `gifSearch`, `newGifSearch`, `handle`, `fetch`, `cached`/`store`, `canonicalGIF` |
| `cmd/chatroom/gifs_test.go` | Create | Fake Giphy helpers and the tests above |
| `cmd/chatroom/main.go` | Modify | `config` struct; `main` reads `GIPHY_API_KEY`; `serve`/`run` take `config`; `run` builds `*gifSearch` or logs "disabled"; `newRouter(chatSrv, gifs)` registers `/gifs` when non-nil |
| `cmd/chatroom/main_test.go` | Modify | The 4 call sites; `TestIndexHasGifPicker` |
| `cmd/chatroom/index.html` | Modify | `#gif-search-box` (input, status, grid, credit) above the hint; hint text "O pegá…" |
| `cmd/chatroom/app.js` | Modify | GIF search block (`GIF_DEBOUNCE_MS`, `gifSeq`, `openGifPanel`, `loadGifs`, `renderGifs`, `showGifError`, `disableGifSearch`, input/Enter listeners). `togglePanel` calls `openGifPanel()` |
| `cmd/chatroom/app.css` | Modify | `.gif-search`, `.gif-grid`, `.gif-item`, `.gif-status`, `.gif-credit`; a chadroom-skin border for `.gif-item` only if needed |
| `README.md` | Modify | `GIPHY_API_KEY` in the config table; a `GET /gifs?q=` row in the API table (200/400/404/502) |
| `openspec/specs/server/spec.md` | Modify at archive | Add SRV-08 and update Purpose |

## Open Risks

- **Search terms in access logs.** gin's default logger prints the inbound path **with the query**,
  so users' search text ends up in stdout. The key does not, because it's never in the inbound
  request. This is accepted for a friends chat. Filtering the logger is a separate change.
- **Quota and abuse.** `/gifs` has no auth. The cache plus the debounce cut the call volume, but distinct
  queries still reach Giphy. General rate limiting stays with the backlog item `rate-limiting`.
- **Duplicate misses.** Concurrent first requests for the same key each call Giphy (no singleflight).
  This is bounded and rare.
- **Viewer IPs reach Giphy** for up to 24 thumbnails per open. That's the same exposure as inline GIFs,
  and a proxy is out of scope.
- **Giphy shape drift.** If `fixed_height.url` disappears or changes host, items are skipped and the
  page shows "Sin resultados". The system fails closed, and `imageHosts` is the single place to widen.
- **No JS tests.** Debounce, the stale-response guard and the 404 fallback rely on the manual checklist.
