# Verify Report: structured-messages

## Round 1 — 2026-10-22 — Verdict: **FAIL**

Executors (parallel): crombie-reviewer (static), crombie-security (static), crombie-worker (execution).

### Execution (crombie-worker + orchestrator)
| Check | Result |
|-------|--------|
| go version | go1.27.3 linux/amd64 (`/snap/go/current/bin/go`; the `/snap/bin` wrappers swallow output) |
| go build ./... | rc=0 |
| go vet ./... | rc=0 |
| gofmt -l . | **2 files** (`cmd/chatroom/main.go`, `internal/chat/client_test.go`) — earlier "clean" reports used the silent `/snap/bin/gofmt`. Fixed by orchestrator with `gofmt -w` in round 1 (formatting only); tests re-run green. |
| go test -race -v ./... | PASS — 41 top-level tests, 110 incl. subtests |
| go test -race -count=20 ./... | PASS — no flakes |
| coverage | internal/chat 96.3%, cmd/chatroom 80.4% |
| go.mod / go.sum | unchanged (no new deps) |
| Smoke test of real binary (two clients `Bob`/`bob`) | **NOT RUN** — starting the server in background denied by the permission system |

### Findings
| Sev | Source | Location | Finding | Disposition |
|-----|--------|----------|---------|-------------|
| HIGH | reviewer + worker | client_test.go, main.go | gofmt violations | **Fixed** in round 1 |
| HIGH | reviewer | cmd/chatroom/index.html | CHAT-14 (MUST, 3 scenarios) has no automated test and the manual two-tab check (4.7) is not done | Round 2: manual check by developer; optional cheap guard |
| MEDIUM | security M1 | nickname.go:15-20 | Invisible / format chars survive normalization (U+200B, U+FEFF, U+00AD, U+2060–2064, tags, interior U+2028/2029, NBSP and other spaces, Hangul fillers U+3164/115F/1160/FFA0, U+2800) → `Bob​` coexists with `Bob` looking identical; blank-looking names bypass `Anonymous`. Design had deferred "other invisible characters" as out of scope. | **Needs user decision** (scope) |
| MEDIUM | security M2 | nickname.go:58-64 | `assignNickname` scans n linearly inside `Run`: K clients requesting `x` cost O(K²) total; at ~10k each join stalls the hub ~ms | Round 2 (proposed: per-base next-suffix hint or attempt cap) |
| MEDIUM | reviewer | room.go:89-103, client.go:70-105 | **Message loss on disconnect**: readPump queues a chat on buffered `broadcast` then sends `unregister`; `Run`'s select may take unregister first → chat dropped by the non-member check and leave goes out before it. Regression introduced by the non-member drop. Confirmed by orchestrator reading the code. | Round 2 (carry leave through the same FIFO) |
| MEDIUM | reviewer | room_test.go TestContextCancelClosesAll | "Shutdown is silent" (CHAT-02) unverified — `expectClosed` discards frames | Round 2 |
| LOW | security L1 | index.html:333,336 | Notices concatenate the nickname inline (`Bob salió 👋 se unió 🎉`) | Round 2 (cheap: name in its own styled element) |
| LOW | security L2 | client.go:98 | Messages made only of invisible chars pass the blank filter | Round 2 if M1 is fixed (share predicate) |
| LOW | reviewer | index.html:293 | `onmessage` lacks the `ws !== socket` guard that `onclose` has; late frames from an old socket can render / overwrite `myName` | Round 2 |
| LOW | reviewer | client_test.go | `nextChat` forbidden-join param is a no-op in both uses; forged-notice test never connects a real `Bob` (spec GIVEN); misleading slow-client comment; missing `<-slow.registered` wait | Round 2 |
| LOW | reviewer | room_test.go TestSaturationNoDeadlock | listener can be slow-dropped under the flood, then its messages are discarded and fanOut stops being exercised | Round 2 |
| LOW | reviewer | index.html:319, room.go:85, README.md:62-67 | `addRaw` name kept (design: handler); unkeyed `message{}` literals; README tree omits message.go/nickname.go | Round 2 |
| LOW | security L4 | client.go:30 | Peer close reason logged with `%v` → log-line injection (pre-existing) | Round 2 (cheap `%q`) |
| LOW | security L6 | message.go:33 | JSON escaping amplifies `<>&`/controls up to ~6× | Backlog (`rate-limiting`) |
| LOW | security L3 | index.html GIF | Any user makes every viewer fetch an arbitrary https URL (pre-existing) | Backlog (`frontend-hardening`) |
| LOW | security L5 | nickname.go:55 | A client can pre-claim `Bob (2)` | Accepted (same class as homoglyphs); note in design |
| LOW | reviewer | client_test.go keepalive | pacing sleeps (pre-existing) | Accepted |

### Spec compliance (round 1)
No requirement MISSING or DIVERGENT. PARTIAL: CHAT-10 forged-notice test lacks a real `Bob`;
CHAT-12 lowest-free reuse only unit-tested; CHAT-02 shutdown-silent untested; CHAT-06 oversized
tested with a shortened limit. UNTESTED: CHAT-14 (all scenarios).
Concurrency invariants (registry ownership, single close, no send on own channels, nickname
happens-before via `registered`, welcome first, no hang on `<-registered`, `taken` release on
all paths) confirmed by both reviewers. Forgery of notices/senders, "own" marking and XSS: clean.

## Round 2 — 2026-10-22 — Verdict: **PASS**

Scope decided with the user: friends-only chat → fix real UX bugs, defer adversarial hardening.
Deferred to `openspec/backlog.md` § `nickname-polish`: M1, M2, L1, L2, L4, L6 and test polish.
L3 stays in `frontend-hardening`; L5 accepted.

| Round 1 finding | Resolution |
|-----------------|------------|
| HIGH gofmt | Fixed (round 1) |
| MEDIUM message loss on disconnect | Fixed: `leave` drains queued inbound first; regression test deterministic |
| — (new, found in round 2) double close when the drain slow-drops the leaving client | Fixed: membership re-check after drain; `TestLeaveAfterDrainDroppedClient` |
| MEDIUM shutdown-silent untested | `TestShutdownIsSilent` |
| LOW stale-socket `onmessage` | Fixed |
| LOW README tree | Fixed |
| HIGH CHAT-14 untested | **Resolved** — manual two-tab check done by the developer (2026-10-22): `(2)` suffix, "vos" only on own bubbles, join/leave lines and times OK |

Execution: `gofmt -l .` empty, `go vet ./...` clean, `go test -race -count=20 ./...` PASS.
Smoke test of the real binary: still NOT RUN (background server start denied); covered by the
manual browser check.

