# Archive report: account-sessions

Archived 2026-11-03, before merge (shipped together with the code in one PR). Verdict PASS in
round 2 (see `verify-report.md`). Manual two-window check pending (backlog `account-sessions-polish`).

## Spec merge

- `specs/chat/spec.md`:
  - CHAT-02: join on the first connection of an account, leave on its last + 3 scenarios.
  - CHAT-04: renamed "Client identity is the account"; defines account and member. Existing
    scenarios reworded to different accounts + "Two connections of one account share the nickname".
  - CHAT-11: a later connection's `welcome` carries the member profile + 1 scenario.
  - CHAT-12: uniqueness among members; suffix only for a different member; name freed on the last
    connection + 2 scenarios.
  - CHAT-17: one entry per member + 1 scenario.
  - CHAT-22: DM to every connection of both accounts + 1 scenario.
  - CHAT-23: `self` / `offline` by account and member + 1 scenario.
  - CHAT-26: typing excludes the sender's connections; DM typing to every recipient connection
    + 1 scenario.
  - CHAT-30: `music` snapshot before `history`; halt on the last connection + 2 scenarios.
  - CHAT-38: profile per member + 1 scenario.
  - CHAT-41: email is the account key; no account key → not admitted; "Same account in two tabs"
    replaced + "Different accounts with the same name".
  - New CHAT-43 (chat page with several connections of one account).
- `specs/server/spec.md`: unchanged.
- No requirement removed. The CHAT-41 scenario "Same account in two tabs" was changed from
  `Ana Pérez (2)` to the shared nickname.

## Docs

- `README.md`: frame order and the "Cuentas" paragraph.
- `CLAUDE.md`: `chat.IdentityFunc`, account-key privacy, `member`; no change in progress.

## Follow-ups

- `openspec/backlog.md`: `account-sessions-polish` (manual checklist, per-connection rate gates,
  per-room profiles, nickname frozen while online); the logout-closes-sockets item now notes the
  per-session vs per-account decision.
