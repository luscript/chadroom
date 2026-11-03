# Proposal: account-sessions

## Intent

Today every WebSocket connection is a separate user: opening a second window (or a phone) with the
same Google account shows `Ana (2)`, a second `join`, and DMs reach only one window. Make the
**Google account** the identity in a room: all connections of one account are the same member,
kept in sync, without forcing a single window.

## Scope

**In**
- Chat receives a stable, private **account key** (the session email, already lowercased and
  verified) alongside the display name.
- Per room, the hub keys members by account; each account has a set of connections (hub pattern,
  only `Room.Run` touches it, no locks).
- `join` is announced only on the account's first connection in the room; `leave` only when its
  last connection goes (any removal path: close, slow drop). Silent shutdown unchanged.
- One entry per account in `users`. Nickname suffixes (`Ana (2)`) only for **different** accounts
  with the same display name; the name is freed when the account's last connection goes.
- Later connections of an online account inherit its nickname and profile; their `?avatar=` /
  `?bio=` are ignored (the first connection seeds the profile).
- DMs are delivered to every connection of the recipient and echoed to every connection of the
  sender (one frame each, no duplicates). DM to one's own account stays `dm_error self`.
- Profile updates from any connection update the account and reach all its connections (and the room).
- Typing signals are not sent to the sender's own other connections.
- Frontend: a second window of the shared player room (`musica`) must leave its "waiting for snapshot"
  state without an own `join`.
- Tests: two connections of one account, different accounts with the same name, DM fan-out,
  leave on last close / last slow drop, profile sync, typing exclusion.

**Out**
- Closing the account's sockets on logout / session expiry (separate backlog follow-up).
- Cross-room DMs, DM replay after reconnect, persisting identity across restarts.
- Exposing any account identifier to browsers (none is added to frames).
- Per-account rate limits (gates stay per connection; noted as a risk).
- Using `sub` instead of email (would need a cookie version bump).

## Approach

Replace `chat.NicknameFunc` with a resolver that returns both the account key and the display name
(e.g. `IdentityFunc func(*http.Request) (account, nickname string)`); production builds it from
`auth.IdentityFrom`. Inside `Room`, add an account record (`nickname`, `avatar`, `bio`,
`conns map[*Client]struct{}`) indexed by account key and by folded nickname; `clients` stays the flat
per-connection set so `fanOut`, `send` ownership and the player halt rule do not change. Join, leave,
DM, typing and profile paths read through the account. Exact structures are defined in the design.

## Affected Areas

| Area | Files | Change |
|------|-------|--------|
| Chat entry / resolver | `internal/chat/server.go`, `cmd/chatroom/main.go` | Resolver returns account + name |
| Room hub registry | `internal/chat/room.go`, `client.go`, `nickname.go` | Account → connections; join/leave/DM/typing/profile |
| Frontend | `cmd/chatroom/app.js` | Music snapshot wait without own `join` |
| Tests | `internal/chat/*_test.go`, `cmd/chatroom/*_test.go` | New resolver, multi-connection tests |
| Specs | `openspec/specs/chat/spec.md` | CHAT-02, 04, 12, 17, 22, 23, 26, 38, 41 |

## Key decisions

- **Account key = session email**: stable within the allowed domain, already verified and
  lowercased; never sent on the wire nor logged.
- **First connection wins the profile**: later connections inherit; avoids a fresh window wiping it.
- **Nickname is fixed while the account is online** in that room (a renamed Google profile applies
  after all connections close).
- **Per-room accounts**: each room keeps its own account registry (rooms already independent).

## Rejected alternatives

- Forcing a single window (kick the old connection): worse UX on phone + laptop.
- Keying by `Sid`: changes on every login; two devices would still be two users.
- Keying by display name: two different people named "Ana" would merge.

## Risks

- Hub rewrite touches every removal path; a missed path causes false `leave` or a leaked name.
  Mitigation: tests for close, slow drop and failed admit with 2 connections.
- N tabs × per-connection rate gates = N× profile/typing budget (friends chat: accepted).
- Wide test churn from the resolver signature change.

## Rollback plan

Single branch/PR; revert the merge commit. No data migration, no cookie format change, no
persistent state — reverting restores per-connection identity immediately.
