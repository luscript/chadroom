# Token Tracker: multiple-rooms

> Best-effort record of token usage for this SDD run. Missing data renders as
> `[unavailable]` and NEVER blocks the flow. Sub-agent tokens come from each Task
> completion notification's `usage` block; the orchestrator total is parsed post-hoc from
> the local session transcript JSONL (`[unavailable]` on failure).

**Status**: partial (through apply)
**Generated**: 2026-10-22

## Per-agent / per-phase

| Phase    | Agent                       | Model / Tier            | Tokens | Tool uses | Duration | Tasks covered |
|----------|-----------------------------|-------------------------|--------|-----------|----------|---------------|
| explore  | crombie-explore (backend)   | claude-sonnet-5-5 / low | 36,368 | 13        | 47s      | —             |
| explore  | crombie-explore (frontend)  | claude-sonnet-5-5 / low | 29,945 | 9         | 31s      | —             |
| design   | crombie-architect           | claude-opus-5-5 / high  | 98,754 | 20        | 306s     | —             |
| apply    | crombie-worker (batch 1)    | claude-sonnet-5-5 / high | 52,587 | 10       | 67s      | 3.1–3.6       |
| apply    | crombie-worker (batch 1)    | claude-sonnet-5-5 / high | 60,217 | 17       | 120s     | 1.1–1.3, 2.1, 2.2, 4.1–4.4 |

## Orchestrator

| Scope         | Model / Tier          | Tokens (in / out) | Notes |
|---------------|-----------------------|-------------------|-------|
| whole feature | claude-opus-5-5 / —   | [unavailable]     | Captured at archive |

## Aggregate

| Phase                  | Tokens |
|------------------------|--------|
| explore                | 66,313 |
| design                 | 98,754 |
| apply                  | 112,804 |
| **Sub-agent total**    | 277,871 |
| **Orchestrator total** | [unavailable] |
| **Grand total**        | 277,871 (sub-agents only; orchestrator [unavailable]) |
