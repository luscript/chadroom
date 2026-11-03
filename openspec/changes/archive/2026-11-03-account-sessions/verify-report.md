# Verify Report: account-sessions

## Round 1 — Verdict: FAIL (1 HIGH)

Executors: crombie-reviewer (static), crombie-security (static), crombie-worker (execution).

### Execution (crombie-worker)

| Check | Result |
|-------|--------|
| `gofmt -l .` | clean |
| `go vet ./...` | clean |
| `go build ./...` | ok |
| `go test -race -count=1 ./...` | ok (291 PASS lines) |
| Flakiness `-count=20` on the multi-tab tests | ok |
| `go test -race -count=5 ./internal/chat/` | ok |
| `node --check cmd/chatroom/app.js` | ok |
| `govulncheck` | not installed |

### Security (crombie-security): 0 CRITICAL / 0 HIGH / 0 MEDIUM / 3 LOW

- LOW: rate gates per connection → N tabs × budget (accepted, out of scope; backlog).
- LOW: 404/401/403 order is not an oracle (`/ws` is behind `Require`). Info only.
- LOW: `logError` logs the nickname with `%q` (not the account key). Info only.
- Verified: the account key is never encoded or logged; `queryIdentity` exists only in `_test.go`;
  no cross-account takeover; the second connection can't overwrite the profile; no new DOM sinks.

### Review (crombie-reviewer): 0 CRITICAL / 1 HIGH / 3 MEDIUM / 6 LOW

- **HIGH**: README (`:292-293`, `:315`) still documents `welcome → users → history → music`, with no
  multi-connection or 401 notes; CLAUDE.md:77 still says `chat.NicknameFunc`.
- MEDIUM: base CHAT-30 text is resolved by this delta at archive time (no action beyond the archive).
- MEDIUM: `app.js` `saveProfile` sets `resendProfile` only for `CONNECTING`; a socket that is CLOSED
  and waiting to retry reconnects as a later connection, so its welcome overwrites the pending edit.
- MEDIUM: test gaps: multi-connection shutdown is silent; both recipient connections dropped via
  `deliverPrivate` → one leave.
- LOW: misplaced comment above `closeSnapshotSlot`; the `privateFrames` comment; an untimed
  `<-z.registered`; the dedupe test lacks `expectClosed`; the sleep comment in
  `TestTwoTabsOverWebSocket` (not flaky).

All spec scenarios are covered by tests except CHAT-43, which is manual only (there is no JS harness).

## Round 2 — Verdict: PASS

Fixes applied by crombie-worker (F1–F6):
- F1 (HIGH) README: frame order `welcome → users → [music] → history → join`; new "Cuentas"
  paragraph (identity = account, join on first / leave on last connection, DMs to all connections,
  401 without an account). Checked by the orchestrator.
- F2 (MEDIUM) `saveProfile` marks `resendProfile` for every joined room whose socket is not OPEN.
  Checked by the orchestrator.
- F3 comments in `app.js`; F4 new tests `TestShutdownWithTwoConnectionsIsSilent`,
  `TestDMBothRecipientConnectionsDroppedLeaveOnce`; F5 test nits; F6 sleep comment.

Execution: `gofmt -l .` clean, `go vet ./...` clean, `go test -race -count=1 ./...` ok,
new tests `-count=10` ok, `node --check` ok.

Open (not blocking):
- CLAUDE.md:77 still names `chat.NicknameFunc` (now `chat.IdentityFunc`). This is left for the
  user to confirm, because CLAUDE.md is project instructions.
- CHAT-43 is verified manually only (two windows of one account: `musica` snapshot/pause, DM from
  the other window, profile sync, a fresh window does not wipe the profile).
- The security LOWs (per-connection rate gates × N tabs) go to the backlog under `rate-limiting`.
