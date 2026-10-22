# Tasks: Structured messages and nickname hardening

> Spec ↔ design reconciled, no divergences remaining. Coverage: CHAT-10 → envelope /
> marshal-once decisions (`message.go`); CHAT-11 → registration handshake (`registered`,
> `deliver`); CHAT-12 → `taken` set + `foldKey` + suffix algorithm; CHAT-13 → blank-frame drop
> in `readPump`; CHAT-14 → frontend decision (`index.html`); CHAT-01/02/04/06 → envelope
> migration in `Run`; CHAT-09 → `normalizeNickname` order. Two spec adjustments made during
> reconciliation to match the design: U+061C added to the stripped bidi set, and trailing
> whitespace trimmed from the truncated base before the suffix. Design-only hardening: the hub
> drops `inbound` from non-members (implied by CHAT-01 "from a registered client").

## Batch plan (file independence)

| Batch | Worker | Tasks | Files |
|-------|--------|-------|-------|
| 1 (parallel) | W1 | 1.1–1.2, 2.1–2.4, 4.1–4.4 | internal/chat/message.go, nickname.go, room.go, client.go, room_test.go, client_test.go |
| 1 (parallel) | W2 | 3.1–3.3 | cmd/chatroom/index.html, README.md |
| 2 (parallel) | W3 | 4.5 | internal/chat/client_test.go |
| 2 (parallel) | W4 | 4.6 | cmd/chatroom/main_test.go |
| — | orchestrator | 5.1 | openspec/project-map.yaml |

Batch 2 depends on batch 1 (W1's server behavior). 4.4 moved into W1: `client_test.go` is
in package `chat`, so the package's tests do not compile until it is migrated too. W2 only depends on the wire contract
(spec CHAT-10, design "Interfaces / Contracts").

## Phase 1: Infrastructure — new types and helpers (`internal/chat`)

- [x] 1.1 Create `internal/chat/message.go`: type constants `typeChat/typeJoin/typeLeave/typeWelcome`; `message{Type, From, Text, TS}` with JSON tags `type, from, text, ts` (no `omitempty`); `inbound{from *Client; text string}`; `encode(m message) []byte` (marshal once; `json.Marshal` of this struct cannot fail — log and return nil defensively per design).
- [x] 1.2 Create `internal/chat/nickname.go`: move `normalizeNickname` from `room.go` and add the strip step (Cc via `unicode.IsControl`/`unicode.Cc` + `unicode.Bidi_Control`) in the CHAT-09 order (ToValidUTF8 → strip → TrimSpace → truncate 32 → `Anonymous`); `truncateRunes(s, n)`; `foldKey(s)` (each rune → smallest rune of its `unicode.SimpleFold` orbit); `(*Room).assignNickname(base) string` (lowest free ` (n)`, n ≥ 2, base truncated to fit 32 runes then right-trimmed of whitespace).

## Phase 2: Core implementation — hub and pumps (`internal/chat`)

- [x] 2.1 `client.go`: add `registered chan struct{}` to `Client` (doc: closed by `Run` once nickname is final); `readPump` drops frames where `strings.TrimSpace(text) == ""` (connection stays open) and sends `inbound{c, text}` to `broadcast`.
- [x] 2.2 `room.go`: `broadcast chan inbound`; `taken map[string]struct{}` initialized in `NewRoom`; extract `deliver(c, msg) bool` (non-blocking send, silent drop + `remove` on full buffer) and make `fanOut` loop over it; add `remove(c)` that deletes from `clients` and `taken` and closes `send` — used by unregister, slow drop and shutdown.
- [x] 2.3 `room.go` `Run`: register case → `assignNickname`, set `c.nickname`, add to `clients`/`taken`, `close(c.registered)`, `deliver` welcome, then `fanOut` join (one `ts` per event); unregister case → `remove` + `leave` envelope (only if member); broadcast case → ignore non-members, else `fanOut` chat envelope with `from = in.from.nickname`. Keep CHAT-03 (never send on own channels) and silent drop/shutdown.
- [x] 2.4 `room.go` `HandleWebSocket`: build client with `registered`; keep `select { register <- c | <-done }`; then `<-c.registered` before starting `writePump`/`readPump`; log using the final nickname only after that point.

## Phase 3: Integration — client page and docs

- [x] 3.1 `cmd/chatroom/index.html`: replace the `addRaw` regex with `JSON.parse` in `try/catch` + `switch (msg.type)`; `welcome` stores `myName`; `chat` renders bubble with `from`, `text` and time from `ts`; "own" iff `from === myName`; `join`/`leave` render system lines (`<from> se unió 🎉` / `<from> salió 👋`); unknown types and invalid JSON ignored. All strings via `textContent`. Keep the Nocturne styling.
- [x] 3.2 `cmd/chatroom/index.html`: anything that showed the requested nickname as the user's identity (header/avatar, if present) switches to the `welcome` name.
- [x] 3.3 `README.md`: `/ws` documentation describes inbound plain text and the outbound JSON envelope (`type`, `from`, `text`, `ts`; four types), unique case-insensitive nicknames and the charset rules.

## Phase 4: Testing (`go test -race ./...`, no sleep-based assertions)

- [x] 4.1 `internal/chat/room_test.go` — mechanical migration first (no new behavior in the same step): `decode`/`expect` helpers; `newTestClient` makes `registered`; string assertions → envelopes; leading `welcome` for every newcomer; slow-client buffers 2 → 3; `TestDroppedSlowClientConnectionIsClosed` uses `registered` + 2-slot buffer; `TestSaturationNoDeadlock` pre-fills `broadcast` with `inbound` from a registered listener.
- [x] 4.2 `internal/chat/room_test.go` — new unit tests: `TestFoldKeyMatchesEqualFold`; `TestNormalizeNickname` extended with the CHAT-09 table (incl. U+061C, U+0085, ZWJ emoji kept, control-only → `Anonymous`, strip-before-trim); `TestAssignNickname` table per CHAT-12 scenarios (case collision, lowest free reuse, 32-rune base → 28 + ` (2)`, n=10, truncation on a space, `Anonymous (2)`, literal `bob (2)` → `bob (2) (2)`, all ≤ 32 runes).
- [x] 4.3 `internal/chat/room_test.go` — new hub tests: `TestWelcomeFirst` (CHAT-11, CHAT-02 ordering); `TestNameReleasedOnRemove` via unregister and via slow drop (CHAT-12); `TestBroadcastFromNonMemberDropped`; `ts` within `[before, after]` and identical across recipients (CHAT-10).
- [x] 4.4 `internal/chat/client_test.go` — mechanical migration: `readMsg`/`readUntil` helpers; `dial` helper reads the `welcome` and returns the assigned name; string assertions → envelopes (CHAT-01/02/06/09); `expectLeaveWithoutBroadcast` checks `Type == chat && From == nick`; at-limit test uses a 4096-byte frame of `"` and checks decoded text (CHAT-06).
- [x] 4.5 `internal/chat/client_test.go` — new integration tests: `TestConcurrentSameNickname` (20 dials of `bob`, distinct `foldKey`s, ≤ 32 runes); `TestCaseInsensitiveCollision` (`Bob`/`bob`/`BOB` → `Bob`/`bob (2)`/`BOB (3)`, release then `bob`); `TestEmptyMessagesDropped` (CHAT-13 both scenarios); `TestForgedNoticeIsChat` (CHAT-10 forged notice/sender + JSON-escaping scenario); nickname control/bidi stripping end-to-end via `welcome`.
- [x] 4.6 `cmd/chatroom/main_test.go`: `TestRunGracefulShutdown` (and any test that reads frames) asserts the first frame is a `welcome` envelope.
- [ ] 4.7 Run `gofmt -l .`, `go vet ./...` and `go test -race ./...`; all clean. Manual check of the page in two tabs: same nickname → ` (2)`, "vos" only on own bubbles, join/leave lines, times shown, unknown `type` ignored (CHAT-14).

## Phase 5: Housekeeping

- [x] 5.1 `openspec/project-map.yaml`: describe new files `message.go`/`nickname.go`, the JSON wire protocol in api_surface, and re-stamp the header.
