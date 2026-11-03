# Archive report: user-profiles

Archived 2026-11-03. Shipped in PR #20 (see `verify-report.md`).

## Spec merge

- `specs/chat/spec.md`: CHAT-10 gains optional `bio` (only on `join`/`welcome`, field order
  `…avatar, bio, id, reply`), `profile` frame listed, `bio` never taken from payloads. CHAT-17
  gains optional `bios`. CHAT-21 gains step 2b (profile update) and `avatar`/`bio` in the ignored
  fields sentence + "Profile frame is not chat". CHAT-28: the picture changes only via a valid
  profile update; history keeps the old avatar + 1 scenario. CHAT-29: no picture field in the
  join form, profile kept in page memory only, legacy `chadroom.avatar` deleted; scenario
  "Invalid field blocks joining" removed. New CHAT-37 (bio), CHAT-38 (profile update), CHAT-39
  (profile editor), CHAT-40 (user popup).
- Removal: CHAT-29 scenario "Invalid field blocks joining" (the field no longer exists).

## Notes

- Archived together with `message-replies` (merged first; this delta builds on CHAT-35/36) and
  `google-oidc-login`. Orchestrator tokens were not captured in that earlier session.
