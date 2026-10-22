# Backlog

Pending improvements identified during `fix-channel-handling` (archived 2026-10-22).
Sources: `changes/archive/2026-10-22-fix-channel-handling/exploration.md` (initial review) and
`verify-report.md` (rounds 1–2). Each group is a suggested future SDD change
(`/sdd-new <name>`).

## `structured-messages` (security + features foundation)
- [ ] #10 JSON envelope `{type, from, text, ts}` so join/leave notices can't be spoofed by a
      user typing "X joined! 🎉" or a nickname like "Admin".
- [ ] Nickname charset rules: strip/reject control characters and bidi overrides (U+202E);
      decide whether to reject duplicate nicknames.
- [ ] Drop empty / whitespace-only messages.

## `rate-limiting` (DoS)
- [ ] #11 Per-client token bucket in readPump.
- [ ] Global max-clients cap (503 when exceeded); optionally per-IP cap.
- [ ] Don't log `ErrReadLimit` per connection (attacker-controlled log volume).

## `frontend-hardening`
- [ ] #13 Build WS URL from `location.host` and use `wss:` on HTTPS (today the page only works
      at exactly `http://localhost:8080`; `127.0.0.1` is rejected by the origin check).
- [ ] #15 Handle cancelled `prompt` (nickname "null").
- [ ] #16 Check `readyState` before `send`; reconnect with backoff.
- [ ] CSP header on `GET /` — requires moving the inline script / `onclick` to a hashed or
      external script. Note: the page now also loads Google Fonts (needs `style-src`/`font-src`).
- [ ] Inline images leak viewers' IPs: any user can send an https image URL and every other
      browser fetches it (`IMAGE_URL` in `index.html`). Options: click-to-load, a host allowlist,
      or a server-side proxy.

## `deps-cleanup`
- [ ] #17 `go mod tidy` (gin and gorilla are marked `// indirect`).
- [ ] #18 Consider replacing gin with `net/http` `ServeMux` (only two routes).
- [ ] Run `govulncheck ./...`.

## `ops-hardening`
- [ ] gin release mode (`GIN_MODE=release` in deploy, or `gin.SetMode`).
- [ ] DNS rebinding: optional `ALLOWED_HOSTS` check on `r.Host`, or bind to `127.0.0.1` for
      local use. Revisit missing-Origin allowance if cookie auth is ever added.
- [ ] TLS.

## `chatroom-test-polish` (small follow-ups from verify round 2)
- [ ] N1 Fix misleading comment in `TestDroppedSlowClientConnectionIsClosed` (drop is
      triggered by B's join notice, not the broadcast) or size buffer to 2.
- [ ] N2 Test `run()` Serve-error branch (closed listener → exit code 1).
- [ ] N3 Rename `serve` param `addr` (shadows package const).
- [ ] N4 Extract `newServer` and assert SRV-05 timeouts / `MaxHeaderBytes`.
- [ ] N5 Don't log close 1006 (abrupt tab close) on every disconnect.
- [ ] N6 Log 1003/1007 rejections with nickname.
- [ ] N7 Assert CHAT-07 write deadline.

## `chat-features` (after `structured-messages`)
- [ ] Online user list.
- [ ] Multiple rooms.
- [ ] Last-N message history on join.
- [ ] Typing indicator.

## Housekeeping
- [ ] README line ~19 still says the page connects to `ws://localhost:8080` and to avoid
      `127.0.0.1`; `index.html` already builds the URL from `location.host` (#13 partially done).
- [ ] #20 Review `.claude.json` / `.claude/` for secrets before committing.
