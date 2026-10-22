# Archive Report: multiple-rooms

**Archived**: 2026-10-22
**Branch**: `feat/multiple-rooms` (base `feat/online-users-and-history`, already merged to `main` as PR #4)
**Commits**: `2b0f9c1` feat(chat,web) code + tests + README, `20e792f` docs(openspec) change artifacts, plus this archive commit.
**Verify**: NOT RUN. The verify phase (reviewer, security, test runner) was stopped by the user;
there is no `verify-report.md`. The user tested the feature manually. The apply worker reported
`gofmt`, `go vet` and `go test -race -count=1 ./...` passing, but no independent re-run.

## Specs merged

`openspec/specs/chat/spec.md`
- Purpose updated: each room is an independent hub; one connection per room; nicknames unique per room.
- ADDED: CHAT-19 Room isolation, CHAT-20 Chat page joins several rooms.
- CHAT-01..18 unchanged in text; CHAT-19 scopes them to the connection's room.

`openspec/specs/server/spec.md`
- Purpose updated (room set and listing).
- ADDED: SRV-06 Room selection on the WebSocket endpoint, SRV-07 Room list.
- MODIFIED: SRV-03 Graceful shutdown (waits for every room; new scenario with two rooms).
- No requirement removed.

## Deviations recorded
See `apply-progress.md` (extra test helpers, `.join-error` color token, double-submit guard,
room id de-duplication). Spec CHAT-20 was aligned with the design before apply (rooms-fetch
failure shows an error; "Dejar sala" disabled with one joined room).

## Deferred
- Independent verification (`/sdd-verify`) — not run.
- Known trade-offs: per-room nickname suffixes, tablist without arrow-key navigation,
  inactive room connection state only visible in its pane, no JS test harness.
- Backlog candidates: global nickname identity, remembering joined rooms across reloads,
  capping live DOM per room.
