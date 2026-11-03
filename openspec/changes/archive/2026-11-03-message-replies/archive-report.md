# Archive report: message-replies

Archived 2026-11-03. Verdict PASS (see `verify-report.md`); shipped in PR #19.

## Spec merge

- `specs/chat/spec.md`: CHAT-10 gains optional `id` and `reply` (only on `chat`, server-produced,
  field order) + "Join envelopes stay unchanged" scenario. CHAT-18 `messages` keep `avatar`, `id`,
  `reply` + "History keeps ids and quotes". CHAT-21 accepts `reply_to` in room/direct messages
  (malformed → ignored) + 2 scenarios. CHAT-22 `dm` gains `id` and optional `reply_to` (not looked
  up by the server) + 1 scenario. New CHAT-35 (message ids and room replies) and CHAT-36 (chat
  page replies).
- No removals.

## Notes

- Archived late, together with `user-profiles` and `google-oidc-login` (their deltas build on
  CHAT-35/36). Orchestrator tokens were not captured in that earlier session.
