# Apply Progress: user-profiles

## Batch 1 (parallel, 2 workers) — 2026-10-30

| Worker | Tasks | Status |
|--------|-------|--------|
| crombie-worker (group A, backend + README) | 1.1–1.4, 2.1–2.3, 3.1 | SUCCESS |
| crombie-worker (group B, frontend) | 4.1–4.5 | SUCCESS |

### Files changed
- `internal/chat/nickname.go`, `message.go`, `room.go`, `server.go`, `client.go`
- `internal/chat/room_test.go`, `client_test.go`, `server_test.go`
- `README.md`
- `cmd/chatroom/app.js`, `index.html`, `app.css`, `main_test.go`

### Worker verification
- Group A: `gofmt -l .` clean, `go vet ./...` clean, `go test -race -count=1 ./...` green (3 runs).
- Group B: `go test -race ./cmd/...` green, `gofmt -l cmd` clean, `node --check app.js` ok; no
  `innerHTML` / `style=` / `setAttribute('style')`.

### Deviations from design
- Backend: `decodeUsers` test helper now tolerates `avatars` (needed for late-joiner frames).
  `NewServer` signature unchanged; `main.go` untouched. README never mentioned the join-form
  avatar field, so nothing to remove there.
- Frontend: `activate()` closes the user popup first; `openProfileEditor` clears
  `#profile-error`; `userTrigger(room, nick, returnFocus)` falls back to the clicked element;
  extra CSS rule `.avatar-slot .user-trigger { display: block; }`.

### Issues
- Tests log noisy `Read error`/`Write error` lines on abrupt closes (pre-existing pattern).
- Manual two-browser / mobile checklist (5.2) pending — user.

## Batch 2 — verify fixes (parallel, 2 workers) — 2026-10-30

- Backend (F1–F4): `cmd/chatroom/main.go`, `main_test.go`, `internal/chat/room.go`, `server_test.go`.
- Frontend (F5–F9): `cmd/chatroom/app.js`.
- Final run green (see `verify-report.md` round 2). Task 5.1 done; 5.2 pending (user).
