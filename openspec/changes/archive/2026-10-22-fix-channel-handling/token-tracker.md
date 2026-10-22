# Token Tracker: fix-channel-handling

> Best-effort record of token usage for this SDD run. Missing data renders as
> `[unavailable]` and NEVER blocks the flow. Sub-agent tokens come from each Task
> completion notification's `usage` block; the orchestrator total is parsed post-hoc from
> the local session transcript JSONL (`[unavailable]` on failure).

**Status**: complete
**Generated**: 2026-10-22

## Per-agent / per-phase

| Phase   | Agent             | Model / Tier  | Tokens | Tool uses | Duration | Tasks covered |
|---------|-------------------|---------------|--------|-----------|----------|---------------|
| design  | crombie-architect | opus / high   | 50,894 | 12        | 215s     | —             |
| apply   | crombie-worker (batch 1) | sonnet / high | 20,307 | 5 | 96s | 1.1–1.6, 3.1 |
| apply   | crombie-worker (batch 2) | sonnet / high | 23,356 | 6 | 81s | 3.2 |
| apply   | crombie-worker (batch 2) | sonnet / medium | 15,858 | 6 | 33s | 2.1–2.4, 3.3 |
| verify  | crombie-reviewer (round 1) | sonnet / high | 52,734 | 12 | 129s | — |
| verify  | crombie-security (round 1) | sonnet / high | 24,915 | 6 | 77s | — |
| verify  | crombie-worker (round 1, test/build) | sonnet / low | 17,364 | 9 | 55s | — |
| apply   | crombie-worker (batch 3) | sonnet / high | 43,974 | 11 | 192s | 5.1–5.4 |
| apply   | crombie-worker (batch 3) | sonnet / medium | 17,928 | 5 | 29s | 5.5–5.7 |
| verify  | crombie-reviewer (round 2) | sonnet / medium | 54,185 | 11 | 72s | — |
| verify  | crombie-security (round 2) | sonnet / medium | 19,278 | 4 | 37s | — |
| verify  | crombie-worker (round 2, test/build) | sonnet / low | 14,508 | 4 | 81s | — |

> init, explore, propose and spec ran inline in the orchestrator (no sub-agent rows).

## Orchestrator

| Scope         | Model / Tier | Tokens (in / out) | Notes                         |
|---------------|--------------|-------------------|-------------------------------|
| whole feature | opus         | 262 / 160,367     | Cache: 572,003 created, 11,765,163 read. Parsed post-hoc from the session transcript; input includes injected sub-agent results |

## Aggregate

| Phase                  | Tokens                                               |
|------------------------|------------------------------------------------------|
| design                 | 50,894                                               |
| apply                  | 121,423                                              |
| verify                 | 182,984                                              |
| **Sub-agent total**    | 355,301                                              |
| **Orchestrator total** | 160,629 (in/out; cache excluded)                     |
| **Grand total**        | 515,930 (sub-agents + orchestrator in/out; cache excluded) |
