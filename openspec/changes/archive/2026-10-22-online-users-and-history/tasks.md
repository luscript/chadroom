# Tasks: online-users-and-history

Design: `design.md`. Spec: `specs/chat/spec.md` (CHAT-17, CHAT-18; CHAT-02/05/10/11/14/16 modified).
Batches: **A** (Go hub, `internal/chat/`) and **B** (page, `cmd/chatroom/` + README) touch
disjoint files and run in parallel.

## 1. Infrastructure — frame types (batch A)

- [x] 1.1 `message.go`: add `typeUsers`, `typeHistory`, `historySize = 50`, `usersFrame`, `historyFrame`; `encode(v any)`; reword the 4-field comment to apply to chat/join/leave/welcome.

## 2. Implementation — hub (batch A)

- [x] 2.1 `room.go`: `history []message` (cap 50) and `dropped []*Client` fields, initialized in `NewRoom`.
- [x] 2.2 `room.go`: `remember(m)` (copy-on-evict) called from `relay` after a successful encode; `relay` builds the `message` once.
- [x] 2.3 `room.go`: `userNames()` sorted by `foldKey`, never nil.
- [x] 2.4 `room.go`: register case → `admit(c)`: welcome → users → history (shared ts) via `deliver`, return on first failure; then `publish(join)`.
- [x] 2.5 `room.go`: `fanOut` records drops in `dropped`; `publish(msg)` fans out then announces leaves iteratively and clears `dropped`; `relay`/`leave` use `publish`; shutdown stays silent; update CHAT-02 comments in `leave`.

## 3. Testing — hub (batch A)

- [x] 3.1 `room_test.go` helpers: `nextRaw`, `decodeUsers`, `decodeHistory` (exact keys, `[]` not null, entries via `decode`), `expectWelcome`, `expectUsers`, `expectHistory`; `syncRoom` uses `expectWelcome`; `TestShutdownIsSilent` lenient type peek.
- [x] 3.2 `room_test.go`: resize slow buffers (3→5, 2→4), expect `leave` after slow drops, deterministic sync (`join obs` → `leave dropped`), `TestLeaveAfterDrainDroppedClient` expects exactly one leave; timestamp test uses `expectWelcome`.
- [x] 3.3 `room_test.go` new tests: `TestJoinSequence`, `TestUsersSortedCaseInsensitive`, `TestJoinFramesExactShape`, `TestHistoryOnlyChat`, `TestHistoryKeepsLast50`, `TestSlowDropCascadeAnnounced`, `TestNewcomerDroppedDuringJoinIsSilent`; extend `TestBroadcastFromNonMemberDropped`.
- [x] 3.4 `client_test.go`: `readRaw`; `dial` consumes users + history; slow buffer 2→4 with fixed comment; new `TestJoinReceivesUsersAndHistory`.
- [x] 3.5 `gofmt`, `go vet ./...`, `go test -race ./...` pass.

## 4. Implementation — page (batch B)

- [x] 4.1 `index.html`: `.online` wrapper with `#users-btn` (`#users-count` + "en línea", `aria-expanded`, `aria-controls`), `#users-panel.popover.users` with title and `#users-list` in `.actions` before `#me`. No inline code/style.
- [x] 4.2 `app.css`: `.online`, `.tool-users`, `.popover.users`, `.users-title`, `.users-list` (+ `li`, `.tag`); mobile tag max-width in the existing 560px block.
- [x] 4.3 `app.js`: `users` Set + `renderUsers()` (localeCompare 'es' base, textContent, "vos" tag on `myName`, count); `panels.users` + click toggle; `join()` clears users.
- [x] 4.4 `app.js`: `addRaw` handles `users`/`history` before the `from` guard (type-check fields); `join`/`leave` patch the set; `renderHistory` (aria-busy, replaceChildren, lastAuthor=null, addChat for valid chat entries, unbusy, then "Reconectado" if `outage`); remove "Reconectado"/`outage` reset from `welcome`.

## 5. Testing & docs — page (batch B)

- [x] 5.1 `main_test.go`: `TestIndexHasOnlineList` (ids + aria-controls); existing CSP/no-inline tests unchanged and passing.
- [x] 5.2 `README.md`: protocol section — `users`/`history` frames, join sequence, slow-drop leave, in-memory history of 50.
- [x] 5.3 `go vet ./...`, `go test -race ./cmd/...` pass.
