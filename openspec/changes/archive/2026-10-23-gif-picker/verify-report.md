# Verify Report: gif-picker

**Date**: 2026-10-23 · **Verdict**: PASS

Executors: crombie-worker (gates), crombie-reviewer (static), crombie-security (static), all in
parallel. A follow-up worker then applied fixes F1–F8 and re-ran the gates.

## Gates

| Gate | Result |
|------|--------|
| `gofmt -l .` | empty |
| `go vet ./...` | clean |
| `go test -race -count=1 ./...` | ok (both packages), before and after fixes |
| `go test -race -count=5 -run 'Gif…'` | ok (no flakiness) |
| `go build ./...` | ok |
| `node --check app.js` | ok |
| grep `innerHTML` / `style=` / `on*=` in page | no matches |

## Spec coverage (SRV-08)

All server scenarios have tests: trending, search, no key, too long, injection, upstream
failure/timeout, filter/canonicalize, cache, plus cap, bound, concurrency and redirects. The
page scenarios ("Pick a GIF", "Page without a key") and SRV-05 "Page works under the policy"
rely on the **manual checklist** in `design.md` §Testing. That checklist is **pending a browser
run by the user**.

## Findings

No CRITICAL or HIGH findings. **Fixed** in batch 2:

| ID | Sev | Source | Finding | Fix |
|----|-----|--------|---------|-----|
| F1 | LOW | security | Client followed redirects, so a `Referer` with `api_key` could reach another host | `CheckRedirect` → `ErrUseLastResponse`, plus test |
| F2 | LOW | security | `canonicalGIF` accepted Tenor/Imgur from the Giphy response | Giphy-only hosts, plus table rows |
| F3 | LOW | security | Empty result list cached 10 min | not stored, plus test |
| F4 | LOW | review | `fmt.Sprint(int)` | `strconv.Itoa` |
| F5 | MED | review | `TestGifsKeyNeverLeaks` could pass vacuously | per-case status + non-empty log assert |
| F6 | MED | review | JSON headers asserted only on 200 | `requireJSONHeaders` on 200/400/502 |
| F7 | MED | review | Reopening the panel during the first load fired a duplicate request | `gifPending` guard |
| F8 | LOW | review | Enter during IME composition triggered search | `e.isComposing` early return |

**Deferred** to `backlog.md`:

| Sev | Source | Finding |
|-----|--------|---------|
| MED | security | Quota exhaustion: unique queries are unauthenticated, with no singleflight or upstream concurrency cap → group `rate-limiting` |
| LOW | security | gin access log records `/gifs?q=` search terms |
| LOW | review | Non-200 upstream body not drained before close (connection reuse) |
| LOW | review | GIF click when `sendText` fails gives no feedback (same as the paste box) |
| LOW | review | CLAUDE.md structure and conventions do not mention `gifs.go` / `GIPHY_API_KEY` → at archive |
