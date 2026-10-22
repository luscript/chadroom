# Proposal: Frontend hardening

## Intent

The chat page has three gaps left after the Nocturne redesign and `structured-messages`:
(1) if the WebSocket drops (server restart, Wi-Fi blip, laptop sleep) the page shows
"Desconectado" forever and the user must leave and re-join by hand; (2) there is no
Content-Security-Policy, so any future HTML-injection bug would run script freely;
(3) any participant can send an `https://…/x.gif` URL from a host they control and every
other browser fetches it, leaking viewers' IPs (and online times) to that host. This change
closes those gaps and fixes the stale README note. Backlog group `frontend-hardening`
(#13, #15, #16, CSP, inline images).

## Exploration summary (2026-10-22)

Already resolved by the redesign — only verified, not re-implemented:

| Backlog item | Status in `cmd/chatroom/index.html` |
|---|---|
| #13 WS URL from `location.host`, `wss:` on HTTPS | Done (`join()`, line ~289). README line ~19 still says the opposite. |
| #15 Cancelled `prompt` → nickname `null` | Gone: `prompt()` replaced by a join form with `trim()`. |
| #16a `readyState` check before `send` | Done in `sendText()`. |
| #16b Reconnect with backoff | **Missing.** `onclose` sets `offline` and stops. |
| CSP | **Missing.** Inline `<script>`, inline `<style>`, two `style=""` attributes; no `onclick` left. Page loads Google Fonts (CSS from `fonts.googleapis.com`, fonts from `fonts.gstatic.com`). |
| Inline images leak IPs | **Missing.** `IMAGE_URL` accepts any `https` host. |

## Decisions (taken with the user, 2026-10-22)

- **Images: host allowlist.** Only image URLs whose host is on a fixed allowlist render
  inline; any other URL is shown as plain text. The allowlist is enforced twice: in the
  page's JS (render decision) and in CSP `img-src` (browser-enforced backstop).
- **GIF picker is a separate change** (`gif-picker`, added to the backlog): a GIF search
  panel like the emoji one, backed by a server endpoint that calls Giphy with
  `GIPHY_API_KEY` from the environment (backend-for-frontend, key never reaches the
  browser). This change only prepares the ground: the allowlist includes Giphy's media hosts.
- **CSP via external files, not hashes**: the inline script and style move to embedded
  `app.js` / `app.css` served by the Go binary, so the policy is `'self'` and needs no
  maintenance when the code changes.

## Scope

### In Scope
- **Reconnect with backoff** (#16b): on an unexpected close the page retries automatically
  with exponential backoff and jitter (≈1 s doubling to a 30 s cap, reset after a
  successful open), shows `Reconectando…` in the status pill and a single system notice per
  outage, and re-requests the nickname the user originally typed. No retry after the user
  clicks "Salir". Sending stays disabled while not `online` (already the case).
- **CSP on `GET /`**: move inline `<script>` → `/app.js` and `<style>` → `/app.css` (both
  `go:embed`ded, correct `Content-Type`, `nosniff`); replace the two `style=""` attributes
  with classes. Proposed policy (final form in design):
  `default-src 'none'; script-src 'self'; style-src 'self' https://fonts.googleapis.com;
  font-src https://fonts.gstatic.com; img-src <allowlisted hosts>; connect-src 'self';
  base-uri 'none'; form-action 'none'; frame-ancestors 'none'`.
- **Image host allowlist**: initial list — Giphy media hosts (`media.giphy.com`,
  `media0–4.giphy.com`, `i.giphy.com`), `media.tenor.com`, `i.imgur.com`. Parsed with
  `new URL()` (exact host match, `https:` only), not a regex on the whole string. Pasting a
  URL in the existing GIF panel keeps working; a non-allowlisted URL is sent and shown as
  text.
- **README**: remove the "use `localhost`, not `127.0.0.1`" note; document the allowlist and
  the reconnect behavior.
- Spec deltas: SRV-02 and SRV-05 (server), CHAT-14 or new CHAT requirements for reconnect and
  images (chat). Go tests for the new routes and headers.

### Out of Scope
- GIF search panel / Giphy integration → `gif-picker`.
- Server-side image proxy (hides IPs from Giphy too, but adds SSRF surface; not worth it for
  a friends-only chat).
- Message history after reconnect (messages sent while offline are lost) → `chat-features`
  (last-N history).
- Rate limiting, TLS, `GIN_MODE`, DNS rebinding → `rate-limiting`, `ops-hardening`.
- Self-hosting Inter (would drop Google from CSP; optional follow-up).

## Approach

1. **Split assets**: `cmd/chatroom/static/{index.html,app.js,app.css}` (or flat in
   `cmd/chatroom/`), embedded with `go:embed`; gin serves `GET /app.js` and `GET /app.css`
   explicitly (two routes, no directory listing).
2. **Headers in one place**: a small helper sets `nosniff` on every static response and the
   CSP + `X-Frame-Options` on the page. The CSP string is built from the same host list the
   JS uses — one Go slice rendered into the header; the JS keeps a mirrored constant with a
   comment pointing to it (same pattern as `MAX_MESSAGE_BYTES`).
3. **Reconnect state machine in JS**: `connect()` extracted from `join()`; `onclose` schedules
   `connect()` unless the user left; a `leaving` flag / socket identity check (already used)
   prevents stale sockets from reconnecting.
4. **Image rendering**: `isAllowedImage(text)` replaces `IMAGE_URL.test(text)` for inline
   rendering; the GIF panel's enable check uses the same function.

## Constraints

- No new Go dependencies; no build step for the frontend (plain files, vanilla JS).
- Keep the single-binary property: all assets embedded.
- The page must still work at any host/port (`localhost`, `127.0.0.1`, LAN IP) — CSP
  `connect-src 'self'` covers same-origin `ws:`/`wss:` in current browsers.

## Affected Areas

| Area | Files | Change |
|------|-------|--------|
| Server | `cmd/chatroom/main.go` | embed + routes for `app.js`/`app.css`, CSP header |
| Frontend | `cmd/chatroom/index.html` → + `app.js`, `app.css` | split, reconnect, allowlist |
| Tests | `cmd/chatroom/main_test.go` | asset routes, content types, CSP present on `/` |
| Docs | `README.md`, `openspec/backlog.md` | stale note, new `gif-picker` group |
| Specs | `openspec/specs/server/spec.md`, `openspec/specs/chat/spec.md` | SRV-02, SRV-05, page behavior |

## Risks

| Risk | Likelihood | Mitigation |
|------|------------|------------|
| CSP blocks something the page needs (fonts, SVG `fill="var(...)"`, `ws:`) → blank or unstyled page | Medium | Manual check in Chrome + Firefox with DevTools console open; Go test asserts the exact policy |
| Reconnect while the server still holds the old connection (pong timeout) assigns `nick (2)` | Medium | Accepted: `welcome` already updates the identity; document. Old conn dies within the pong window |
| Reconnect storm when the server restarts with many tabs | Low | Jitter + 30 s cap; friends-only scale |
| Allowlist too narrow (a friend's favourite GIF host) | Low | One constant in Go + JS; easy to extend |
| Old cached `index.html` without `app.js` | Low | Single binary, page reload fixes it |

## Rollback Plan

Frontend-only behavior plus two static routes and one header, no persisted data: revert the
change's commits and redeploy. If only CSP misbehaves in production, the header line can be
removed on its own without touching the rest.

## Dependencies

- Builds on `structured-messages` (merged into `main`, PR #2, `934f368`).
- Branch `feat/frontend-hardening` from `main`.
- `openspec/project-map.yaml` is stale (missing `openspec` module); refresh before design.

## Success Criteria

- [ ] Killing and restarting the server reconnects open tabs automatically, without a reload, and they can chat again.
- [ ] Clicking "Salir" never triggers a reconnect.
- [ ] `GET /` sends a CSP with no `'unsafe-inline'`; the page renders and works with no CSP violations in the console.
- [ ] A Giphy GIF URL renders inline; `https://evil.example/x.gif` renders as text and the browser makes no request to `evil.example`.
- [ ] The page works when opened at `127.0.0.1:8080`, and the README no longer says otherwise.
- [ ] `go vet ./...` and `go test -race ./...` pass.
