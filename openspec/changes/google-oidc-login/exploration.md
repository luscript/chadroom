# Exploration: google-oidc-login

Synthesis of two crombie-explore passes (Go backend; frontend + specs + docs) on `main` @ 9fbdb7d
(`user-profiles` merged).

## Key findings

- **Router** — `newRouter(chatSrv, gifs, playlists)` / `newRouterLogging` (`cmd/chatroom/main.go:162`)
  on `gin.New()` with a custom access log that already strips the query string (`main.go:155`, tested
  at `main_test.go:898`) — so `?code=`/`?state=` on the callback never reach the access log. No route
  groups or per-route middleware yet. `gifs`/`playlists` are nil-able (route not registered when nil);
  tests rely on that (`gifs_test.go:48`, `playlist_test.go:70`, `main_test.go:28`).
- **Assets** are `[]byte` vars from `//go:embed`, served with `c.Data` via `servePage`/`serveAsset`.
  CSP is built by `buildCSP` and set only in `servePage` (`default-src 'none'`, `connect-src 'self'`,
  `form-action 'none'`). A redirect to Google is a navigation, not a form submit → CSP unaffected.
- **Config** is a `config` struct read with `os.Getenv` + `TrimSpace`; empty = feature disabled.
  Nothing fails fast today. Listen address is the const `:8080`. Proxy headers are not trusted (SRV-05).
- **WebSocket** — `chat.Server.HandleWebSocket` (`internal/chat/server.go:73`): room from `?room`
  (404 before upgrade) → Upgrade with `checkOrigin` (403; empty Origin allowed, `null` rejected) →
  reads `?avatar`, `?bio`, `?nickname` → `room.serveConn(conn, normalizeNickname(nick), avatar, bio)`.
  `NewServer(allowedOrigins, validAvatar, rooms...)`. Seam for identity: request context set by
  middleware before the upgrade.
- **Nicknames** (`nickname.go:61`): `cleanText` (control/bidi stripped, trim, 32 runes, empty →
  `Anonymous`); uniqueness case-insensitive (`foldKey`) per room, enforced in `Room.admit` by
  suffixing `"<base> (n)"` — never rejects. DMs, `users`/`avatars`/`bios` maps, `from`, music all key
  on the nickname string → identity is per connection, not per account.
- **Deps** — `go 1.27.3`; `golang.org/x/oauth2` and `github.com/coreos/go-oidc/v3` absent. All
  requires currently marked `// indirect` (a `go mod tidy` will fix markers).
- **Frontend** — `#join-view` nickname form (`index.html:14-34`, `app.js:377`) → `enterChat(nick)`;
  `connect(room)` builds `/ws?room=&nickname=&avatar=&bio=` (`app.js:419`); final nick comes from
  `welcome.from` (`app.js:635`) so a server-chosen name already works. "Salir" (`#leave-btn`,
  `app.js:474`) is client-only: tears down sockets, back to join form. Reconnect never gives up
  (CHAT-16); a 401 on upgrade is invisible to the browser (close 1006). `/gifs`, `/playlist` treat 404
  as "disabled" and anything else as generic error; nothing handles 401. `/rooms` is fetched before
  joining.
- **Specs** — IDs `CHAT-01..40` (35/36 from `message-replies`, 37–40 from `user-profiles`, both
  unarchived) and `SRV-01..09`. New IDs start at **CHAT-41** and **SRV-10**. Delta format:
  `## MODIFIED Requirements` (restate) / `## ADDED Requirements`; no `REMOVED` precedent.
  Affected: SRV-01, SRV-02, SRV-06, SRV-07, SRV-08, SRV-09; CHAT-09, CHAT-12, CHAT-16, CHAT-20.
- **README** — config table at `README.md:77` (`Variable | Default | Descripción`), API table from
  line 90 (`/ws` row at 100), nickname prose at 35/41/294, architecture tree at 329.
- **Backlog** — `ops-hardening` (line 100) "revisit missing-Origin allowance if cookie auth is added";
  `frontend-polish` (line 34) reconnect never gives up; impersonation via look-alike nicknames (86-87);
  `rate-limiting` section (line 15).

## Risks

- Changing `newRouter`'s signature touches ~4 test call sites unless auth is nil-able there.
- Empty `Origin` is accepted on upgrade: with cookie auth, CSWSH protection relies on SameSite=Lax +
  the origin check for browsers (browsers always send Origin), which is sufficient; non-browser
  clients must present the cookie anyway.
- Stateless HMAC cookies cannot be revoked server-side without some state → "logout invalidates the
  session" needs an in-memory revocation set (or a server-side session store).
- Expired sessions would turn into an infinite silent reconnect loop unless the page probes a
  protected endpoint after failures.
- Google profile pictures (`lh3.googleusercontent.com`) are not in `imageHosts`/`IMAGE_HOSTS`;
  using them is out of scope.
- oauth2 exchange errors can embed the token-endpoint response body: never log them verbatim.

## Gaps

- `client.go`, `message.go`, `music.go` were not read in full (nickname usage inferred from `room.go`).
