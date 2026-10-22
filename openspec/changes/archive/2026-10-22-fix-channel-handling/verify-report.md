# Verify Report: fix-channel-handling

## Round 1 — 2026-10-22 — Verdict: **FAIL**

Executors (parallel): crombie-reviewer (static), crombie-security (static), crombie-worker (execution).

### Execution (crombie-worker)
| Check | Result |
|-------|--------|
| go version | go1.27.3 linux/amd64 (note: `/snap/bin/go` wrapper swallows some output; real binary `/snap/go/current/bin/go`) |
| go build ./... | rc=0 |
| go vet ./... | rc=0 |
| gofmt -l . | empty |
| go test -race -v ./... | PASS — 21 tests (+ subtests), no data races |
| go test -race -count=20 ./... | PASS — no flakes |
| coverage | internal/chat 96.2%, cmd/chatroom 38.5% |
| Smoke test of real binary (SRV-01..04) | **NOT RUN** — command denied by the permission system; requires user action |

### Findings
| Sev | Source | Location | Finding | Disposition |
|-----|--------|----------|---------|-------------|
| HIGH | security + reviewer | client.go readPump, room.go HandleWebSocket | Binary frames / invalid UTF-8 (message or `?nickname=%ff`) rebroadcast as text frames → every browser fails the connection (1007). Pre-existing, on the changed path. | Fix in round 2 (new CHAT-09) |
| HIGH | reviewer | cmd/chatroom/main.go | SRV-03 (graceful shutdown) and SRV-04 (startup error exit≠0) untested; `run()` not drivable. | Fix in round 2 |
| MEDIUM | security | room.go HandleWebSocket | Unbounded nickname (up to ~1 MB via query) defeats the 4 KiB read limit (fan-out amplification). | Fix in round 2 (length cap only; validation stays in backlog #10) |
| MEDIUM | security | main.go http.Server | No `IdleTimeout`, no `MaxHeaderBytes`. | Fix in round 2 |
| MEDIUM | reviewer | client.go readPump | CHAT-07 divergence: deadline only extended by pong, spec says "pong or any frame". | Fix in round 2 |
| MEDIUM | reviewer | client.go readPump | Read-limit and timeout disconnects never logged. | Fix in round 2 |
| MEDIUM | reviewer | client_test.go | CHAT-05 "slow client connection is closed" untested at conn level. | Fix in round 2 |
| MEDIUM | reviewer | room_test.go | Slow-drop tests trigger the drop on register, not broadcast; saturation test does not guarantee a full queue. | Fix in round 2 |
| MEDIUM | reviewer | client_test.go | Ping-period tests have thin timing margins; goroutine-leak baseline is global/noisy. | Fix in round 2 |
| LOW | security | main.go newRouter | Trusted proxies default to all; no `nosniff` / `X-Frame-Options` on `GET /`. | Fix in round 2 (cheap) |
| LOW | reviewer | client.go, main.go, tests | Close frame without status code, missing `Client` doc, ignored best-effort errors not marked, "listening" logged before bind, `Anonymous` default untested, table test style. | Fix in round 2 |
| LOW | security | room.go checkOrigin | DNS rebinding passes same-origin (no auth/cookies → low impact). | Backlog (documented residual risk) |
| LOW | security | main.go | gin debug mode unless `GIN_MODE=release`. | Backlog / deploy config |
| LOW | security | index.html | CSP requires inline-script refactor. | Backlog (#13 frontend-hardening) |
| LOW | security | room.go | Join/leave notices spoofable as plain text. | Backlog (#10) |

### Spec compliance (round 1)
All requirements implemented. Untested: CHAT-04 "Anonymous" default, CHAT-05 conn-level close, CHAT-07 write deadline, SRV-03, SRV-04. Divergent: CHAT-07 "any frame".
Concurrency invariants I1–I7 confirmed to hold in code by the reviewer; all 6 disconnect paths traced OK.

---

## Round 2 — 2026-10-22 — Verdict: **PASS**

Executors (parallel): crombie-reviewer (static re-review), crombie-security (static re-audit), crombie-worker (execution, real binary `/snap/go/current/bin/go`).

### Execution
| Check | Result |
|-------|--------|
| go build ./... | rc=0 |
| go vet ./... | rc=0 |
| gofmt -l . | empty |
| go test -race -count=1 -v ./... | PASS — cmd/chatroom 6 tests, internal/chat all tests; no data races |
| go test -race -count=20 ./... | PASS — no flakes |
| coverage | cmd/chatroom 80.4% (was 38.5%), internal/chat 94.7% |
| Smoke test of real binary | **NOT RUN** — denied by permission system in round 1; left to the user (commands below). SRV-03/04 are covered by in-process tests (`TestRunGracefulShutdown`, `TestServeListenError`). |

### Round-1 findings
All "Fix in round 2" items **Resolved** (reviewer + security confirmed with file:line), except SRV-04 "Serve error after start" branch → partially tested (see N2).

### Concurrency invariants
I1–I7 re-checked after changes: hold. `closeWith` uses `WriteControl` (concurrency-safe with writePump); `run()` shutdown order (cancel → wait room → Shutdown) correct; test-only `/slow` handler does not close `send`.

### Spec compliance
| Req | Impl | Tested |
|-----|------|--------|
| CHAT-01..06, CHAT-08, CHAT-09 | ✅ | ✅ |
| CHAT-07 | ✅ | Partial — write deadline not asserted (gorilla enforces it) |
| SRV-01, SRV-02, SRV-03 | ✅ | ✅ (SRV-03 via ctx cancel, not a real signal) |
| SRV-04 | ✅ | Partial — bind failure tested; Serve-error branch untested |
| SRV-05 | ✅ | Partial — headers tested; timeouts / trusted proxies set but not asserted |

### Remaining findings (non-blocking)
| Sev | Id | Location | Finding | Disposition |
|-----|----|----------|---------|-------------|
| MEDIUM | N1 | internal/chat/client_test.go (TestDroppedSlowClientConnectionIsClosed) | Comment says B's broadcast triggers the drop; actually B's join notice does. Test is deterministic. | Follow-up |
| MEDIUM | N2 | cmd/chatroom/main.go run() Serve-error branch | Untested (close listener → expect 1). | Follow-up |
| LOW | N3 | main.go | `serve` param `addr` shadows package const `addr`. | Follow-up |
| LOW | N4 | main.go | SRV-05 server fields not asserted (extract `newServer`). | Follow-up |
| LOW | N5 | client.go logError | Close 1006 (tab closed abruptly) logged on every disconnect. | Follow-up |
| LOW | N6 | client.go closeWith paths | Rejections (1003/1007) not logged. | Follow-up |
| LOW | N7 | — | CHAT-07 write deadline untested. | Follow-up |
| LOW | S1–S7 | security round 2 | Nickname interior control/bidi chars, empty messages, rate limit/conn cap, ErrReadLimit log volume, gin release mode + CSP, missing-Origin + DNS rebinding, no Read/WriteTimeout (moot for hijacked). | Backlog (openspec/backlog.md) |

### Manual smoke test (for the user)
```bash
go build -o /tmp/chatroom ./cmd/chatroom
/tmp/chatroom & PID=$!
sleep 1
curl -s -o /dev/null -w '%{http_code} %{content_type}\n' http://localhost:8080/            # 200 text/html
curl -s -o /dev/null -w '%{http_code}\n' -H 'Connection: Upgrade' -H 'Upgrade: websocket' \
  -H 'Sec-WebSocket-Version: 13' -H 'Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==' \
  -H 'Origin: https://evil.example' http://localhost:8080/ws                                # 403
/tmp/chatroom; echo "second instance rc=$?"                                                 # rc=1 (port in use)
kill -TERM $PID; wait $PID; echo "rc=$?"                                                    # rc=0
```
