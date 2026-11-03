# Verify report: message-replies

**Verdict**: **PASS** (0 CRITICAL, 0 HIGH; all gates green)
**Date**: 2026-10-29

## Gates (crombie-worker)

| Gate | Result |
|------|--------|
| `gofmt -l .` | clean |
| `go vet ./...` | clean |
| `go build ./...` | ok |
| `go test -race -count=1 ./...` | ok (`cmd/chatroom`, `internal/chat`) |
| `go test -race -count=5 … -run 'Reply\|ID\|DM\|ParseInbound\|Inbound'` | ok, no flakiness |
| `node --check cmd/chatroom/app.js` | ok |

Gates re-run green after the follow-up batch below.

## Review (crombie-reviewer)

All CHAT-10/18/21/22/35 server scenarios are implemented and tested. Every CHAT-36 page scenario is
implemented. Hub pattern respected (no locks; only `Room.Run` touches ids and the ring).

| Sev | Finding | Resolution |
|-----|---------|------------|
| MEDIUM | Ring eviction test not at the exact 200/201 boundary | **Fixed** — `TestReplyRingBoundary` |
| MEDIUM | ↩ button on a closed DM conversation did nothing | **Fixed** — `closeConversation` removes the buttons |
| MEDIUM | Reply button hidden on touch devices (hover only) | **Accepted** — desktop-only scope (proposal); backlog |
| MEDIUM | CHAT-36 has no automated behavioral tests (no JS harness) | **Accepted** — covered by the manual checklist (`design.md`) |
| LOW | Misplaced `CHAT-21, CHAT-13` comment in `client_test.go` | **Fixed** |
| LOW | Blank line splitting `dmFrame` fields | **Fixed** |
| LOW | `isImageQuote` is host-only (looser than CHAT-15) | **Kept** by design; comment added |
| LOW | No end-to-end test that blank/rate-limited frames use no id | Accepted (dropped in `readPump`, never reach the hub) |
| LOW | Read order in `TestMessageIDsSharedByChatAndDM` | Accepted (correct; cosmetic) |
| LOW | Quote inside a GIF bubble (`.bubble.media`) styling | Check in manual checklist step 4 |

## Security (crombie-security)

No CRITICAL/HIGH/MEDIUM. `reply_to` parsing rejects every non-integer and value < 1 without
invalidating the message; `id`/`reply` are server-only (forged inbound fields tested); DMs never
enter the ring (CHAT-24 intact); per-room state (CHAT-19); quotes flat and bounded; textContent
only, no `<img>` from quotes, CSP unchanged; nothing logged.

| Sev | Finding | Resolution |
|-----|---------|------------|
| LOW | DM quote kept full text (~4 KB) in the DOM | **Fixed** — cut to 100 runes in `dmQuote` |
| LOW | `reply_to` probing reaches 200 room messages (> 50 history), incl. before joining | Accepted risk (room-public; each probe is a visible message) |
| LOW | Shared id counter reveals DM timing (not who/what) | Accepted risk; backlog: separate DM counter |

## Pending

- 5.2 Manual two-browser checklist (`design.md`) — user.
