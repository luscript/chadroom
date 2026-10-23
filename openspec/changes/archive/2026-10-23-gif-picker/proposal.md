# Proposal: GIF picker

## Intent

Let users search Giphy (and browse trending) from the existing GIF panel and send a GIF with
one click, in rooms and DMs. The Giphy API key stays on the server (`GIPHY_API_KEY`). This is
the `gif-picker` backlog group.

## Decisions

Orchestrator decisions (auto mode, 2026-10-23):

- **Endpoint `GET /gifs?q=<text>`.** `q` is trimmed. Empty or absent means **trending**, and
  otherwise it is a **search**. `q` longer than 50 characters (runes) → **400**. The server
  calls Giphy with fixed parameters: `limit=24`, `rating=pg-13`, and `lang=es` for search.
  The client cannot pass any other parameter through.
- **Trimmed response.** `200 {"gifs":[{"url":"…","preview":"…"}]}`. `url` is the rendition
  sent in chat (`fixed_height`) and `preview` is the thumbnail (`fixed_width_small`, falling
  back to `url`). The server **re-validates** each URL: https, host on `imageHosts`, path
  ending in `.gif`, no userinfo or port. It **drops query and fragment** (Giphy tracking
  params) and skips any item that fails. Headers match SRV-07 (JSON, `nosniff`,
  `Cache-Control: no-cache`).
- **Key unset → 404** on `/gifs`, and the server never calls Giphy. The page learns this from
  its first fetch: on 404 it hides the search UI and keeps only the paste box. There is no
  extra config endpoint (YAGNI).
- **Keep the paste-a-URL box.** It still covers Tenor/Imgur and works without a key. The
  picker goes above it in the same `#gif-panel`.
- **Failure handling.** Upstream error, non-200 or timeout → **502** `{"error":"unavailable"}`.
  The outbound `http.Client` has a 5 s timeout, the body is read through `io.LimitReader`
  (1 MiB), and only the needed fields are decoded. Logs contain the upstream status code or a
  generic message, **never the URL or the key**.
- **Small server cache.** In-memory TTL cache keyed by the lowercased query ("" = trending),
  10 min TTL, at most 100 entries. It is guarded by its own mutex: this is not the Room hub,
  so the "no locks" convention does not apply. Its job is to protect the shared key's quota,
  and it is not general rate limiting (still backlog `rate-limiting`).
- **Config wiring.** `GIPHY_API_KEY` is read in `main()` next to `ALLOWED_ORIGINS`. The Giphy
  base URL is injectable so tests use an `httptest` fake. No new dependency.
- **CSP unchanged.** `connect-src 'self'` and the existing Giphy hosts in `img-src` already
  cover this.
- **UI.** In `#gif-panel`: a search `<input type="search">` ("Buscá un GIF…"), a `#gif-grid`
  of `<button><img></button>` and a "Powered by GIPHY" text line. Opening the panel loads
  trending once. Typing re-queries with a 300 ms debounce, and a sequence counter discards
  stale responses. The page shows "Cargando…", "Sin resultados" and "No se pudieron cargar
  los GIF" states. A result's `url` and `preview` are rendered only if `isAllowedImage`
  passes, and images use `loading=lazy` and `referrerPolicy=no-referrer`. A click calls
  `sendText(url)` then `closePanels()`. Focus goes to the search input when it is shown,
  otherwise to `#gif-url`.

## Affected Areas

| Area | Files | Change |
|------|-------|--------|
| Server / Giphy client | `cmd/chatroom/gifs.go` (new), `cmd/chatroom/main.go` | `/gifs` handler, Giphy client, cache, config wiring |
| Page | `cmd/chatroom/index.html`, `app.js`, `app.css` | search input, results grid, attribution, states |
| Tests | `cmd/chatroom/gifs_test.go` (new), `main_test.go` | handler vs fake Giphy, cache, validation, no-key 404 |
| Docs / specs | `README.md`, `openspec/specs/server/spec.md` | env var + API rows; SRV-08 added, Purpose updated |

## Out of scope

- A server-side image proxy (viewer IPs still reach Giphy, as with inline GIFs today).
- Tenor search, pagination / infinite scroll, favorites.
- General rate limiting (backlog `rate-limiting`).

## Rollback

Revert the change's commit(s). If `GIPHY_API_KEY` is unset, the feature disables itself and
the page falls back to the paste box. No persisted data.
