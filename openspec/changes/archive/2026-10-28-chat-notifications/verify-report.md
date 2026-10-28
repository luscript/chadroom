# Verify Report: chat-notifications

**Verdict: PASS**
**Date**: 2026-10-28

No CRITICAL/HIGH findings, no failing gate. One MEDIUM fixed before the verdict (T901).
Manual two-browser checklist (T302, design.md) is deferred to the user.

## Gates (crombie-worker, Bash)

| Gate | Result |
|------|--------|
| `gofmt -l .` | clean |
| `go vet ./...` | clean |
| `go build ./...` | ok |
| `go test -race ./...` | PASS (`cmd/chatroom`, `internal/chat`) |
| `node --check cmd/chatroom/app.js` | ok |

Re-run after the fix round: see apply-progress.md.

## Code review (crombie-reviewer, static)

- CRITICAL/HIGH: none. Notifying filter, hooks, tab counter, resets and audio unlock match
  CHAT-32/CHAT-33/SRV-02.
- **MEDIUM (FIXED, T901):** `playBlip` stamped the 1 s window and scheduled on a suspended
  `AudioContext` (frozen clock) → stale late beep. Now skips (and resumes) unless `running`.
- **MEDIUM (partially addressed, T902):** markup test only checks strings; no JS harness. Added
  assertions on the hook calls; JS unit tests go to the backlog.
- LOW (fixed): brittle `" on"` match → regexp; `t.Run` per SVG; explicit node disconnect.
- LOW (accepted): Safari may not repaint a swapped favicon (title still updates); pressed
  colour of the mute button to check visually; mute click does not close popovers (same as
  other `.tool` buttons).

## Security audit (crombie-security, static, OWASP)

- CRITICAL/HIGH/MEDIUM: none. SVGs inert; no untrusted data reaches title/href/DOM; CSP
  unchanged and sufficient; localStorage compared with `=== '1'` only.
- LOW (backlog): serve the SVGs with their own `Content-Security-Policy: default-src 'none'; sandbox`
  as defence in depth if someone navigates to them directly.
- LOW (note): favicon href must stay one of the two constants.
