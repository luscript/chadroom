# Verify report: profile-picture

Date: 2026-10-23. Sources: crombie-reviewer (static), crombie-security (static), crombie-worker
(gates). Specs: CHAT-10, CHAT-17 (modified), CHAT-28, CHAT-29 (added).

## Verdict: PASS

No CRITICAL or HIGH findings; all gates green.

## Gates

| Gate | Result |
|------|--------|
| `gofmt -l .` | clean |
| `go vet ./...` | clean |
| `go test -race -count=1 ./...` | pass (cmd/chatroom, internal/chat) |
| `go test -race -count=3 -run 'Avatar\|Users\|History' ./internal/chat` | pass (no flakes) |
| `node --check app.js` | ok |
| Coverage | cmd/chatroom 92.3%, internal/chat 97.2% |

## Findings

| ID | Sev | Source | Finding | Resolution |
|----|-----|--------|---------|------------|
| R1 | MEDIUM | review + security L1 | Page check looser than server (`new URL` normalizes uppercase host, `:443`, IDN) → silent join without picture | Fixed in round 1 (raw-prefix check in `validAvatarInput`) |
| R2 | MEDIUM | review | `TestValidAvatar` missing the spec's look-alike host case | Fixed in round 1 |
| R3 | LOW | review | Invalid UTF-8 avatar reaches JSON (U+FFFD) | Fixed in round 1 (`utf8.ValidString`) |
| R4 | LOW | review + security L6 | `#profile-avatar` lacks `referrerpolicy` (header covers it) | Fixed in round 1 |
| R5 | LOW | review | Wire-level test for existing client's `join` with avatar | Fixed in round 1 |
| R6 | LOW | review | `TestUsersFrameAvatars` newcomer-without-avatar spec case | Fixed in round 1 |
| R7 | LOW | review | `renderUsers` rebuilds `<img>` on every join/leave | Accepted (browser cache) |
| S2 | LOW | security | Avatar repeated in every chat/history frame + users frame → output amplification with no client cap | Backlog (`rate-limiting`) |
| S3 | LOW | security | Copying another user's picture + similar nickname → cosmetic impersonation | Backlog (`nickname-polish`) |
| S4 | LOW | security | gin access log records `/ws` query (avatar + nickname) | Backlog (`gif-picker-polish` already tracks query stripping) |
| S5 | LOW | security | Viewers fetch large/animated images chosen by others; IPs reach CDNs | Accepted (same hosts as inline GIFs, `no-referrer`) |

## Spec coverage

Every scenario of CHAT-10 (added), CHAT-17 (added), CHAT-28 is covered by Go tests
(hub + integration + `TestValidAvatar`, `TestWebSocketAvatarWiring`). CHAT-29 page behavior
is covered by `TestIndexHasAvatarField` (markup) and the manual UI checklist in `design.md`
(pending user check).
