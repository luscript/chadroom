# Apply Progress: account-sessions

## Batch 1 (parallel: 2 workers)

Completed: 1.1, 1.2, 1.3, 2.1–2.8, 3.2, 4.1, 4.2, 4.3.

Files changed:
- `internal/chat/server.go` — `IdentityFunc`, 401 on empty account before upgrade.
- `internal/chat/client.go` — `account`, `member`, `reqAvatar`, `reqBio`.
- `internal/chat/room.go` — `member` registry, `attach`, first/later admit, last-connection leave,
  DM/typing/profile via member; private order `welcome, users, [music], history`.
- `internal/chat/nickname.go` — comment only.
- `internal/chat/{room,server,client}_test.go` — adapted + new tests (178 top-level tests pass,
  `-race -count=3`, vet, gofmt clean).
- `cmd/chatroom/app.js` — `closeSnapshotSlot()` on `history`; `welcome` resends or adopts own profile
  (old duplicate `resendProfile` line removed).

Deviations:
- Failed-first-admit: new direct-drive `TestFailedFirstAdmitLeavesNoMember` instead of extending the
  running-room test (reading `cr.members` during `Run` would race).
- `TestTwoTabsOverWebSocket` uses a 100 ms sleep after the first close (possible flakiness; review).
- Extra test `TestMusicLaterConnectionGetsSnapshot`.


## Batch 2 (1 worker)

Completed: 3.1, 4.4, 4.5.

Files changed:
- `cmd/chatroom/main.go` — `chatIdentity` (account = session email), pure `identityNickname`,
  `newChatServer(…, chat.IdentityFunc)`.
- `cmd/chatroom/{main,gifs,playlist,auth}_test.go` — `queryIdentity`; `TestChatIdentity`;
  `TestSameAccountInTwoTabs` (marker join, no sleeps); `TestDifferentAccountsSameName`.

Verification: `gofmt -l .` empty, `go vet ./...` clean, `go test -race -count=1 ./...` green.

All tasks complete.
