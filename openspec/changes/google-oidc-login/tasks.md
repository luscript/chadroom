# Tasks: google-oidc-login

Source of truth: `specs/server/spec.md` (SRV-02, 06, 10–18), `specs/chat/spec.md` (CHAT-16, 20,
41, 42), `design.md`. Where spec and design differ, the spec wins.

Execution batches (file-independent groups run in parallel):
- **A** (parallel): 1.1 + 2.1–2.4 · 3.1 · 4.2
- **B**: 2.5
- **C** (parallel): 4.1 · 5.1–5.3

## 1. Infrastructure

- [x] 1.1 Add `golang.org/x/oauth2` and `github.com/coreos/go-oidc/v3` to `go.mod`/`go.sum`
  (`go get`, `go mod tidy`; no other new deps).

## 2. `internal/auth` (new package)

- [x] 2.1 `config.go`: `Config`, `LoadConfig(getenv)` with defaults and validation (SRV-10 table,
  errors name the variable, never the value), `SecureCookies()`. Tests: `TestLoadConfig` table.
- [x] 2.2 `cookie.go` + `session.go`: sealer (b64url(json).b64url(HMAC-SHA256), per-purpose
  derived keys, `hmac.Equal` before parse, 4096-byte cap), `Identity`, `IdentityFrom(ctx)`,
  `Sessions` (`NewSessions`, `Cookie`, verify, revoke + prune, injectable clock), cookie
  names/attributes (`__Host-`/`__Secure-` when Secure), `Require(Page|API)` middleware (302 to
  `/auth/login?next=` / 401 JSON `no-store`). Tests: tamper, expired, revoked, prune, flow-as-session,
  oversized, attributes, require modes (SRV-13, SRV-15).
- [x] 2.3 `ratelimit.go`: fixed-window per-IP limiter (20/min, shared login+callback), bounded map
  (4096, refuse new IPs when full), 429 + `Retry-After`, injectable clock. Test: `TestRateLimit`
  (SRV-17).
- [x] 2.4 `authtest/provider.go`: stdlib-only fake OIDC provider (discovery, JWKS RSA, authorize,
  single-use code + PKCE-validating token endpoint, hand-signed RS256 id_token, per-test claim
  overrides). Must not import `auth`.
- [x] 2.5 `oidc.go`: `Handler`, `New(ctx, cfg, denied)` (discovery against `cfg.Issuer`,
  verifier with clock), `Register(group)` for `GET /auth/login`, `GET /auth/callback`,
  `POST /auth/logout`, `GET /auth/signed-out` hook as designed; state/nonce/PKCE flow cookie;
  callback order and 400/403 split; `emailInDomain` (last `@`, exact, ASCII case-insensitive);
  `hd` + `email_verified`; `safeNext`; logout with Origin check + revocation; fixed-reason logs
  only. Tests: happy path, denied (domain incl. `evilcrombie.dev`, `crombie.dev.evil.com`,
  unverified, hd), bad request (state, nonce, missing/expired flow cookie, provider error,
  missing code, wrong aud, expired token), safeNext table, logout revokes + Origin, logs contain
  no secrets (SRV-11, 12, 14, 18).

## 3. `internal/chat`

- [x] 3.1 `server.go`: `NicknameFunc func(*http.Request) string` as a required `NewServer`
  parameter (nil panics); `HandleWebSocket` uses it and no longer reads `?nickname=`. Update the
  test call sites with a `queryNickname` helper; add `TestNicknameResolver` (resolver wins over
  `?nickname=`) (CHAT-41).

## 4. `cmd/chatroom`

- [x] 4.1 `main.go` (+ `main_test.go`, `gifs_test.go`, `playlist_test.go`): `auth.LoadConfig` in
  `main` (fail fast), `auth.New` in `run` (return 1 on failure), `newChatServer(origins,
  nickname)` with `identityNickname` (name → email local part), nil-able `*auth.Handler` in
  `newRouter`/`newRouterLogging`, public vs protected groups (SRV-02/15: `/` page mode, `/app.js`
  `/rooms` `/gifs` `/playlist` `/ws` API mode, session check before room/origin), embedded
  `denied.html`/`signed-out.html` served with auth-page CSP + `no-store`, `recoverPanics` replacing
  `gin.Recovery()`. Tests: `TestAuthRoutes`, `TestAuthPublicRoutes`, `TestAuthPageSecurityHeaders`,
  `TestWebSocketNicknameFromSession`, `TestRunRequiresSession`, `TestRunFailsWithoutAuthConfig`,
  recovery does not log cookies; existing tests adapted (constructor args, minted cookie,
  `start-error`).
- [x] 4.2 Frontend: `index.html` start view (no nickname form; `#leave-btn` id kept, label
  "Cerrar sesión"), new `denied.html` and `signed-out.html` (no inline code, use `/app.css` and
  `/favicon.svg`), `app.js`: remove `requestedNick`/nick form/`nickname=`, boot `loadRooms` →
  `enterChat`, start error + retry, `toLogin()` on 401 from `/rooms` `/gifs` `/playlist`, reconnect
  probe after 3 failed attempts (shared in-flight `/rooms` fetch), logout via fetch POST (CHAT-42),
  audio unlock on first pointerdown/keydown (CHAT-16, 20, 42).

## 5. Docs

- [x] 5.1 `README.md`: config table (6 vars, required/fail-fast, `.env` loading, Google Cloud
  redirect URI), API table (`/auth/*`, 302/401, `/ws` without `nickname`), nickname prose,
  architecture tree.
- [x] 5.2 `openspec/backlog.md`: follow-ups (close sockets on logout/expiry, revocation lost on
  restart, trusted proxies/rate limit behind Caddy, https redirect URI + `ALLOWED_ORIGINS` in prod,
  name disambiguation by email, Google avatar host).
- [x] 5.3 `CLAUDE.md`: structure (`internal/auth`, auth pages), commands (required env), conventions
  (never log cookies/tokens/codes, `newRouter` nil auth is test-only, custom recovery), change in
  progress `google-oidc-login`.

## 6. Login page instead of auto-redirect (user feedback after manual test)

- [x] 6.1 `GET /` without a valid session serves an embedded `login.html` (200, auth-page CSP,
  `no-store`, `Vary: Cookie`) with the former join-view look and "Iniciar sesión con Google" →
  `/auth/login` plus "Usar otra cuenta" → `/auth/login?switch=1`; with a session it serves the chat
  page (`Vary: Cookie` too). Remove `signed-out.html` and `GET /auth/signed-out`. app.js: logout
  success and every 401 navigate to `/`. Remove the now-unused `Require(Page)` mode if nothing uses
  it. Update tests and README/CLAUDE.md (SRV-02, SRV-15, SRV-16, CHAT-16, CHAT-20, CHAT-42).
