# Archive report: google-oidc-login

Archived 2026-11-03. Verdict PASS (see `verify-report.md`); shipped in PR #21. Manual test done;
`govulncheck` pending.

## Spec merge

- `specs/server/spec.md`: Purpose mentions authentication. SRV-02: `/` serves the chat page only
  with a session and the login page otherwise (same URL, `Vary: Cookie`); `/app.js` protected;
  `/app.css` and favicons public + 2 scenarios. SRV-06: `nickname` not read; session check before
  room and origin; scenario URLs without `nickname=` + "Unknown room without a session". New
  SRV-10 (auth configuration, fail-fast), SRV-11 (login flow), SRV-12 (domain authorization),
  SRV-13 (session cookie), SRV-14 (logout), SRV-15 (protected routes), SRV-16 (login and
  access-denied pages), SRV-17 (login rate limit), SRV-18 (secrets out of logs).
- `specs/chat/spec.md`: Purpose says connections belong to signed-in users. CHAT-16: reconnect with
  the session cookie, "Cerrar sesión", session probe after 3 failed reconnects + 1 scenario.
  CHAT-20: no nickname screen; enter on load; 401 → `/`; scenarios reworded. New CHAT-41
  (nickname from the session) and CHAT-42 (page session handling).
- No requirement removed. The nickname screen and its scenarios were reworded, not deleted.

## Follow-ups

- `openspec/backlog.md`: `google-oidc-login` follow-ups and the next change `account-sessions`.
