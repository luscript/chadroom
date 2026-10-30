# Token Tracker: user-profiles

> Best-effort record of token usage for this SDD run. Missing data renders as
> `[unavailable]` and NEVER blocks the flow. Sub-agent tokens come from each Task
> completion notification's `usage` block; the orchestrator total is parsed post-hoc from
> the local session transcript JSONL (`[unavailable]` on failure).

**Status**: partial (through verify)
**Generated**: 2026-10-30

## Per-agent / per-phase

| Phase   | Agent                        | Model / Tier          | Tokens        | Tool uses     | Duration      | Tasks covered |
|---------|------------------------------|-----------------------|---------------|---------------|---------------|---------------|
| explore | crombie-explore (backend)    | sonnet / low          | 43,602        | 15            | 40s           | —             |
| explore | crombie-explore (frontend)   | sonnet / low          | 48,328        | 12            | 57s           | —             |
| design  | crombie-architect            | opus / high           | 134,917       | 24            | 421s          | —             |
| apply   | crombie-worker (batch 1)     | sonnet / high         | 123,896       | 28            | 317s          | 1.1–1.4, 2.1–2.3, 3.1 |
| apply   | crombie-worker (batch 1)     | sonnet / high         | 92,700        | 15            | 124s          | 4.1–4.5       |
| verify  | crombie-reviewer             | sonnet / high         | 142,057       | 38            | 162s          | —             |
| verify  | crombie-security             | sonnet / high         | 58,314        | 22            | 90s           | —             |
| verify  | crombie-worker (tests r1)    | sonnet / low          | 13,514        | 2             | 32s           | 5.1           |
| apply   | crombie-worker (batch 2)     | sonnet / medium       | 35,453        | 13            | 91s           | F1–F4         |
| apply   | crombie-worker (batch 2)     | sonnet / medium       | 21,774        | 9             | 44s           | F5–F9         |
| verify  | crombie-worker (tests r2)    | sonnet / low          | 12,500        | 2             | 29s           | 5.1           |

## Orchestrator

| Scope         | Model / Tier | Tokens (in / out) | Notes                 |
|---------------|--------------|-------------------|-----------------------|
| whole feature | opus / high  | [unavailable]     | captured at archive   |

## Aggregate

| Phase                  | Tokens                                               |
|------------------------|------------------------------------------------------|
| explore                | 91,930                                               |
| design                 | 134,917                                              |
| apply                  | 273,823                                              |
| verify                 | 226,385                                              |
| **Sub-agent total**    | 726,435 (partial)                                    |
| **Orchestrator total** | [unavailable]                                        |
| **Grand total**        | 726,435 (sub-agents only; orchestrator [unavailable]) |
