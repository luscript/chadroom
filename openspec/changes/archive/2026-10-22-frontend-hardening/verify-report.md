# Verify Report: frontend-hardening

**Date**: 2026-10-22 · **Round**: 1 · **Verdict**: PASS

Reviewed at `22e9827`; round-1 fixes applied on top (see below). Parallel run:
crombie-reviewer (static, spec scenarios), crombie-security (static, OWASP), crombie-worker
(build/vet/fmt/tests), plus the user's manual browser checklist.

## Checks

| Check | Result |
|---|---|
| `go build ./...`, `go vet ./...` | pass |
| `gofmt -l .` | clean |
| `go test -race ./...` | pass (`cmd/chatroom` 9 tests, `internal/chat`) — re-run after fixes: pass |
| Coverage | `cmd/chatroom` 84.1 %, `internal/chat` 96.6 % |
| `node --check cmd/chatroom/app.js` | pass (re-run after fixes) |
| Manual checklist M1–M11 (design), Chrome + Firefox | **all OK** (user, 2026-10-22) |
| Live `curl` smoke test of headers | skipped (port 8080 held by the user's test server; probing it was not permitted). Headers are asserted by `TestStaticAssets` / `TestIndexSecurityHeaders` via `httptest` |
| `govulncheck` | not installed (backlog `deps-cleanup`) |

## Spec coverage

| Requirement | Scenarios | Coverage |
|---|---|---|
| SRV-02 | open page, by IP, static assets, unknown path | Go tests (`TestServeIndex`, `TestWebSocketSameOriginNoAllowlist`, `TestStaticAssets`); `/static/` added in round 1 |
| SRV-05 | headers on page/assets, no inline code, works under policy | Go tests (`TestIndexSecurityHeaders` literal CSP, `TestStaticAssets`, `TestIndexHasNoInlineCode`) + M1/M2/M5 |
| CHAT-15 | allowlisted inline, unknown host, look-alike, surrounding text, GIF panel | code + M3/M4; list sync by `TestImageHostsMirroredInJS` |
| CHAT-16 | closed before welcome, server restart, backoff, Salir ×2, renamed | code + M6–M10 (no JS test infrastructure) |

No scenario NOT MET.

## Findings

No CRITICAL / HIGH. Security: CSP strict and complete (`default-src 'none'` covers
object/worker/manifest/frame), no `isAllowedImage` bypass (same WHATWG parser as `img.src`,
CSP `img-src` also checks redirects), no XSS sinks.

### Fixed in round 1

| Sev | Source | Issue | Fix |
|---|---|---|---|
| MEDIUM | reviewer | A retry socket stuck in `CONNECTING` (Wi-Fi back) was not helped by `online`/`visibilitychange`; could wait a minute+ | `reconnectNow` replaces it while `reconnecting` (detach, close, `connect()`) |
| LOW | reviewer | GIF panel send active while reconnecting (silent no-op); could send between `open` and `welcome` | `sendText` requires `online`; `updateSendState` drives `gif-send` |
| LOW | security | No `Referrer-Policy`: page origin (LAN IP) sent to Google Fonts | `Referrer-Policy: no-referrer` on `GET /` + test |
| LOW | reviewer | `/static/` 404 scenario not tested | added to `TestStaticAssets` |

Specs (SRV-05, CHAT-16) and design ("Verify round 1 adjustments") updated to match.

### Deferred to backlog (`frontend-polish`)

- Self-host Inter (Google Fonts sees every user's IP; breaks offline LAN use) → `font-src 'self'`.
- Reconnect never gives up on permanent failures (403 origin looks like 1006); ~2 req/min/tab at the cap — design-accepted.
- Trusted Types (`require-trusted-types-for 'script'`) to lock in "no HTML sinks".
- First join that fails shows "Conexión perdida…/Reconectado" without having been connected (wording).

Accepted, no action: allowlisted hosts accept anyone's uploads, but the uploader cannot see
viewers' IPs; large images only cost the viewer's bandwidth.
