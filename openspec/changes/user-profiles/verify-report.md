# Verify Report: user-profiles

## Round 1 — 2026-10-30

**Verdict: PASS** (no CRITICAL/HIGH; tests/build/lint green)

### Execution (crombie-worker)
- `gofmt -l .` clean · `go vet ./...` clean · `go build ./...` ok
- `go test -race -count=1 ./...` green ×2 (`cmd/chatroom` 1.8 s, `internal/chat` 9.8 s)
- `node --check app.js` ok; no `innerHTML`/`insertAdjacentHTML`/`setAttribute('style')`/leftover
  `requestedAvatar`/`avatar-url` (only the intentional `LEGACY_AVATAR_KEY` cleanup)

### Review (crombie-reviewer) — spec checklist CHAT-10/17/21/28/29/37–40 all ✔, hub pattern ✔
| Sev | Finding | Action |
|-----|---------|--------|
| MEDIUM | GIF pick-mode error written to hidden `#profile-error` (`app.js:1325`) | fix round 2 |
| MEDIUM | `myProfile` set before server echo → drift with server (rate gate / resend edge case) | fix round 2: sync own `profile` echo into `myProfile` |
| MEDIUM | `TestProfileConcurrentWithJoins` fails silently on dial/read errors | fix round 2 |
| LOW | `validAvatarInput` empty-branch leftover; stale `broadcast` comment; closure shadows `c` in `admit`; profile mutates before encode | fix round 2 |
| LOW | Spec-literal tests: forged `bio` in chat ignored over the wire; `abc`+157 `x`; clear over the wire | fix round 2 |
| LOW | Save lock not reset on leave; popup stays open while reconnecting | fix round 2 (cheap) |
| LOW | `CLAUDE.md` "En curso" stale | at archive |

### Security (crombie-security) — A01/A02/A03/A05/A10 clean; XSS surface textContent-only ✔
| Sev | Finding | Action |
|-----|---------|--------|
| MEDIUM | gin default logger writes `/ws` query (bio, avatar, nickname) to logs | fix round 2: log `/ws` without query string |
| LOW | History keeps the old avatar for up to 50 messages after a change | accepted by spec (CHAT-28) |
| LOW | `cleanText` keeps zero-width/format chars (Cf, U+2028/2029) → blank-looking bio, look-alike nicks | backlog (changes nickname rule CHAT-09) |
| LOW | `publish` on profile may drop slow clients; no-op updates still broadcast; no connection cap | backlog (`rate-limiting`) |
| LOW | `users` frame grows with bios/avatars under join churn | backlog (`rate-limiting`) |
| LOW | Avatar check before rate gate | accepted (documented, cheap) |

## Round 2 — 2026-10-30 (after fixes)

**Verdict: PASS**

Fixed: security MEDIUM (request log omits query strings: `newRouterLogging` + `logFormatter`,
`TestRequestLogOmitsQuery`); review MEDIUMs (GIF-pick error shown in `gifStatus`; own `profile`
echo adopted into `myProfile` via `adoptOwnProfile`; `TestProfileConcurrentWithJoins` fails
loudly and counts joins/leaves); LOWs (`validAvatarInput` leftover, stale comment, shadowed
closure param, encode-before-mutate in `Room.profile`, save lock reset on leave, popup closed on
reconnect); spec-literal tests `TestForgedInboundBioIgnored`,
`TestBioCleanedAndTruncatedOverQuery`, `TestProfileClearOverWebSocket`. CHAT-37 scenario
wording corrected (interior spaces are kept).

Execution: `gofmt -l .` clean · `go vet` clean · `go test -race -count=1 ./...` green ×2 ·
`node --check app.js` ok.

Remaining: manual two-browser / mobile checklist (task 5.2, `design.md`) — user. Deferred LOWs
added to `openspec/backlog.md`.
