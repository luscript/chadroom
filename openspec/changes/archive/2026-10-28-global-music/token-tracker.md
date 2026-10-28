# Token Tracker: global-music

> Best-effort record of token usage for this SDD run. Missing data renders as
> `[unavailable]` and NEVER blocks the flow. Sub-agent tokens come from each Task
> completion notification's `usage` block; the orchestrator total is parsed post-hoc from
> the local session transcript JSONL (`[unavailable]` on failure).

**Status**: complete
**Generated**: 2026-10-28

## Per-agent / per-phase

| Phase    | Agent                    | Model / Tier               | Tokens        | Tool uses     | Duration      | Tasks covered |
|----------|--------------------------|----------------------------|---------------|---------------|---------------|---------------|
| explore  | crombie-explore          | sonnet / low               | 50,024        | 16            | 55s           | —             |
| design   | crombie-architect        | opus / medium              | 73,155        | 12            | 143s          | —             |
| apply    | crombie-worker (batch 1) | sonnet / high              | 49,385        | 10            | 51s           | 1.1–2.2       |
| verify   | crombie-worker           | sonnet / low               | 12,374        | 2             | 13s           | —             |
| verify   | crombie-security         | sonnet / medium            | 30,812        | 12            | 30s           | —             |
| verify   | crombie-reviewer         | sonnet / medium            | 58,984        | 24            | 94s           | —             |
| apply    | crombie-worker (batch 2) | sonnet / medium            | 21,084        | 5             | 29s           | F1–F8         |

## Orchestrator

| Scope         | Model / Tier  | Tokens (in / out) | Notes |
|---------------|---------------|-------------------|-------|
| whole feature | opus / —      | [unavailable]     | captured at archive |

## Aggregate

| Phase                  | Tokens |
|------------------------|--------|
| explore                | 50,024 |
| design                 | 73,155 |
| apply                  | 70,469 |
| verify                 | 102,170 |
| **Sub-agent total**    | 295,818 |
| **Orchestrator total** | [unavailable] |
| **Grand total**        | 295,818 (sub-agents only; orchestrator [unavailable]) |
