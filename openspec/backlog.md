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
- [x] ~~Self-host Inter~~ — obsolete: the pinkroom redesign dropped Google Fonts (CSP
      `style-src 'self'`, no `font-src`). `app.css` still names `"Inter"` with a `system-ui` fallback.
- [ ] Reconnect never gives up on permanent failures (403 origin is reported as 1006);
      consider an attempt cap or a "retry" button after N minutes.
- [ ] Trusted Types: `require-trusted-types-for 'script'` in the CSP (no sinks today).
- [ ] First join that fails shows "Conexión perdida… / Reconectado" without ever having been
      connected; use a `wasOnline` flag for the wording.

## `gif-picker` — done, archived 2026-10-23 (manual UI check user-verified)
- [x] GIF panel with search + trending, like the emoji panel.
- [x] Server endpoint `GET /gifs?q=` calling Giphy with `GIPHY_API_KEY` from the environment
      (key never sent to the browser); search UI hidden when the key is unset.
- [x] HTTP client with timeout, response trimmed to the fields the page needs; "Powered by
      GIPHY" attribution. Images come only from the allowlisted Giphy hosts.

## `gif-picker-polish` (deferred from `gif-picker` verify — low priority)
- [ ] Quota: unique `/gifs` queries are unauthenticated; add singleflight + a small cap on
      in-flight upstream calls (503 when busy). Belongs with `rate-limiting`.
- [ ] gin access log records `/gifs?q=` search terms; strip the query in the logger.
- [ ] Drain the upstream body on non-200 before closing (connection reuse).
- [ ] No feedback when a GIF click fails to send (`sendText` false); same as the paste box.

## `music-player` — done as `room-music-sync`, archived 2026-10-23 (manual UI check pending)
- [x] Shared YouTube / YouTube Music player in `musica`: one track, global play/pause/seek,
      server-authoritative state, `postMessage` control (no YouTube script), consent via "Escuchar".
- Local-file variant (`MUSIC_DIR`) was built and then dropped by the user; it lives unmerged on
      branch `feat/music-player`.

## `room-music-sync-polish` (deferred from `room-music-sync` verify — low priority)
- [ ] Manual two-browser checklist from `changes/archive/2026-10-23-room-music-sync/design.md`
      (steps 1 and 7 first: referrer / error 153 on localhost, error codes).
- [ ] Try `sandbox="allow-scripts allow-same-origin"` on the iframe; keep it only if the embed,
      autoplay and handshake still work.
- [ ] `Permissions-Policy` header (camera/mic/geolocation off; autoplay/encrypted-media for self +
      youtube-nocookie). Pairs with `ops-hardening`.
- [ ] Fan-out: N connections × 4 controls/s; room-wide coalescing or per-IP cap (`rate-limiting`).
- [ ] Seek bar disabled until "Escuchar" (no duration known); frames during buffering may cause a
      drift seek (jitter); Safari/iOS may block `playVideo` into the iframe.
- [ ] Undocumented `postMessage` protocol: if YouTube changes it, sync breaks (chat unaffected).
- [ ] Queue / playlists, titles/thumbnails, DJ role (out of scope today).

## `music-queue` — done, archived 2026-10-23 (manual UI check pending)
- [x] Shared queue in `musica`: anyone adds (paste-while-playing appends) / removes / skips, cap 30,
      auto-advance on end (client reports `ended`, server dedupes by `seq`), separate `ended` gate.
- [x] Auto-start on opening the pane ("Escuchar" is the blocked-audio fallback); re-sync on return;
      end detection independent of a known duration; finished track latched so it never auto-replays.
- [ ] Manual two-browser checklist from `changes/archive/2026-10-23-music-queue/design.md`
      (steps 3/4/6/8: auto-advance, ↺ replay, re-sync on return, repeated-id reload).
- [ ] `ended` plateau heuristic can false-trigger on a long network stall when the duration is
      unknown (could skip a track). Tighten only if seen in practice.
- [ ] Track titles/thumbnails via oEmbed (no API key) — candidate follow-up, needs a server fetch + cache.

## `profile-picture` — done, archived 2026-10-23 (manual UI check pending)
- [x] Profile picture next to messages and in the online list (allowlisted image URL at join,
      per connection; `avatar` / `avatars` fields).

## `profile-picture-polish` (deferred from `profile-picture` verify — low priority)
- [ ] Manual UI checklist from `changes/archive/2026-10-23-profile-picture/design.md`.
- [ ] Avatar URL repeated in every chat/history frame and per member in `users` (≤512 B each):
      output amplification with no client cap. Belongs with `rate-limiting` (max-clients cap);
      optionally drop it from chat frames and resolve from `users`.
- [ ] Copying someone's picture + look-alike nickname = cosmetic impersonation (same class as
      `nickname-polish` M1).
- [ ] gin access log records the `/ws` query (nickname + avatar); strip the query in the logger
      (same fix as `gif-picker-polish`).
- [ ] Change the picture mid-session (today: leave and join again); avatars on DM tabs.

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

## `typing-indicator-polish` (deferred from `typing-indicator` verify — low priority)
- [x] Manual UI checklist from `changes/archive/2026-10-23-typing-indicator/design.md` (user-verified 2026-10-23).
- [ ] Typing frames use send-buffer slots: an already-stuck client is evicted slightly sooner;
      optional `offer` skip when the buffer is ¾ full.
- [ ] Room and DM typing share one 1/s server budget: a pane switch within 1 s delays the first
      DM indicator by up to 3 s (accepted, cosmetic).
- [ ] Throttle is per connection, not per user/IP; invalid frames and malformed `dm` frames
      (→ `dm_error`) are not rate-limited. Belongs with `rate-limiting` (connection cap).

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
- [x] Multiple rooms — done in `multiple-rooms` (2026-10-22).
- [x] Private chat (1-to-1 DMs per room) — done in `private-chat` (2026-10-23).
- [x] Last-N message history on join — done in `online-users-and-history` (2026-10-22).
- [x] Typing indicator — done in `typing-indicator` (2026-10-23).

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

## `private-chat-polish` (deferred from `private-chat` verify — low priority)
- [ ] S1 Fail-open fallback: a malformed / case-variant DM envelope (`{"Type":"dm"…}`, `"type":"DM"`,
      truncated JSON) from a non-page client is relayed as room chat and kept in history.
      Option: frames starting with `{` that are not a valid envelope → `dm_error invalid`
      (needs a CHAT-21 change; breaks plain-text messages that start with `{`).
- [ ] S2 `memberByKey` folds every nickname per DM inside `Run`; store the folded key on
      `Client` or make `taken` a `map[string]*Client`. Pairs with `rate-limiting`.
- [ ] S3 Recipient dropped as slow still gets the sender an echo (looks delivered); consider
      `dm_error offline` instead.
- [ ] Per-tab close for DM tabs (today they go away only on "Dejar sala" / "Salir").
- [x] Manual UI checklist from `changes/archive/2026-10-23-private-chat/design.md` (user-verified 2026-10-23).
- [ ] Cross-room DMs / DM replay after reconnect (need a server-wide identity or session token).

## Housekeeping
- [x] README line ~19 still says the page connects to `ws://localhost:8080` and to avoid
      `127.0.0.1`; `index.html` already builds the URL from `location.host` (#13 partially done).
- [ ] #20 Review `.claude.json` / `.claude/` for secrets before committing.
