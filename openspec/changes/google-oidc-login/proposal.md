# Proposal: google-oidc-login

## Intent

The chat is for remote coworkers, but today anyone who reaches the URL can join with any nickname.
Restrict access to Google Workspace accounts of the company domain (`crombie.dev`) using OpenID
Connect against Google, and derive the chat identity from the authenticated session instead of a
free-form nickname.

Rejected alternatives (decided before this change): a rotating shared code and own
username/password accounts — more work and more attack surface.

## Scope

**In scope**
- Configuration via env vars (documented in README): `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET`,
  `OIDC_REDIRECT_URL` (default `http://localhost:8080/auth/callback`), `ALLOWED_EMAIL_DOMAIN`
  (default `crombie.dev`), `SESSION_SECRET` (≥ 32 bytes), `SESSION_TTL` (default `12h`).
  Missing client id/secret or a missing/short session secret → the process refuses to start with a
  clear error. Auth is never silently disabled and no secret is generated at random.
- `GET /auth/login`, `GET /auth/callback`, `POST /auth/logout` using `golang.org/x/oauth2` +
  `github.com/coreos/go-oidc/v3` (issuer `https://accounts.google.com`): random state + nonce
  (`crypto/rand`) in a short-lived signed cookie, PKCE (S256), ID token verified by go-oidc
  (signature, issuer, audience, expiry) plus nonce check.
- Server-side authorization: `email_verified == true`, email domain part equal to
  `ALLOWED_EMAIL_DOMAIN` (exact, after the last `@`, case-insensitive), `hd` claim equal to the
  domain. Any failure → 403 with a generic page. The `hd` URL parameter is only a UX hint.
- Access/refresh tokens are discarded; only email and name are kept.
- Session: HMAC-SHA256-signed cookie (session id, email, name, expiry), `HttpOnly`,
  `SameSite=Lax`, `Path=/`, `Secure` iff `OIDC_REDIRECT_URL` is `https`. Logout clears the cookie and
  revokes its session id in an in-memory revocation set until it would have expired.
- Post-login redirect only to own relative paths (open-redirect safe; default `/`).
- Protection: `GET /`, `/app.js`, `/rooms`, `/gifs`, `/playlist`, `/ws` require a valid session.
  Pages → 302 to `/auth/login`; API and WebSocket → 401 (before the upgrade). Public: `/auth/*`,
  `/app.css` and the favicons (needed by the auth result pages).
- Two small embedded static pages for the auth flow (no inline code): "access denied" (403) and
  "signed out" (with a link to sign in again — so logout doesn't instantly re-login via Google SSO).
- Basic per-IP rate limit on `GET /auth/login` and `GET /auth/callback` (429).
- Identity: the WebSocket nickname comes from the session (`name` claim, falling back to the email
  local part), normalized with the existing rules. `?nickname=` is ignored. Collisions (two tabs of
  the same account, two people with the same name) keep the existing `"<base> (n)"` suffixing
  (CHAT-12). Avatar and bio (user-profiles) stay user-editable and unchanged.
- Frontend: the nickname join form is removed; the page enters the chat directly. "Salir" becomes
  "Cerrar sesión" (POST `/auth/logout`, then go to the signed-out page). A 401 from `/rooms`,
  `/gifs` or `/playlist`, or repeated WebSocket reconnect failures confirmed by a 401 on `/rooms`,
  send the browser to `/auth/login`.

**Out of scope**
- Deployment, production domain, reverse proxy (Caddy), trusted-proxy configuration.
- Admin/moderation roles; persistence of users or sessions (state stays in memory).
- Closing already-open WebSockets of other tabs on logout or at session expiry (validated at
  upgrade only; reconnects re-check). Recorded in the backlog.
- Using the Google profile picture as avatar; showing emails in the UI.

## Approach

New package `internal/auth`: config validation, `Sessions` (sign/verify/revoke), OIDC handlers,
`Require(mode)` gin middleware that puts an `Identity{Email, Name}` in the request context, and a
small rate limiter. `internal/chat` stays auth-agnostic: `HandleWebSocket` reads the nickname from a
resolver injected into `NewServer` (production resolver reads the identity from the context; tests
keep their query-based resolver), so `chat` never imports `auth`. `newRouter` gets a nil-able auth
dependency so existing router tests keep passing; a dedicated test proves production wiring is never
nil. Hub pattern untouched.

## Affected Areas

| Area | Change |
|------|--------|
| `internal/auth/` (new) | config, session cookie, OIDC login/callback/logout, middleware, rate limit, tests with a fake OIDC provider |
| `internal/chat/server.go` (+ tests) | nickname resolver injected into `NewServer`; `?nickname=` no longer read in production |
| `cmd/chatroom/main.go`, `main_test.go` | env config + fail-fast, route groups (public / protected), auth pages, wiring |
| `cmd/chatroom/index.html`, `app.js`, new `denied.html`, `signed-out.html` | no join form, Cerrar sesión, 401 → login |
| `go.mod`/`go.sum`, `README.md`, `openspec/backlog.md` | new deps, config + API docs, follow-ups |

## Risks

- **Local http vs production https**: `Secure` is derived from the redirect URL scheme, so prod
  (https) cannot accidentally run without it; local http still works.
- **Production later**: add the https redirect URI in Google Cloud, review `ALLOWED_ORIGINS` and
  scheme/IP detection behind Caddy (rate limiter keys on the remote address; behind a proxy all
  requests share one IP until trusted proxies are configured).
- **Revocation is in memory**: a restart forgets revoked session ids, so a cookie logged out before
  a restart is valid again until its expiry. Acceptable for this scope; documented.
- **Name collisions / display-name spoofing**: Workspace names are admin-managed; duplicates get a
  suffix. Disambiguation by email is a backlog item.
- **Breaking change for local dev**: `go run ./cmd/chatroom` now needs a `.env` with the OIDC vars.

## Rollback plan

Revert the merge commit. No persisted data or migrations; sessions are cookies + memory only.
Google Cloud client can stay configured.
