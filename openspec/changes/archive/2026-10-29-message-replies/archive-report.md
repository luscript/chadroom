# Archive report: message-replies

Archived 2026-10-29. Verdict PASS (see `verify-report.md`); shipped in PR #19.

## Spec merge

- `specs/chat/spec.md`:
  - Intro: outbound envelopes mention `id` / `reply` on `chat`.
  - CHAT-10: `id` and `reply` rows, field order, never on join/leave/welcome, server-only;
    new scenario "Join envelopes stay unchanged".
  - CHAT-18: history entries keep `avatar`, `id` and `reply`; new scenario.
  - CHAT-21: `reply_to` accepted on room/direct messages, malformed values ignored; 2 scenarios.
  - CHAT-22: `id` and `reply_to` on `dm` frames, no server lookup (CHAT-24); 1 scenario.
  - Added CHAT-35 (message ids and room replies) and CHAT-36 (chat page replies).
- `specs/server/spec.md`: unchanged.
- No removals.

## Delivered

Server-assigned per-room ids on `chat` and `dm` frames; room replies resolved server-side against
the last 200 room messages with a flat 100-rune quote (also in `history`); DM replies forward
`reply_to` and are resolved by each page. Page: one ↩ button per message, reply preview with
cancel/Escape, quotes that jump to the original, quoted GIFs as text.

## Pending

- T5.2 manual two-browser checklist (`design.md`) — deferred to the user.
- Backlog: reply button on touch screens; separate DM id counter.
