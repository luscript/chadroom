# Apply Progress: private-chat

**Status**: complete (batch 1 + verify fix batch V1–V6)
**Updated**: 2026-10-23

## Batch 1 (parallel: Group B backend + Group F frontend)

| Group | Tasks | Result |
|-------|-------|--------|
| B (backend) | 1.1, 1.2, 1.3, 2.1, 2.2, 4.1–4.5 | SUCCESS: `gofmt -l` empty, `go vet ./...` clean, `go test -race ./...` green (also `-count=5` on `internal/chat`) |
| F (frontend) | 3.1–3.4 | SUCCESS: `node --check app.js`, `go test ./cmd/...` green; not exercised in a browser |

## Files changed

- `internal/chat/nickname.go`: `cleanNickname` extracted; `normalizeNickname` delegates
- `internal/chat/message.go`: DM constants, `dmFrame`, `dmErrorFrame`, `inboundKind`, `parseInbound`, `jsonString`
- `internal/chat/client.go`: `readPump` uses `parseInbound`
- `internal/chat/room.go`: `dispatch`, `direct`, `dmError`, `memberByKey`, `deliverPrivate`; `publish` = `fanOut` + `announceDropped`
- `internal/chat/room_test.go`, `client_test.go`, `server_test.go`: `TestParseInbound` (31 cases), 9 hub DM tests, 5 WebSocket DM tests, `TestDMRoomScoped`
- `README.md`: inbound envelopes, `dm`/`dm_error`, privacy note
- `cmd/chatroom/app.js`: conversations, DM tabs/panes, `dm`/`dm_error`/`leave` handling, envelope `sendText`
- `cmd/chatroom/app.css`: `.dm-tab`, `.closed`, `.users-dm`

## Deviations from design

- Backend tests: extra helpers `joinAll`, `decodeDM`, `decodeDMError`, `nextNonJoin`; `readDM`/`readDMError` skip `join` frames; `TestDMNotLogged` calls `dispatch` directly.
- Frontend: divider text "Nueva conversación con X: puede ser otra persona."; `dm_error` without a matching conversation goes to the owning room's pane (never another room's); a `dm` whose peer would be our own name is ignored; helper `renderPaneTab`.

## Issues

None. Manual UI checklist (design.md) still pending.
