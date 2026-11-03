# Verify Report: google-oidc-login

**Verdict: PASS** — no open CRITICAL/HIGH findings; build, vet, gofmt and `go test -race ./...` green.
Pending before archive: manual end-to-end test with real Google accounts (see below).

## Automated checks (crombie-worker, HEAD 9f93e06)

| Check | Result |
|-------|--------|
| `go build ./...` | PASS |
| `go vet ./...` | PASS |
| `gofmt -l .` | PASS (empty) |
| `go test -race -count=1 ./...` | PASS — 618 `--- PASS`, 0 failures (cmd/chatroom, internal/auth, internal/auth/authtest, internal/chat) |
| `go test -race -count=3 ./...` | PASS (no flakiness) |
| `node --check cmd/chatroom/app.js` | PASS |
| Fail-fast smoke (binary with empty env / short secret) | NOT RUN — executing the binary was denied by the permission system; covered by `TestLoadConfig*` and `TestRunFailsWithoutAuthConfig`; included in the manual test |
| `govulncheck` | NOT RUN — not installed; go-jose v4.1.4 is past CVE-2025-27144 |

## Mandatory test list (from the task)

| Behaviour | Test |
|-----------|------|
| Allowed domain accepted | `TestCallbackHappyPath`, `TestCallbackHappyPathSteps`, `TestEmailInDomain` |
| Wrong domain incl. `evilcrombie.dev` rejected | `TestCallbackDenied/*`, `TestEmailInDomain` |
| `email_verified=false` rejected | `TestCallbackDenied/email_verified false`, `/missing`, `/string "true"` |
| Invalid state or nonce rejected | `TestCallbackBadRequest/state mismatch`, `/nonce mismatch` (token endpoint not called on bad state) |
| Tampered or expired cookie rejected | `TestSessionVerifyRejects`, `TestSessionExpired`, `TestRequireModes` |
| WebSocket without session → 401 | `TestAuthRoutes`, `TestWebSocketForeignOriginNoCookie401`, `TestRunRequiresSession` |
| Logout invalidates the session | `TestLogoutRevokes`, `TestSessionRevokeAndPrune` |

Existing router/CSP/image-host tests pass with unchanged assertions.

## Static review (crombie-reviewer) and security audit (crombie-security)

First pass: security 0 CRITICAL / 0 HIGH / 2 MEDIUM / 6 LOW; review 0 CRITICAL / 0 HIGH / 9 MEDIUM /
12 LOW. Fixed in ada3105, 8c73fa3, 9f93e06:

| Finding | Severity | Fix |
|---------|----------|-----|
| Rate-limit table exhaustion via IPv6 rotation; O(n) sweep when full | MEDIUM (sec) | IPv6 keyed by /64; sweep at most once per second |
| `http` redirect URL accepted for any host (no Secure cookies in prod by mistake) | MEDIUM (sec) | `http` only for loopback hosts |
| No account switch after logout/denied | LOW (sec) | `/auth/login?switch=1` → `prompt=select_account`; auth pages link to it |
| `newRouter(nil)` fail-open | MEDIUM (rev) | explicit `guards`, panics if incomplete; tests use `openGuards()` |
| CHAT-41 "non-empty after normalization" not honoured | MEDIUM (rev) | `chat.CleanNickname` used by `identityNickname` |
| Callback `next` re-validation test could not fail | MEDIUM (rev) | real flow test, confirmed failing without `safeNext` |
| No test for 401 before origin 403 on `/ws` | MEDIUM (rev) | `TestWebSocketForeignOriginNoCookie401` |
| No tests for frontend session handling | MEDIUM (rev) | string-guard tests `TestAppJSAuthFlow`, `TestIndexHasSessionMarkup` |
| README broken table, stale "Salir", incomplete API docs | MEDIUM (rev) | fixed |
| Auth tests in the 1200-line `main_test.go` | MEDIUM (rev) | moved to `auth_test.go` (splitting `oidc.go` deferred to backlog) |
| app.js: notice in hidden pane, error flash and stray retry while redirecting | LOW (rev) | fixed |
| Test left package logger silenced | LOW (rev) | restores previous writer |
| Spec wording (`next` param, `safeNext` rules) | LOW (rev) | spec delta updated |

Accepted / backlog (`openspec/backlog.md`, google-oidc-login section): in-memory revocation lost on
restart; open sockets not closed on logout/expiry; proxy IP behind Caddy (one shared login budget);
HSTS; SESSION_SECRET entropy/rotation; callback 400 page without link; `/gifs`/`/playlist` 404 vs
401 without key; discovery after bind; panic value in logs; flow cookie cannot be `__Host-`; name
look-alikes.

## Manual test (pending — user, real credentials)

1. `.env` with `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET`, `SESSION_SECRET` (`openssl rand -base64 48`);
   `http://localhost:8080/auth/callback` registered as redirect URI.
2. `go run ./cmd/chatroom` without env → exits with an error naming the variables (no values).
3. Login with a crombie.dev account → chat, nickname = Google name; two tabs → `Name (2)`.
4. Login with an account outside the domain → 403 access-denied page; "Probar con otra cuenta" shows the chooser.
5. "Cerrar sesión" → signed-out page; going back to `/` asks to log in again.
