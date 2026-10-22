# Verify Report: online-users-and-history

**Verdict**: PASS — no CRITICAL/HIGH findings; all checks green; round-1 MEDIUM fixes applied and re-verified (`go test -race -count=3 ./...` pass).
**Date**: 2026-10-22

## Checks (crombie-worker)

| Command | Result |
|---------|--------|
| `gofmt -l .` | clean |
| `go vet ./...` | clean |
| `go build ./...` | ok |
| `go test -race -count=10 ./...` | ok — `cmd/chatroom` 1.1s, `internal/chat` 30.8s; no flakes |
| targeted `Join\|History\|Users\|Slow\|Drop\|Leave\|Online` | 18/18 PASS |
| `node --check cmd/chatroom/app.js` | ok |
| `govulncheck ./...` | skipped (not installed) |

## Review (crombie-reviewer) — 0 CRITICAL, 0 HIGH, 3 MEDIUM, 8 LOW

Hub correctness confirmed: Run-only ownership, no self-sends, leave exactly once on every
path (drain, slow-drop-then-unregister, cascade), silent newcomer drop and shutdown, history
stores only members' chats, `[]` never `null`.

| ID | Sev | Finding | Action |
|----|-----|---------|--------|
| R-M1 | MEDIUM | CHAT-17 "Dropped slow client leaves the list": snapshot exclusion not asserted | Fixed (round 1) |
| R-M2 | MEDIUM | CHAT-18 "Replayed message keeps its original data": `ts` not asserted | Fixed (round 1) |
| R-M3 | MEDIUM | Slow-buffer sizes are magic numbers tied to 3 private frames | Fixed (`privateFrames` const) |
| R-L2 | LOW | `renderHistory` leaves `aria-busy`/`outage` set if a render throws | Fixed (`try/finally`) |
| R-L1 | LOW | After reconnect, own "X se unió" follows "Reconectado" | Backlog (UX choice) |
| R-L3 | LOW | `aria-busy` toggled in one task; screen-reader behavior unverified | Manual check |
| R-L4 | LOW | "0 en línea" while connecting / stale count during outage | Backlog |
| R-L5 | LOW | `TestHistoryKeepsLast50` exercises one eviction | Backlog (test polish) |
| R-L6 | LOW | `TestShutdownIsSilent` `goto` hard to read | Backlog (test polish) |
| R-L7 | LOW | `TestChatBeforeLeaveOnDisconnect` missing `expectEmpty` | Backlog (test polish) |
| R-L8 | LOW | Unkeyed struct literals | Existing style; no action |

## Security (crombie-security) — 0 CRITICAL, 0 HIGH, 0 MEDIUM, 5 LOW

Clean: all server strings via `textContent`; history images still gated by
`isAllowedImage`; CSP/no-inline intact; users/history private to the newcomer and sent only
after registration; cascade loop bounded; malformed frames ignored.

| ID | Finding | Action |
|----|---------|--------|
| S-L1 | History frame bounded by count, not bytes (~1.2 MB worst case) | Backlog |
| S-L2 | Per-join sort + encode inside Run; cache history frame | Backlog (with rate-limiting) |
| S-L3 | `attempt` resets on every welcome → fast reconnect loop on flapping server | Backlog |
| S-L4 | History visible to any joiner (no auth, by design) | Documented |
| S-L5 | `replaceChildren(...names)` spread on huge arrays (malicious server only) | Backlog |

## Spec coverage

Automated: CHAT-02 (all), CHAT-05, CHAT-10, CHAT-11, CHAT-17 server side (snapshot, first
client, sort, dropped excluded), CHAT-18 (newcomer, empty, last 50, notices not kept,
original data incl. ts).

Manual (no JS harness — accepted): CHAT-14 regression pass; CHAT-16 reconnect re-sync
(no duplicates, "Reconectado" after history, server restart notices, rename after
reconnect); CHAT-17 page (count/list follow join/leave/slow drop, "vos" tag, popover
Escape/outside click); ≤560px header layout.
