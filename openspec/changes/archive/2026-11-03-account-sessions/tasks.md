# Tasks: account-sessions

Spec: `specs/chat/spec.md` (CHAT-02, 04, 11, 12, 17, 22, 23, 26, 30, 38, 41, ADDED 43).
Design: `design.md`.

## 1. Infrastructure — chat entry point (internal/chat)

- [x] 1.1 `server.go`: `NicknameFunc` → `IdentityFunc func(*http.Request) (account, nickname string)`;
      `Server.identity`; nil panic message; resolve identity before upgrade, `401` (no body, no log)
      on empty account; pass `account` to `serveConn`.
- [x] 1.2 `client.go`: add `account`, `member`; rename `avatar`/`bio` → `reqAvatar`/`reqBio`.
- [x] 1.3 Test helpers: `queryIdentity` (account from `?account=` or unique `conn-<n>`),
      `dialAccount`; `newTestClient` gets a unique account; `newAccountClient`.

## 2. Implementation — member registry (internal/chat)

- [x] 2.1 `room.go`: `member` type; `members`, `taken map[string]*member`, `dropped []*member`; `attach`.
- [x] 2.2 `admit`: first vs later connection; private order `welcome, users, [music], history`;
      later connection inherits nickname/profile, no `join`; failed delivery handled by `remove`.
- [x] 2.3 `remove`, `leave`, `fanOut`, `deliverPrivate`, `announceDropped`: leave only when the
      member's last connection goes; name freed then.
- [x] 2.4 `userNames`, `memberValues`, `memberByKey` (O(1) via `taken`), `relay`, `music` via `c.member`.
- [x] 2.5 `direct`: self by member identity; fan-out to all recipient + sender connections.
- [x] 2.6 `typing`: room-wide skips the sender's member; DM to all recipient connections, silent to self.
- [x] 2.7 `profile`: updates the member; publish to the room.
- [x] 2.8 `nickname.go`: `assignNickname` over `taken map[string]*member`.

## 3. Implementation — wiring and frontend

- [x] 3.1 `cmd/chatroom/main.go`: `chatIdentity(r) (account, nickname)`; pure
      `identityNickname(auth.Identity)`; `newChatServer(…, chat.IdentityFunc)`; `run` wires it.
- [x] 3.2 `cmd/chatroom/app.js`: `closeSnapshotSlot()` on `history` (removed from own `join`);
      `welcome` adopts own profile unless a resend is pending.

## 4. Testing

- [x] 4.1 Adapt existing `internal/chat` tests (rename, `cr.attach` seating, `req*`,
      `TestMusicNewcomerSnapshot` order, failed-first-admit leaves no member).
- [x] 4.2 New hub tests from the design test plan (same-account sharing, never suffixed, name kept,
      leave on last, silent slow drop, both dropped → one leave, later failed admit, profile inherit,
      DM fan-out, DM self from other tab, DM slow recipient conn, typing exclusions, profile sync,
      player keeps playing).
- [x] 4.3 Chat integration: `TestIdentityResolver`, `TestEmptyAccountRejected`, `TestTwoTabsOverWebSocket`.
- [x] 4.4 cmd tests: `queryIdentity` rename in `main_test.go`, `gifs_test.go`, `playlist_test.go`,
      `auth_test.go`; `chatIdentity` test; CHAT-41 same-account and same-name-different-account
      tests with real session cookies.
- [x] 4.5 `gofmt` + `go vet ./...` + `go test -race ./...` green.
