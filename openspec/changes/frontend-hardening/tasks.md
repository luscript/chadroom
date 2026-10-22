# Tasks: Frontend hardening

> Spec ↔ design reconciled. Coverage: SRV-02 → asset split + routes (`serveAsset`, `no-cache`);
> SRV-05 → `buildCSP(imageHosts)`, `servePage`, no-inline-code guard; CHAT-15 →
> `IMAGE_HOSTS` / `isAllowedImage` + CSP `img-src` backstop + mirror test; CHAT-16 → reconnect
> state machine (`connect` / `scheduleReconnect` / `reconnectNow`, online on `welcome`).
> Spec adjustments made during reconciliation to match the design: `img-src` also allows
> `'self'` (favicon probe; same origin leaks nothing); "connected" = `welcome` received, not
> socket `open`; no terminal `offline` state (retries never stop); `Cache-Control: no-cache`
> added to SRV-02.

## Batch plan (file dependencies)

| Batch | Worker | Tasks | Files |
|-------|--------|-------|-------|
| 1 | W1 | 1.1–1.3, 2.1–2.3, 4.1–4.3 | cmd/chatroom/index.html, app.js, app.css (verbatim move only), main.go, main_test.go |
| 2 | W2 | 3.1–3.6, 4.4 | cmd/chatroom/app.js, app.css, index.html (GIF hint text), main_test.go (append one test) |
| 2 | orchestrator | 5.1–5.2 | README.md, openspec/project-map.yaml |

Batch 2 depends on batch 1: W2 edits the files W1 creates, and 4.4 needs `appJS` embedded.
Batch 1 moves code **verbatim** (no behavior change) so its diff is reviewable on its own and
the page must behave exactly as before under the new CSP.

## Phase 1: Infrastructure — split inline code into embedded assets

- [ ] 1.1 Create `cmd/chatroom/app.css` with the exact content of the inline `<style>` element; in `index.html` replace the element with `<link rel="stylesheet" href="/app.css">` placed after the Google Fonts `<link>`.
- [ ] 1.2 Create `cmd/chatroom/app.js` with the exact content of the inline `<script>` (same IIFE); in `index.html` replace it with `<script src="/app.js"></script>` at the same position (end of `<body>`).
- [ ] 1.3 Replace the two `style=""` attributes in `index.html` with classes per design: `subtitle` ("Sala general", `.chat-header .subtitle { font-size: 12px; }`) and `gif-hint` (`.gif-hint { font-size: 13px; line-height: 1.45; }`), rules added to `app.css`.

## Phase 2: Implementation — server (`cmd/chatroom/main.go`)

- [ ] 2.1 Embed `app.js` / `app.css` as `appJS` / `appCSS` `[]byte` next to `indexHTML`; add `setStaticHeaders` (nosniff + `Cache-Control: no-cache`), `serveAsset(contentType, body)` and routes `GET /app.js` (`text/javascript; charset=utf-8`) and `GET /app.css` (`text/css; charset=utf-8`).
- [ ] 2.2 Add `imageHosts` (design list, with the comment pointing to `IMAGE_HOSTS` and the mirror test), `buildCSP(hosts)` and package-level `contentSecurityPolicy`; output must equal the design's exact policy (directive order as in design).
- [ ] 2.3 Extract the `GET /` handler into `servePage`: static headers + `Content-Security-Policy` + `X-Frame-Options: DENY` + `index.html`.

## Phase 3: Implementation — page behavior (`cmd/chatroom/app.js`, `app.css`)

- [ ] 3.1 Allowlist (CHAT-15): replace `IMAGE_URL` with `IMAGE_HOSTS` (Set, design list, comment pointing to `imageHosts` in `main.go`), `IMAGE_PATH` and `isAllowedImage(text)` exactly as in the design; use it in `addChat` and in the GIF panel (`input` enable check and `gif-send` click guard).
- [ ] 3.2 GIF panel hint and placeholder in `index.html` mention the allowed hosts (e.g. "Pegá la URL de un GIF de Giphy, Tenor o Imgur.").
- [ ] 3.3 Reconnect state (CHAT-16): add `RETRY_BASE_MS`, `RETRY_MAX_MS`, `requestedNick`, `retryTimer`, `attempt`, `outage`; extract `connect()` from `join()` (keep the `ws !== socket` guard, no `onopen` handler); `join()` resets state, sets `connecting` and calls `connect()`.
- [ ] 3.4 `scheduleReconnect()` on unexpected `close` (`ws = null`, `reconnecting`, one "Conexión perdida, reconectando…" notice per outage, equal-jitter delay `d/2 + rand·d/2`, `d = min(30 s, 1 s·2^attempt)`); `welcome` sets `online`, resets `attempt`, and adds "Reconectado" if `outage`.
- [ ] 3.5 `reconnectNow()` on `window` `online` and `document` `visibilitychange` (visible): only short-circuits a pending timer, never opens a second socket, does not reset `attempt`. "Salir" clears `retryTimer` before closing the socket.
- [ ] 3.6 Status pill: `STATUS_LABEL` gets `reconnecting: 'Reconectando…'` and loses `offline`; in `app.css` replace the `offline` rule with the `reconnecting` rule + `pulse` animation + `prefers-reduced-motion` override from the design.

## Phase 4: Testing (`cmd/chatroom/main_test.go`, `go test -race ./...`)

- [ ] 4.1 `TestStaticAssets` (table `/`, `/app.js`, `/app.css`): 200, exact `Content-Type`, `nosniff`, `Cache-Control: no-cache`, body equals the embedded var. Also `GET /index.html` → 404.
- [ ] 4.2 Extend `TestIndexSecurityHeaders`: CSP equals the literal policy string from the design (written out in the test) and contains neither `unsafe-inline` nor `unsafe-eval`.
- [ ] 4.3 `TestIndexHasNoInlineCode` on `indexHTML`: no `<style`, no `style=`, every `<script` tag has `src=`, no `on[a-z]+=` attributes; references `src="/app.js"` and `href="/app.css"`.
- [ ] 4.4 `TestImageHostsMirroredInJS`: extract the `IMAGE_HOSTS` Set from `appJS` and compare (sorted) with `imageHosts`; fails on drift in either direction.
- [ ] 4.5 Run `go vet ./...`, `gofmt -l .` and `go test -race ./...` after each batch.

## Phase 5: Docs and wrap-up

- [ ] 5.1 `README.md`: drop the "usá `localhost`, no `127.0.0.1`" note; document the image allowlist (extend `imageHosts` + `IMAGE_HOSTS`, the test enforces sync), the CSP and automatic reconnection; list `/app.js` and `/app.css` in the API table.
- [ ] 5.2 Refresh `openspec/project-map.yaml` (stale: `openspec` module missing; new assets in `cmd/chatroom`).
- [ ] 5.3 Manual browser checklist M1–M11 from the design (Chrome + Firefox) — done by the user during verify; results recorded in `verify-report.md`.
