# Apply Progress: typing-indicator

## Batch 1 (2026-10-23) — 2 parallel crombie-workers

### Group B (backend) — SUCCESS
Tasks: 1.1, 1.2, 2.1, 2.2, 3.1, 3.2
Files: `internal/chat/message.go`, `client.go`, `room.go`, `client_test.go`, `room_test.go`, `README.md`
- `typeTyping`, `typingFrame`, `typingDMFrame`, `inboundTyping`; `parseInbound` typing case (dm → typing → chat).
- `typingGate` + `typingInterval` (1 s) in `readPump`, after `parseInbound`; liveness refresh untouched.
- `Room.typing` (room: all except sender; DM: recipient via `memberByKey`, self/offline silent) and
  best-effort `offer` (skip on full buffer, never removes/announces). No history/log/dm_error.
- Tests: `TestParseInbound` rows (unknown-type example → `{"type":"poke"}`), `TestTypingGate`,
  WS tests (delivery, throttle, invalid discarded), hub tests (exclude sender, DM recipient only,
  self/offline silent, non-member, not in history, full buffer skip, not logged).
- Checks: gofmt, vet, `go test -race ./internal/...` pass.

### Group F (page) — SUCCESS
Tasks: 4.1, 4.2, 4.3, 4.4
Files: `cmd/chatroom/index.html`, `app.css`, `app.js`, `main_test.go`
- `#typing` line (no aria-live/role), base + chadroom CSS, `composerRoom()`, `notifyTyping`
  (input + emoji), per-pane `typing` Maps with TTL timers, clears on chat/dm/leave/close/
  reconnect/teardown, `activate` re-render, `TestIndexHasTypingLine`.
- Checks: gofmt, `go test -race ./cmd/...`, `node --check` pass.

## Deviations from design
None.

## Batch 2 (2026-10-23) — verify follow-ups, 1 crombie-worker — SUCCESS
Files: `internal/chat/room.go` (comment only), `room_test.go`, `client_test.go`
- V1 `TestTypingNotLogged` asserts an empty log. V2 `Room.broadcast` comment mentions typing.
- V3 `TestInvalidTypingDiscarded` proves invalid frames do not consume the throttle budget.
- V4 `TestThrottledTypingExtendsReadDeadline` (existing `shortPings` helper; no new knob).
- Checks: gofmt, vet, `go test -race ./...`, `-count=5 -run Typing` pass.

## Pending
- 5.2 manual UI checklist (user) — moved to backlog `typing-indicator-polish`.
