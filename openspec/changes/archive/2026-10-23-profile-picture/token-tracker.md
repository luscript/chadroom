# Token Tracker: profile-picture

> Best-effort record of token usage for this SDD run. Missing data renders as
> `[unavailable]` and NEVER blocks the flow. Sub-agent tokens come from each Task
> completion notification's `usage` block; the orchestrator total is parsed post-hoc from
> the local session transcript JSONL (`[unavailable]` on failure).

**Status**: complete
**Generated**: 2026-10-23

## Per-agent / per-phase

| Phase   | Agent                             | Model / Tier        | Tokens  | Tool uses | Duration | Tasks covered |
|---------|-----------------------------------|---------------------|---------|-----------|----------|---------------|
| explore | crombie-explore (backend, shared with music-player) | sonnet / low | 44,533 | 13 | 41s | — |
| explore | crombie-explore (frontend, shared with music-player) | sonnet / low | 44,087 | 9 | 44s | — |
| design  | crombie-architect                 | opus / high         | 102,500 | 23        | 250s     | —             |
| apply   | crombie-worker (batch 1)          | sonnet / high       | 83,573  | 22        | 143s     | 1.1–1.6, 3.1  |
| apply   | crombie-worker (batch 1)          | sonnet / high       | 50,270  | 10        | 67s      | 2.1–2.3       |
| verify  | crombie-reviewer                  | sonnet / medium     | 71,385  | 25        | 76s      | —             |
| verify  | crombie-security                  | sonnet / high       | 48,471  | 17        | 99s      | —             |
| verify  | crombie-worker (gates)            | sonnet / low        | 12,640  | 2         | 22s      | 4.1           |
| verify  | crombie-worker (fix round 1)      | sonnet / medium     | 23,133  | 5         | 33s      | R1–R6         |

Project-map refresh (crombie-explore, 55,086 tokens) ran before both changes and is not counted here.

## Orchestrator

| Scope | Model / Tier | Tokens (in / out) | Notes |
|-------|--------------|-------------------|-------|
| whole feature | opus / high | 160 / 105,712 | Window 14:49–15:02 UTC, shared with music-player planning (proposal/spec/tasks written together). Cache: 184,729 creation, 7,591,426 read |

## Aggregate

| Phase                  | Tokens |
|------------------------|--------|
| explore                | 88,620 (shared with music-player) |
| design                 | 102,500 |
| apply                  | 133,843 |
| verify                 | 155,629 |
| **Sub-agent total**    | 480,592 |
| **Orchestrator total** | 160 / 105,712 |
| **Grand total**        | 586,464 (sub-agents + orchestrator in/out; cache excluded) |
