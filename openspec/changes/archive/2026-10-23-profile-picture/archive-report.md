# Archive report: profile-picture

Archived 2026-10-23. Verdict PASS (see `verify-report.md`); fix round 1 applied (R1–R6).

## Spec merge

- `specs/chat/spec.md`: Purpose sentence (optional `avatar`); CHAT-10 and CHAT-17 requirement
  text replaced, existing scenarios kept, new scenarios appended; CHAT-28 and CHAT-29 added.
  No removals.

## Delivered

Optional profile picture per connection (`?avatar=` on `/ws`, validated server-side against the
image allowlist), carried in `welcome`/`join`/`chat`/history and `users.avatars`; page join
field (remembered in localStorage), pictures in messages, DMs, online list and "MI PERFIL".

## Pending

- Manual UI checklist (`design.md`) — user to verify.
- Follow-ups moved to `backlog.md` → `profile-picture-polish`.
