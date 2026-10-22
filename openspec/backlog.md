# Backlog

Pending improvements identified during `fix-channel-handling` (archived 2026-10-22).
Sources: `changes/archive/2026-10-22-fix-channel-handling/exploration.md` (initial review) and
`verify-report.md` (rounds 1–2). Each group is a suggested future SDD change
(`/sdd-new <name>`).

## `structured-messages` (security + features foundation) — done, archived 2026-10-22
- [x] #10 JSON envelope `{type, from, text, ts}` so join/leave notices can't be spoofed by a
      user typing "X joined! 🎉" or a nickname like "Admin".
- [x] Nickname charset rules: strip/reject control characters and bidi overrides (U+202E);
      decide whether to reject duplicate nicknames.
- [x] Drop empty / whitespace-only messages.

## `rate-limiting` (DoS)
- [ ] #11 Per-client token bucket in readPump.
- [ ] Global max-clients cap (503 when exceeded); optionally per-IP cap.
- [ ] Don't log `ErrReadLimit` per connection (attacker-controlled log volume).

## `frontend-hardening` — done, archived 2026-10-22
- [x] #13 Build WS URL from `location.host` and use `wss:` on HTTPS (today the page only works
      at exactly `http://localhost:8080`; `127.0.0.1` is rejected by the origin check).
- [x] #15 Handle cancelled `prompt` (nickname "null") — `prompt()` replaced by the join form.
- [x] #16 Check `readyState` before `send`; reconnect with backoff.
- [x] CSP header on `GET /` — requires moving the inline script / `onclick` to a hashed or
      external script. Note: the page now also loads Google Fonts (needs `style-src`/`font-src`).
- [x] Inline images leak viewers' IPs: any user can send an https image URL and every other
      browser fetches it (`IMAGE_URL` in `index.html`). Options: click-to-load, a host allowlist,
      or a server-side proxy.

## `frontend-polish` (deferred from `frontend-hardening` verify — low priority)
- [ ] Self-host Inter (woff2 embedded, `font-src 'self'`, drop Google preconnect): Google
      Fonts sees every user's IP and the page loses its font offline / on an isolated LAN.
- [ ] Reconnect never gives up on permanent failures (403 origin is reported as 1006);
      consider an attempt cap or a "retry" button after N minutes.
- [ ] Trusted Types: `require-trusted-types-for 'script'` in the CSP (no sinks today).
- [ ] First join that fails shows "Conexión perdida… / Reconectado" without ever having been
      connected; use a `wasOnline` flag for the wording.

## `gif-picker` (after `frontend-hardening`)
- [ ] GIF panel with search + trending, like the emoji panel.
- [ ] Server endpoint (e.g. `GET /gifs?q=`) calling Giphy with `GIPHY_API_KEY` from the
      environment (key never sent to the browser); panel hidden when the key is unset.
- [ ] HTTP client with timeout, response trimmed to the fields the page needs; "Powered by
      GIPHY" attribution. Images come only from the allowlisted Giphy hosts.

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

## `nickname-polish` (deferred from `structured-messages` verify — low priority, friends-only chat)
- [ ] M1 Invisible/format chars survive nickname cleanup (U+200B, U+FEFF, U+00AD, interior
      NBSP/U+2028, fillers U+3164/U+2800) → look-alike `Bob​` or blank names. Same class as
      homoglyphs; also blank-looking messages pass the blank filter (L2).
- [ ] M2 `assignNickname` scans suffixes linearly inside `Run` (O(K²) with K same-name
      clients); per-base next-suffix hint or attempt cap. Pairs with the max-clients cap.
- [ ] L1 Join/leave notices concatenate the nickname inline; render it in its own element.
- [ ] L4 Log close-frame reason with `%q` (peer-controlled text in logs).
- [ ] L6 JSON escaping amplifies `<>&`/controls up to ~6× (consider `SetEscapeHTML(false)`).
- [ ] Test polish: `nextChat` forbidden-join param is a no-op; forged-notice test without a
      real `Bob`; `<-slow.registered` wait; saturation listener can be slow-dropped; keyed
      `message{}` literals; rename `addRaw` → `handleFrame`.

## `chat-features` (after `structured-messages`)
- [x] Online user list — done in `online-users-and-history` (2026-10-22).
- [ ] Multiple rooms — next change.
- [x] Last-N message history on join — done in `online-users-and-history` (2026-10-22).
- [ ] Typing indicator.

## `presence-history-polish` (deferred from `online-users-and-history` verify — low priority)
- [ ] Own "X se unió" notice lands after "Reconectado" on reconnect; consider skipping the
      notice for `from === myName` (also changes first join — decide UX).
- [ ] Hide the "N en línea" button until the first `users` frame (shows "0" while connecting,
      stale count during an outage).
- [ ] Reset reconnect `attempt` only after the socket stayed up ~10 s (flapping server →
      fast loop, each replaying history). Pairs with `rate-limiting`.
- [ ] Cache the encoded history frame (invalidate in `remember`); cap it by bytes (~256 KB)
      as well as count. Pairs with `rate-limiting`.
- [ ] `renderUsers`: build a DocumentFragment instead of spreading into `replaceChildren`.
- [ ] Test polish: `TestHistoryKeepsLast50` with `2*historySize+3` messages + cap assertion;
      `drainTypes` helper instead of `goto` in `TestShutdownIsSilent`; `expectEmpty` at the
      end of `TestChatBeforeLeaveOnDisconnect`.
- [ ] Manual screen-reader check of the bulk history insert (`aria-busy`).

## Housekeeping
- [x] README line ~19 still says the page connects to `ws://localhost:8080` and to avoid
      `127.0.0.1`; `index.html` already builds the URL from `location.host` (#13 partially done).
- [ ] #20 Review `.claude.json` / `.claude/` for secrets before committing.
