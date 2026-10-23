# Verify Report: private-chat

**Date**: 2026-10-23
**Verdict**: PASS (0 CRITICAL, 0 HIGH; all gates green)

Executors (parallel): crombie-reviewer (static review), crombie-security (static audit),
crombie-worker (gates).

## Gates

| Check | Result |
|-------|--------|
| `gofmt -l internal cmd` | clean |
| `go vet ./...` | clean |
| `go build ./...` | ok |
| `go test -race -count=1 ./...` | ok (`cmd/chatroom`, `internal/chat`) |
| `go test -race -count=3 -run 'DM\|ParseInbound\|Envelope'` | ok, no flakiness (15 tests, 31 parse cases) |
| `node --check cmd/chatroom/app.js` | ok |
| `govulncheck ./...` | skipped (not installed; backlog `deps-cleanup`) |

## Spec compliance

Every CHAT-21..24 scenario maps to code and a Go test, except the gaps below (fixed in the
fix batch). CHAT-25 (page) is verified by code tracing only; the manual UI checklist in
`design.md` is still pending (no JS harness, accepted in design).

## Findings

| # | Sev | Source | Finding | Resolution |
|---|-----|--------|---------|------------|
| R1 | MEDIUM | reviewer | `TestDMNotLogged` swaps the global logger into an unsynchronized buffer and restores `os.Stderr` | Fixed (V1): mutex-guarded writer, restore `log.Writer()` |
| R2 | MEDIUM | reviewer | No test for CHAT-23 "Recipient left" (`offline` after a real leave) | Fixed (V2) |
| R3 | MEDIUM | reviewer | Slow **sender** case of CHAT-22 not tested | Fixed (V3): `TestDMSlowSenderDropped` |
| S1 | MEDIUM | security | Fail-open fallback: a malformed/case-variant DM envelope (`{"Type":"dm"…}`, truncated JSON) from a non-page client is relayed to the room and kept in history | Accepted per CHAT-21 (backward compat with plain text). The shipped page always sends well-formed envelopes; only the sender's own text is affected. → backlog `private-chat-polish` |
| R4 | LOW | reviewer | `dm_error` with no matching conversation goes to the owning room's pane, not "the active pane" | Spec CHAT-25 wording amended to match (safer: never another room's pane) |
| R5 | LOW | reviewer | Forged DM sender only asserted at parse level | Fixed (V4): socket-level assertion |
| R6 | LOW | reviewer | Missing "why" comments (chat non-string `text` fall-through; `deliverPrivate` membership guard) | Fixed (V5) |
| R7 | LOW | reviewer | README outbound `type` table lacks `dm`/`dm_error` | Fixed (V6) |
| R8 | LOW | reviewer | Redundant `err == nil` in `jsonString`; `joinAll` naming; DM panes unbounded like room panes | No action |
| S2 | LOW | security | `memberByKey` folds every client's nickname per DM inside the hub (O(N) allocations) | Backlog: store folded key on `Client` / `taken` → `*Client` |
| S3 | LOW | security | Recipient dropped as slow still yields an echo to the sender (looks delivered; `leave` follows) | Accepted in design; backlog: send `dm_error offline` instead |
| S4 | LOW | security | JSON parse cost / echo doubling | No new amplification; rate limiting stays in backlog |
| S5 | LOW | security | Nicknames concatenated into system notices (look-alike text) | Existing backlog M1 / L1 |

## Fix batch

V1–V6 applied by crombie-worker; `gofmt -l` clean, `go vet` clean, `go test -race -count=3 ./...`
green. Extra test `TestDMErrorSlowSenderDropped` added.

## Verified (no finding)

DM confidentiality (never `remember`ed, logged, fanned out or retained); `from`/`to` always
from server state; exact-key decoding; `null`/non-string fields rejected; hub invariants
(only `Run`, no locks, drain-before-leave via `dispatch`); no send on closed channel; XSS-safe
rendering (`textContent`/`aria-label`/`placeholder`, counter-based ids); CSP and `index.html`
unchanged; reopen divider shown on both reopen paths.
