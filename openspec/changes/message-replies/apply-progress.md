# Apply progress: message-replies

## Batch 1 (parallel, 2 workers) — SUCCESS

| Group | Tasks | Files |
|-------|-------|-------|
| A backend | 1.1, 1.2, 2.1, 2.2, 2.3, 4.1 | `internal/chat/{message.go,room.go,room_test.go,client_test.go,server_test.go}`, `README.md` |
| B frontend | 3.1, 3.2, 3.3, 3.4 | `cmd/chatroom/{index.html,app.js,app.css,main_test.go}` |

Worker checks: `gofmt -l .` clean, `go vet ./...` clean, `go test -race ./...` green (backend also
`-count=3` on `internal/...`), `node --check cmd/chatroom/app.js` OK.

### Deviations from design

- `TestChatAndHistoryKeepAvatar` now expects `id` on chat frames (exact-key assertion).
- History check split into its own test `TestReplyKeptInHistory`; test-only helpers
  `chatWithReply` / `dmWithReply`.
- Frontend: `msg-flash` timer stored on the `msgs` entry (`flashTimer`) so a pending flash is
  cleared before a new one; `removeConversations` also cancels a reply whose conversation is removed.

### Pending

- 5.1 verify phase; 5.2 manual two-browser checklist (user).

## Batch 2 (verify follow-ups, 1 worker) — SUCCESS

F1 `TestReplyRingBoundary` (exact 200/201 boundary); F2 closed DM conversation drops its ↩
buttons; F3 DM quotes cut to 100 runes; F4/F5 comment and `dmFrame` formatting; F6 comment on
`isImageQuote`. Files: `internal/chat/{room_test.go,client_test.go,message.go}`,
`cmd/chatroom/app.js`. Gates green.
