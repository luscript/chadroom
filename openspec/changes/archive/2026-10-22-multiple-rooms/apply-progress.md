# Apply Progress: multiple-rooms

**Status**: complete (batch 1 of 1, 2 parallel workers)
**Date**: 2026-10-22

## Batch 1

| Worker | Tasks | Status |
|--------|-------|--------|
| crombie-worker (Group B, backend) | 1.1, 1.2, 1.3, 2.1, 2.2, 4.1, 4.2, 4.3, 4.4 | SUCCESS — gofmt clean, `go vet` clean, `go test -race -count=1 ./...` pass (chat pkg also `-count=3`) |
| crombie-worker (Group F, frontend) | 3.1, 3.2, 3.3, 3.4, 3.5, 3.6 | PARTIAL → resolved — Go string tests could not build mid-backend-edit; `node --check app.js` OK; `TestIndexHasRoomTabs` later passed in Group B's run |

## Files changed

- Created: `internal/chat/server.go`, `internal/chat/server_test.go`
- Modified: `internal/chat/room.go`, `internal/chat/client.go`, `internal/chat/client_test.go`,
  `internal/chat/room_test.go`, `cmd/chatroom/main.go`, `cmd/chatroom/main_test.go`,
  `cmd/chatroom/index.html`, `cmd/chatroom/app.js`, `cmd/chatroom/app.css`, `README.md`

## Deviations from design

- Backend: extra test helpers `joinRoom` and `dialOriginRoom` in `server_test.go`;
  `TestLeaveOneRoomKeepsOther` reads O's juegos frames in a loop (skips O's own queued join,
  fails on any leave) instead of asserting the very next frame; `TestUnknownRoomIs404` also
  covers `general/x` and `"general "`; README updated beyond the minimum (intro, file list).
- Frontend: `.room-tabs` has `flex: 1`; `.join-error` uses `--color-accent-300` (no error
  token); a `joining` flag prevents double submit while rooms load; `loadRooms` de-duplicates
  ids; "Salir" does not reset `active` (re-join re-activates the first room; `room.ws` guards
  neutralize stale callbacks).

## Manual verification checklist (no JS harness)

1. `/rooms` blocked → `#join-error` shown, no socket; unblocked → retry works.
2. After login only `#general` joined/active; others muted ("sin unirte"); "Dejar sala" disabled.
3. Click `#juegos` → joins and activates; header, users, status follow; draft kept.
4. Inactive-room chats bump the badge (99+ cap), reset on activate; own/system/history don't count.
5. Per-room assigned nickname drives "vos" and `#me`.
6. "Dejar sala" leaves only the active room; other members see `leave`.
7. Killing one room's socket reconnects only that room, with "Reconectado" in its pane.
8. "Salir" closes every socket; re-join works.
9. ≤560px: tab strip scrolls in one row; "Dejar sala" stays visible.
10. No CSP violations; existing features (images, emoji/GIF, 4096 check, users popover) unchanged.

## Issues

None open.
