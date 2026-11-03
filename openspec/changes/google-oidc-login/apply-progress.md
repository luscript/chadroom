# Apply Progress: google-oidc-login

## Batch A (parallel) — done

| Task | Result | Commit |
|------|--------|--------|
| 3.1 | `chat.NewServer(allowedOrigins, validAvatar, nickname NicknameFunc, rooms...)`; nil panics; `?nickname=` no longer read; `queryNickname` test helper; `TestNicknameResolver` | 9e6a748 |
| 4.2 | start view (`#start-view/#start-status/#start-error/#start-retry`), `#leave-btn` = "Cerrar sesión" (POST logout), 401 → `/auth/login`, reconnect probe after 3 failures, `denied.html`, `signed-out.html` | ee92d77 |
| 1.1, 2.1–2.4 | deps added (oauth2 v0.37.0, go-oidc v3.21.0, still `// indirect` until 2.5 imports them), `LoadConfig`, sealer, `Sessions`/`Require`, rate limiter, `authtest` fake provider | 57385c4 |

Files: `go.mod`, `go.sum`, `internal/auth/{config,cookie,session,ratelimit}.go` (+ tests),
`internal/auth/authtest/provider.go` (+ test), `internal/chat/{server.go,server_test.go,client_test.go}`,
`cmd/chatroom/{index.html,app.js,app.css,denied.html,signed-out.html}`.

Deviations:
- Rate limiter keys on the `RemoteAddr` host instead of `c.ClientIP()` (immune to forwarded-header spoofing; matches SRV-17).
- `Require(Page)` `next` uses `URL.Path` only (SRV-15) instead of `RequestURI`.
- Start view uses `<div class="join-form">` (no submit); `.join form` CSS selectors became `.join .join-form`.
- `authtest` adds `TokenFail`, `TokenCalls()`, `LastAuthorizeQuery()` for spec scenarios.

Known breakage until batch C: `cmd/chatroom` does not compile (old `NewServer` call at `main.go:135`);
`main_test.go:219` asserts the removed `id="join-error"`.

## Batch B — done

| Task | Result | Commit |
|------|--------|--------|
| 2.5 | `auth.New(ctx, cfg, denied)`, `Sessions()`, `Register(r.Group("/auth"))`; login/callback/logout; `safeNext`, `emailInDomain`, `displayName` (64 runes, no email fallback); oauth2/go-oidc direct requires | ce35a10 |
| 5.1–5.3 | README, backlog, CLAUDE.md | d89dc22 |

Deviations: `email_verified` must be boolean `true` (string `"true"` → 403); token endpoint uses
`AuthStyleInHeader` (no double token request); logout also rejects multiple Origin headers.

## Batch C — done

| Task | Result | Commit |
|------|--------|--------|
| 4.1 | wiring in `main.go`: fail-fast config, discovery in `run`, public/protected routes, auth pages + CSP, `identityNickname`, `recoverPanics`; tests adapted + 10 new | see git log |

Deviations: `recoverPanics` re-panics on `http.ErrAbortHandler`, logs no stack; `run` logs a fixed
line on auth setup failure.

All tasks complete. `go build`, `go vet`, `gofmt -l`, `go test -race -count=1 ./...` green (worker-reported).
