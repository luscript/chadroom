# Tasks: GIF picker

Source: `proposal.md`, `specs/server/spec.md` (SRV-08, SRV-05 scenario), `design.md`.

Batching: group A (server: `gifs.go`, `gifs_test.go`, `main.go`, `main_test.go`, `README.md`)
and group B (page: `index.html`, `app.js`, `app.css`) touch disjoint files and run in parallel.
`TestIndexHasGifPicker` (A) asserts ids that B adds; both land before verify.

## 1. Infrastructure (group A)

- [x] 1.1 `main.go`: add `config{allowedOrigins, giphyKey}`; `main` reads `GIPHY_API_KEY`;
      `serve(ctx, addr, cfg)` / `run(ctx, ln, cfg)`; `run` builds `*gifSearch` when the key is
      set (else logs that GIF search is disabled, without the key); `newRouter(chatSrv, gifs)`
      registers `GET /gifs` only when `gifs != nil`.
- [x] 1.2 `main_test.go`: update the 4 call sites (`newTestServer` → `nil`, two `run`, one
      `serve` → `config{}`).

## 2. Implementation

- [x] 2.1 (A) `gifs.go`: `gifSearch` (baseURL, key, 5 s `http.Client`, `now`, cache + mutex),
      `newGifSearch`, `handle` (trim, 50-rune check → 400 `query_too_long`, lowercase, cache,
      fetch, 502 `unavailable`, SRV-07 headers, `{"gifs":[]}` not `null`), `fetch` (trending vs
      search, fixed `url.Values` params, 1 MiB `LimitReader`, sanitized errors only — never the
      transport error / URL), `canonicalGIF`, cache `cached`/`store` (10 min TTL, 100 entries,
      evict expired then any; no lock held during the upstream call; errors not cached),
      cap 24 results.
- [x] 2.2 (B) `index.html`: `#gif-search-box` (`#gif-search` `type=search` `maxlength=50`
      "Buscá un GIF…", `#gif-status` `role=status`, `#gif-grid`, "Powered by GIPHY") above the
      existing hint; hint text becomes "O pegá…"; keep `#gif-url` / `#gif-send`.
- [x] 2.3 (B) `app.js`: `GIF_DEBOUNCE_MS = 300`, `gifSeq`, `gifLoaded`, `openGifPanel`,
      `loadGifs`, `renderGifs` (createElement only, `type=button`, `isAllowedImage` on url and
      preview, `loading=lazy`, `referrerPolicy=no-referrer`, click → `sendText(url)` +
      `closePanels()`), `showGifError`, `disableGifSearch` (on 404; focus `#gif-url`);
      input listener with debounce; Enter in `#gif-search` calls `preventDefault` (panel is
      inside `#send-form`); `togglePanel` calls `openGifPanel()`. States "Cargando…",
      "Sin resultados", "No se pudieron cargar los GIF".
- [x] 2.4 (B) `app.css`: `.gif-search`, `.gif-grid` (grid, `max-height`, `overflow-y: auto`),
      `.gif-item`, `.gif-status`, `.gif-credit`, existing tokens only; grid scrolls at 480 px.
- [x] 2.5 (A) `README.md`: `GIPHY_API_KEY` row in the config table; `GET /gifs?q=` row in the
      API table (200 / 400 / 404 / 502).

## 3. Testing (group A)

- [x] 3.1 `gifs_test.go`: helpers `fakeGiphy`, `newGifTestServer`, `giphyJSON`; tests
      `TestGifsDisabledWithoutKey`, `TestGifsTrending`, `TestGifsSearchParams`,
      `TestGifsQueryTooLong`, `TestGifsUpstreamError`, `TestGifsUpstreamTimeout`,
      `TestGifsKeyNeverLeaks`, `TestGifsFiltersAndCanonicalizes`, `TestGifsCap`,
      `TestCanonicalGIF`, `TestGifsCache`, `TestGifsCacheBound`, `TestGifsConcurrent`.
- [x] 3.2 `main_test.go`: `TestIndexHasGifPicker`.
- [x] 3.3 Gates: `/snap/go/current/bin/gofmt -l .` empty, `go vet ./...`, `go test -race ./...`.
