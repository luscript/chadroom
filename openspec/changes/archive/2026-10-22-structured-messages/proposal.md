# Proposal: Structured messages and nickname hardening

## Intent

The server builds every frame as free text (`nick + ": " + text`, `nick + " joined! 🎉"`), and
the client guesses what each frame is with a regex. Any user can therefore forge system
notices or other users' messages: sending `Bob joined! 🎉`, picking the nickname `Admin`, or
putting a newline / a bidi override (U+202E) in the nickname. Two users can also share a
nickname, so the client cannot tell them apart (and marks both as "vos"). This change moves
the wire protocol to a typed JSON envelope built only by the server, makes nicknames unique
and free of control/bidi characters, and drops empty messages. It is the foundation for
`chat-features` (user list, rooms, history). Backlog group `structured-messages` (#10).

## Decisions (taken with the user, 2026-10-22)

- **Nicknames are case-insensitive**: `Bob`, `bob` and `BOB` are the same name for every
  comparison (uniqueness, collision, "is this my message"). Comparisons use Unicode case
  folding (`strings.EqualFold` semantics), not ASCII-only lowercasing. The casing the user
  typed is preserved for display.
- **Duplicate nicknames get an automatic suffix**: if `Bob` is taken, a second `bob` joins as
  `bob (2)`; the first free number is used, and names are freed when their owner leaves.
- **Inbound frames stay plain text**: the client sends only the message text; the sender is
  always taken from the connection, never from the payload.

## Scope

### In Scope
- **Outbound JSON envelope** for every server→client data frame:
  `{"type": "...", "from": "<nickname>", "text": "...", "ts": <unix ms>}` with types
  `chat`, `join`, `leave` and `welcome`. Built and serialized only by the hub.
- **`welcome` message** sent only to the joining client, carrying the nickname the server
  actually assigned (it may differ from the requested one), so the UI knows "who am I".
- **Unique nicknames**: uniqueness checked case-insensitively by the hub (the only owner of
  the registry); on collision append ` (n)` with the lowest free `n ≥ 2`, truncating the base
  so the result still fits in 32 runes.
- **Nickname charset rules** (extends current normalization): strip C0/C1 control characters
  (incl. `\n`, `\t`) and bidi controls (U+200E, U+200F, U+202A–U+202E, U+2066–U+2069); then
  trim, truncate to 32 runes, fall back to `Anonymous`. Strip, not reject — consistent with
  today's normalization. ZWJ (U+200D) is kept so emoji sequences survive.
- **Empty messages dropped**: text frames that are empty or whitespace-only after
  `strings.TrimSpace` are silently ignored (connection stays open, nothing is broadcast).
- **Client (`cmd/chatroom/index.html`)**: parse JSON by `type` instead of the regex; take its
  own name from `welcome`; decide "own message" by that name; render `ts` from the server.
- Spec updates (CHAT-01, -02, -04, -09 modified; new requirements for the envelope, unique
  nicknames and empty messages) and test updates (`go test -race ./...`).

### Out of Scope
- JSON for inbound frames, message types beyond the four above (typing, rooms, history) →
  `chat-features`.
- Homoglyph / confusable detection (`Bob` vs Cyrillic `Воb`) and reserved names: with typed
  system messages a nickname can no longer impersonate the *system*; look-alike users stay a
  known limitation.
- Sanitizing control/bidi characters inside message *text* (rendered via `textContent` in
  its own element; revisit in `frontend-hardening` with `dir="auto"` / `unicode-bidi: isolate`).
- Rate limiting, CSP, reconnection → `rate-limiting`, `frontend-hardening`.

## Approach

1. **Message type in `internal/chat`**: a small `message` struct with JSON tags and a helper
   that marshals once per event; `fanOut` keeps receiving `[]byte`, so every client shares one
   encoded buffer (no per-client marshaling).
2. **readPump sends text, not formatted bytes**: `broadcast` carries `{client, text}`; the hub
   builds the `chat` envelope using the hub-owned nickname. This removes the last place
   outside the hub that formats wire output.
3. **Name assignment in the hub**: `Run` keeps a `taken` set (case-folded names). On
   `register` it picks the final nickname, stores it on the client and replies on a
   per-registration channel; `HandleWebSocket` waits for that reply **before** starting the
   pumps, so the pumps read `nickname` only after it is final (no data race). Then the hub
   queues `welcome` to the newcomer and `join` to everyone.
4. **`normalizeNickname`** gains the charset filter before trim/truncate; suffixing reuses the
   same truncation helper.
5. **Frontend**: replace `addRaw` regex with `JSON.parse` + `switch (msg.type)`; unknown types
   are ignored (forward compatible for `chat-features`).

Details (exact JSON field names/order, `ts` precision, suffix truncation algorithm,
registration handshake) go in `design.md`.

## Constraints

- Keep the hub invariants of `fix-channel-handling`: only `Run` touches the registry and
  closes `send`; `Run` never sends on its own channels (CHAT-03); leaves on slow-client drop
  and shutdown stay silent (CHAT-02).
- No new dependencies (`encoding/json` from stdlib).
- Frame size: the 4096-byte limit (CHAT-06) applies to inbound text; outbound envelopes are
  larger (JSON escaping + metadata) and that is fine — no outbound limit exists.

## Affected Areas

| Area | Files | Change |
|------|-------|--------|
| Hub | `internal/chat/room.go` | envelope encoding, name assignment + `taken` set, registration reply, `welcome` |
| Client conn | `internal/chat/client.go` | readPump sends raw text to hub; drop empty messages |
| Nickname | `internal/chat/room.go` (`normalizeNickname`) | control/bidi stripping, suffix-safe truncation |
| Frontend | `cmd/chatroom/index.html` | JSON parsing by type, identity from `welcome` |
| Tests | `internal/chat/*_test.go`, `cmd/chatroom/main_test.go` | assertions move from strings to decoded envelopes (~70 references) |
| Specs | `openspec/specs/chat/spec.md` | modified CHAT-01/02/04/09, new requirements |

## Risks

| Risk | Likelihood | Mitigation |
|------|------------|------------|
| Data race on `client.nickname` (hub writes it, pumps/logging read it) | Medium | Assign before pumps start via registration reply; covered by `-race` tests |
| Registration handshake can block if the room stops mid-register | Medium | Wait on the reply with `select` on `cr.done`, same pattern as today |
| Wire format is a breaking change: an already-open old tab renders raw JSON | Low | Page and server ship in one binary; a reload fixes it. No external clients exist |
| Large test churn hides a behavior regression | Medium | Add a test helper that decodes envelopes; update tests mechanically before adding new ones |
| Suffix truncation splits a grapheme (emoji with ZWJ) | Low | Truncate by runes as today; accepted cosmetic edge case, documented in design |

## Rollback Plan

The change is self-contained in one binary (server + embedded page) with no persisted data:
revert the change's commits and redeploy. Clients reconnect to the plain-text protocol after
reloading the page.

## Dependencies

- Builds on `fix-channel-handling` (archived 2026-10-22, merged into `main` in PR #1).
- Work happens on `feat/structured-messages`, branched from `fix/channel-handling` HEAD so it
  includes `ba91d89` (Nocturne UI redesign, not yet in `main`); the frontend changes build on
  that redesign, so that commit ships with this change's PR unless merged separately first.
- `openspec/project-map.yaml` refreshed before the specs/design phase.

## Success Criteria

- [ ] Every server→client data frame is a valid JSON envelope with `type`, `from`, `text`, `ts`.
- [ ] A user sending `Bob joined! 🎉` or `\nBob: hi` produces a `chat` envelope from the real sender; no client renders it as a system notice or as Bob.
- [ ] With `Bob` connected, a client requesting `bob` or `BOB` joins as `bob (2)` / `BOB (2)`; after the first leaves, a new `bob` gets `bob`.
- [ ] Nicknames with control or bidi characters are stripped; the result is ≤ 32 runes, including any suffix.
- [ ] The joining client receives `welcome` with its assigned name before any other message.
- [ ] Empty / whitespace-only messages are not broadcast and do not close the connection.
- [ ] The page renders chat, join and leave correctly and marks only its own messages as "vos".
- [ ] `go vet ./...` and `go test -race ./...` pass.
