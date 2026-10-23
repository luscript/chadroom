# Proposal: Private chat (direct messages)

## Intent

Today every message goes to everyone in the room. Coworkers on the office LAN want to send a
short message to one person without leaving the chat. This change adds **1-to-1 direct
messages (DMs)** between users who are **online**. DMs are **kept in memory only** (no disk, no
database) and are never shown to anyone else.

## Decisions

User-decided (2026-10-22):
- **1-to-1 DMs only.** No private rooms and no group conversations.
- **DMs are stored in memory.**
- **DMs can only be sent to online users.** There is no offline delivery.
- **Documentation only (proposal).** Specs, design, and tasks come later.

Orchestrator recommendations, confirmed by the user (2026-10-22): DMs are scoped to the room,
and a pair's DMs are deleted when either participant leaves (no replay after a reconnect).

- **DMs are scoped to a room.** You DM someone who is in the same room, through that room's
  socket, and both sides use the nicknames assigned in that room. The recipient lookup happens
  inside the room's `Run` goroutine, using `foldKey` on the room's own `clients`/`taken`. This
  needs no cross-room registry, no locks, and no new identity model, and it keeps the hub
  invariants (only `Run` touches clients and closes `send`). A global DM across rooms was
  rejected for now: nicknames are only unique per room (`Ana` vs `Ana (2)`), so "who is Ana" is
  ambiguous, and it would need a mutex-guarded server-wide registry plus cross-goroutine
  delivery.
- **In-memory storage lives as long as the participants are present.** The room keeps each
  pair's conversation (`foldKey(a)`, `foldKey(b)`, capped at 50 messages like room history,
  evict oldest) **only while both participants are in the room**. When either one leaves
  (including a disconnect or a slow-drop), the pair's conversation is deleted. Reason: nicknames
  are unauthenticated, so if history were kept by name, whoever takes `Ana` after she
  disconnects would inherit her private messages. The page keeps the DM transcript it already
  rendered on screen, so a reconnect does not blank what the user saw. The server just won't
  replay it.
- **Inbound frames become a JSON envelope, and plain text stays as room chat.** The page sends
  `{"type":"chat","text":"…"}` or `{"type":"dm","to":"<nick>","text":"…"}`. Any frame that is
  not a valid envelope of a known type is treated as plain-text room chat (as today), so stale
  pages keep working. The same 4096-byte cap, UTF-8 check, and blank-text filter still apply
  (blank = blank `text` after parsing). The exact parsing rules go in `design.md`.
- **New outbound frames, separate from the 4-field `message` envelope** (CHAT-10 stays the same
  for existing types):
  - `{"type":"dm","from","to","text","ts"}` is sent to the **recipient and echoed to the
    sender**, so the sender sees the server timestamp and knows the message was delivered.
  - `{"type":"dm_error","to","reason","ts"}` is sent only to the sender, with reason
    `offline` (recipient not in the room), `self`, or `invalid`.
  - Old pages ignore both types (CHAT-14).
- **A DM is never written to room history** and never fanned out to the room.
- **Peer leaves**: no new frame is needed. The room's existing `leave` frame tells the page,
  which marks the DM conversation as "<nick> se desconectó" and disables its composer.
- **UI**: each name in the users popover (except "vos") gets a "Mensaje privado" button. This
  opens or activates a DM tab labeled `@Nick` inside the owning room's tab group, with its own
  pane and an unread badge (reusing the room badge logic). The composer sends to whatever is
  active (room or DM). DM conversations are a **separate client structure** from rooms, so
  `Dejar sala`, the joined-room count, and reconnect logic stay room-only. Leaving a room closes
  its DM tabs. Nicknames are rendered only with `textContent` and are never used in element ids
  or selectors. The layout goes in `design.md`.

## Spec-phase amendment (2026-10-23)

**No server-side DM store.** The per-pair 50-message store below had no consumer: the server
never replays DMs, so storing them only kept private text in memory longer (YAGNI and
privacy). The server now forgets a DM as soon as it routes it (CHAT-24); "DMs are stored in
memory" is satisfied by the page's in-memory transcript. The purge-on-leave logic and the
50-per-pair cap are dropped with it, and the "reused nickname inherits nothing" guarantee
still holds because nothing is kept. Bullets about the store further down are superseded.

## Scope

### In Scope
- Hub: parse inbound envelopes, route DMs within the room's `Run`, store per-pair DMs in memory
  with a 50-message cap, delete a pair's DMs when either participant leaves, add the `dm` and
  `dm_error` frames.
- Page: DM entry point in the users popover, DM tabs/panes per room, a composer that targets
  the active conversation, unread badges, a peer-offline state, and a switch to JSON envelopes
  for sending.
- Tests (`go test -race ./...`): delivery to recipient plus echo, isolation (third parties and
  room history never see DMs), offline/self/invalid errors, purge on leave (the nickname reused
  after a leave gets no old DMs), plain-text fallback, size and blank limits on DM frames, and
  the 50-message cap.
- Spec deltas: the chat Purpose paragraph (inbound protocol), CHAT-09/10/13/18, and new DM
  requirements.

### Out of Scope
- Global (cross-room) DMs or a cross-room identity.
- Offline delivery, read receipts, typing indicator, group DMs, private rooms.
- Persistence to disk or a database, and DM replay after a reconnect.
- Authentication or session tokens, rate limiting, connection caps (backlog; see Risks).
- JS test harness.

## Approach

1. `internal/chat/message.go`: add the inbound envelope type, the `dm` and `dm_error` frame
   structs, and type constants.
2. `internal/chat/client.go`: `readPump` parses each frame into a chat or DM inbound and pushes
   it to the room's existing broadcast channel (or a sibling channel; decided in design).
3. `internal/chat/room.go`: `Run` handles the DM inbound (sender membership check, recipient
   lookup by `foldKey`, `deliver` to the recipient and echo to the sender, or send `dm_error`),
   keeps the per-pair store, and purges it in `remove`.
4. `cmd/chatroom/app.js`, `index.html`, `app.css`: DM button in `renderUsers`, a DM
   conversation map keyed by room + peer key, `dm`/`dm_error` cases in `addRaw`, `leave`
   handling for open DMs, and `sendText` that emits envelopes.
5. Tests and spec deltas.

## Constraints

- Keep the hub invariants: only `Run` touches clients and history and closes `send`; no locks;
  no cross-room shared mutable state.
- Secure by default: a DM goes **only** to the named recipient and the sender. The sender must
  be a registered member. The recipient string is normalized and folded the same way as
  nicknames. DMs never reach the room history, logs, or other clients. Storage is bounded.
- Backward compatible: a plain-text inbound frame is still room chat, and old pages ignore the
  new frame types.
- No new dependencies, no new HTTP routes, and the CSP and no-inline rules (SRV-05) stay the
  same.
- Spanish (voseo) UI copy, existing CSS tokens and class conventions.

## Affected Areas

| Area | Files | Change |
|------|-------|--------|
| Wire types | `internal/chat/message.go` | inbound envelope, `dm` / `dm_error` frames |
| Read path | `internal/chat/client.go` | parse envelope, plain-text fallback |
| Hub | `internal/chat/room.go` | DM routing, per-pair store, purge on leave |
| Frontend | `cmd/chatroom/app.js`, `index.html`, `app.css` | DM entry point, tabs/panes, envelopes, errors, peer-offline state |
| Tests | `internal/chat/room_test.go`, `client_test.go` | DM delivery, isolation, errors, purge, fallback |
| Specs | `openspec/specs/chat/spec.md` | Purpose, CHAT-09/10/13/18 wording, new DM requirements |

## Risks

| Risk | Likelihood | Mitigation |
|------|------------|------------|
| **Impersonation**: nicknames are unauthenticated, so someone can take a name after its owner leaves, or use a look-alike name (backlog M1), and receive DMs meant for someone else | Medium | Purge a pair's DMs when either side leaves, so there is no inherited history. The peer-offline state shows the disconnect. Document it as a known limitation for LAN or trusted use. Real auth stays in the backlog. |
| A DM leaks to the room (history, fan-out, logs) | Low | DMs use their own frame type and code path and are never passed to `remember`/`publish`. Tests assert that a third party and the history frame see nothing. |
| A plain-text message that happens to look like JSON gets routed as a DM | Low | Only a well-formed envelope with a known `type` is structured. This only affects the sender's own text, and a recipient is still required to be online in the same room. Covered by tests. |
| Unbounded memory from many pairs | Low | 50 messages per pair, and pairs exist only while both participants are present (at most N² pairs over the online users). Rate limit stays in the backlog. |
| Frontend refactor breaks room behavior (tabs, leave, unread) | Medium | DM state is kept separate from the `rooms` Map, plus a manual verify checklist for the room flows. |
| Users expect to DM someone who is in another room | Medium | The DM button only appears for members of the current room. Global DMs are an explicit follow-up. |

## Rollback Plan

The server and page ship as one binary with no persisted data: revert the change's commits and
redeploy. Stale pages keep working against either version, because plain text is still room
chat and unknown frame types are ignored.

## Dependencies

- Builds on `multiple-rooms` (archived; branch `feat/multiple-rooms`). Branch
  `feat/private-chat` from it.

## Open Questions

None. All were resolved by the user on 2026-10-22: DMs go to online users only, they are scoped
to the room, and a conversation is deleted when either participant leaves. DM replay after a
reconnect (it would need a session token) and global DMs are possible follow-ups.

## Success Criteria

- [ ] A DM from A to B in `general` reaches only B and is echoed to A, with the same `ts`.
- [ ] C in the same room, and the `general` history frame sent to a new joiner, contain no trace
      of the DM.
- [ ] A DM to someone not in the room returns `dm_error` `offline`; a DM to yourself returns
      `self`; a malformed or empty `to` returns `invalid`. Nothing is delivered to anyone else.
- [ ] After B leaves, a new user who takes the name `B` receives no earlier A↔B DMs.
- [ ] A raw plain-text frame is still relayed as room chat; DM frames respect the 4096-byte and
      blank limits; the server keeps no DM after routing it.
- [ ] The page opens a `@Nick` tab from the users popover, shows unread counts, marks the peer
      as offline on `leave`, and keeps all room flows working.
- [ ] `go vet ./...` and `go test -race ./...` pass; the CSP and no-inline tests stay the same
      and pass.
