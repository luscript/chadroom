# Exploration: gif-picker

Synthesized by the orchestrator from two crombie-explore agents (backend, frontend), 2026-10-23.
Source backlog group: `gif-picker`.

## Key findings

- **Backend.** `newRouter(chatSrv *chat.Server) *gin.Engine` (`cmd/chatroom/main.go:105`) registers
  `/`, `/app.js`, `/app.css`, `/rooms`, `/ws`. Env is read only in `main()`
  (`serve(ctx, addr, os.Getenv("ALLOWED_ORIGINS"))`) and threaded `serve → run`. There is no
  outbound HTTP client in production code, so the Giphy client is new and needs only stdlib.
- **CSP already fits.** `buildCSP(imageHosts)` yields `connect-src 'self'` and `img-src` with
  `media.giphy.com`, `media0-4.giphy.com` and `i.giphy.com`. A same-origin `GET /gifs` plus
  Giphy thumbnails need **no CSP or host-list change**. `TestIndexSecurityHeaders` hardcodes
  the CSP string, and `TestImageHostsMirroredInJS` compares Go and JS host lists.
- **Tests.** `newTestServer(t)` (`main_test.go:20`) wraps `newRouter`, and `TestRoomsEndpoint`
  is the template for a JSON endpoint. To test `/gifs`, a fake Giphy (`httptest`) URL must be
  injectable.
- **Frontend today.** `#gif-btn`/`#gif-panel` (`index.html:77-87`) is a **paste-a-URL** box
  (`#gif-url`, `#gif-send`, validated by `isAllowedImage`). Panels use the `panels` map plus
  `togglePanel`/`closePanels` (`app.js:715-744`) with Escape, outside click and
  `aria-expanded`. Sending goes through `sendText(text)` (`app.js:672-703`), which already
  handles rooms vs DMs and `MAX_MESSAGE_BYTES`.
- **Rendering.** A message renders inline only if the whole text is one https URL on
  `IMAGE_HOSTS`, with no port/userinfo/hash, and a path ending in `.gif|.webp|.png|.jpe?g`
  (query allowed). So Giphy `media*.giphy.com/media/<id>/<file>.gif` URLs render.
- **Conventions.** `createElement` + `textContent` only (no `innerHTML`), `replaceChildren`
  for grids (`.emoji-grid` pattern), Spanish voseo UI strings, kebab-case ids.

## Relevant files

| Path | Why |
|------|-----|
| `cmd/chatroom/main.go` | router, env wiring, CSP, `imageHosts`, http.Server |
| `cmd/chatroom/main_test.go` | `newTestServer`, CSP string test, host mirror test |
| `cmd/chatroom/index.html` | `#gif-panel` markup |
| `cmd/chatroom/app.js` | `isAllowedImage`, `sendText`, panels, `updateSendState` (`:668` touches `#gif-url`/`#gif-send`) |
| `cmd/chatroom/app.css` | `.popover.gif`, `.emoji-grid` pattern, breakpoints |
| `openspec/specs/server/spec.md` | SRV-05 (CSP), SRV-07 (JSON endpoint model) → new SRV-08 |
| `README.md` | config table (env vars) and API table |

## Risks

- **Key leakage.** `*url.Error` includes the full URL with `api_key`. Never log or return raw
  upstream errors or URLs.
- **Quota.** The endpoint has no auth, and each call becomes a Giphy call (beta keys are
  rate-limited). Mitigate with client debounce and a small server TTL cache.
- **Untrusted upstream data.** Re-validate every URL (https, allowlisted host, gif path)
  before relaying it, and render only via `img.src`/`textContent`.
- **Size.** Use `io.LimitReader` on the upstream body, an explicit client timeout, and trimmed
  JSON.
- **Frontend breakage.** Removing `#gif-url`/`#gif-send` without updating `updateSendState`
  throws. Keep them.
- **Privacy.** Thumbnails load from Giphy, which leaks viewers' IPs (same exposure as inline
  GIFs today, but many images at once). Accepted for a friends chat; a proxy is out of scope.
- **Layout.** The popover is bottom-anchored, so the grid needs `max-height` + scroll on short
  viewports.

## Gaps / open questions (resolved in proposal)

How the page learns that the key is unset; replace vs keep the paste box; renditions; rating;
caching.
