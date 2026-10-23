# Archive Report: gif-picker

**Archived**: 2026-10-23
**Verdict at verify**: PASS (manual browser check user-verified 2026-10-23)

## Summary
GIF search in the GIF panel: `GET /gifs?q=` proxies Giphy trending/search with the server-held
`GIPHY_API_KEY` (route absent → 404 without a key). The server applies fixed upstream params
(limit 24, rating pg-13), a 5 s timeout, a 1 MiB body cap and no redirects. Results are trimmed
to `{url, preview}` and re-validated to Giphy allowlisted hosts with tracking params stripped.
A 10 min / 100-entry in-memory cache protects the quota. The page adds a debounced search box,
a results grid and "Powered by GIPHY", and keeps the paste-a-URL fallback.

## Specs merged into `openspec/specs/server/spec.md`
- MODIFIED: Purpose; SRV-05 scenario "Page works under the policy" (now includes GIF search).
- ADDED: SRV-08 GIF search.
- No removals.

## Files changed
`cmd/chatroom/{gifs.go,gifs_test.go}` (new), `cmd/chatroom/{main.go,main_test.go,index.html,app.js,app.css}`,
`README.md`, `CLAUDE.md`, `openspec/backlog.md`.

## Follow-ups (backlog `gif-picker-polish`)
Quota protection (singleflight + in-flight cap) → `rate-limiting`; search terms in the gin access
log; drain the upstream body on non-200; feedback when a GIF fails to send.

## Note
Running locally: Go does not read `.env`. Use `set -a; . ./.env; set +a` before `go run` (added to CLAUDE.md).
