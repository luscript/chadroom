# Archive Report: typing-indicator

**Archived**: 2026-10-23
**Verdict at verify**: PASS

## Summary
"X está escribiendo…" in rooms and DM tabs. Start-only `typing` signal (page ≤1/3 s per pane,
server ≤1/s per connection), receivers expire after 5 s or on chat/dm/leave. Best-effort
delivery (`offer` never drops a client), never stored, logged or echoed.

## Specs merged into `openspec/specs/chat/spec.md`
- MODIFIED: Purpose, CHAT-21 (typing is a known envelope; unknown-type example now `poke`).
- ADDED: CHAT-26 Typing signal delivery, CHAT-27 Chat page typing indicator.
- No removals.

## Files changed
`internal/chat/{message,client,room}.go`, `internal/chat/{client,room}_test.go`, `README.md`,
`cmd/chatroom/{index.html,app.js,app.css,main_test.go}`.

## Follow-ups (backlog `typing-indicator-polish`)
Manual UI checklist (both skins); optional ¾-full `offer` skip; shared room/DM throttle budget;
per-connection throttle → `rate-limiting`.
