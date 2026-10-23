# Archive Report: private-chat

**Archived**: 2026-10-23
**Verdict at archive**: PASS (verify-report.md; 0 CRITICAL / 0 HIGH, all gates green)

## Summary

1-to-1 direct messages between users online in the same room. Inbound frames accept
`{"type":"chat"}` / `{"type":"dm","to"}` envelopes (plain text stays room chat); new outbound
`dm` (recipient + echo, same `ts`) and `dm_error` (`invalid` / `self` / `offline`). The
server stores no DM after routing it (spec-phase amendment to the proposal). The page shows
DMs as `@Nick` tabs per room, closed on peer leave / connection loss, reopened with a
"puede ser otra persona" divider.

## Specs synced

- `openspec/specs/chat/spec.md`: Purpose, CHAT-10, CHAT-13, CHAT-14, CHAT-18 replaced;
  CHAT-21 to CHAT-25 added. No requirement removed.
- `openspec/specs/server/spec.md`: unchanged.

## Artifacts

exploration.md, proposal.md (with amendment), specs/chat/spec.md, design.md, tasks.md
(all [x]), apply-progress.md, verify-report.md, token-tracker.md (complete).

## Pending

- Manual UI checklist in design.md: passed (user-verified 2026-10-23).
- Follow-ups added to `openspec/backlog.md` under `private-chat-polish`.
