# Verify Report: typing-indicator

**Date**: 2026-10-23
**Verdict**: PASS (no CRITICAL/HIGH findings; all automated checks green)

Executors: crombie-worker (execution), crombie-reviewer (static), crombie-security (static), in parallel.

## Automated checks

| Check | Result |
|-------|--------|
| `gofmt -l .` | clean |
| `go vet ./...` | clean |
| `go build ./...` | ok |
| `go test -race -count=1 ./...` | ok (`cmd/chatroom`, `internal/chat`) |
| `go test -race -count=5 -run Typing ./internal/chat/` | ok (no flakes) |
| `node --check cmd/chatroom/app.js` | ok |

## Spec coverage

| Scenario | Coverage |
|----------|----------|
| CHAT-21 plain text / chat envelope / forgery / invalid DM | existing tests (unchanged) |
| CHAT-21 JSON not an envelope (now `poke`) | `TestChatEnvelopeAndForgery`, `TestParseInbound` |
| CHAT-21 typing envelope is not chat | `TestTypingOverWebSocket`, `TestParseInbound` |
| CHAT-21 invalid typing discarded | `TestInvalidTypingDiscarded`, `TestParseInbound` |
| CHAT-26 room typing to everyone else | `TestTypingRoomExcludesSender` |
| CHAT-26 sender cannot be forged | `TestTypingOverWebSocket` |
| CHAT-26 direct typing to recipient only | `TestTypingDirectRecipientOnly` |
| CHAT-26 absent / self silent | `TestTypingSelfAndOfflineSilent` |
| CHAT-26 not history | `TestTypingNotInHistory` |
| CHAT-26 throttled | `TestTypingGate`, `TestTypingThrottledOverWebSocket` |
| CHAT-26 full buffer does not disconnect | `TestTypingFullBufferSkips` |
| CHAT-26 non-member dropped / not logged | `TestTypingFromNonMemberDropped`, `TestTypingNotLogged` |
| CHAT-27 (all page scenarios) | manual checklist (`design.md` → Manual UI checklist); placement/a11y by `TestIndexHasTypingLine` |

## Findings

### Reviewer
- MEDIUM `TestTypingNotLogged` only checked substrings → follow-up V1 (assert empty log).
- MEDIUM CHAT-26 liveness clause untested → follow-up V4 (only if cheap; otherwise accepted, code order verified statically).
- LOW `Room.broadcast` comment outdated → follow-up V2.
- LOW invalid typing should be shown not to consume the throttle budget → follow-up V3.
- LOW throttle WS test could fail under a >1 s stall (accepted; `TestTypingGate` is deterministic).
- LOW `typingGate.allow` lacks its own doc comment (cosmetic). Page has no JS tests (accepted by design).

### Security (OWASP A01/A03/A05 clean; no secrets, deps or SSRF)
- LOW typing frames use send-buffer slots, so an already-stuck client is evicted slightly sooner (optional ¾-full skip; not needed).
- LOW invalid typing frames are parsed without a rate limit (same as any frame) → backlog `rate-limiting`.
- LOW throttle is per connection, not per user/IP → backlog `rate-limiting` (connection cap).
- LOW (pre-existing) malformed `dm` → unthrottled `dm_error` to sender → backlog `rate-limiting`.

## Follow-ups applied after verify
See `apply-progress.md` → Batch 2.

## Pending (user)
- 5.2 Manual UI checklist in a browser (two tabs / two browsers, both skins).
