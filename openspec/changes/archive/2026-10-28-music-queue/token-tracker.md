# Token Tracker: music-queue

> Best-effort record of token usage for this SDD run. Missing data renders as
> `[unavailable]` and NEVER blocks the flow. Sub-agent tokens come from each Task
> completion notification's `usage` block; the orchestrator total is parsed post-hoc from
> the local session transcript JSONL (`[unavailable]` on failure).

**Status**: complete
**Generated**: 2026-10-23

## Per-agent / per-phase

| Phase   | Agent                      | Model / Tier             | Tokens  | Tool uses | Duration | Tasks covered |
|---------|----------------------------|--------------------------|---------|-----------|----------|---------------|
| explore | crombie-explore (server)   | claude-sonnet-4-6 / low  | 62,116  | 14        | 50s      | —             |
| explore | crombie-explore (frontend) | claude-sonnet-4-6 / low  | 54,038  | 10        | 48s      | —             |
| design  | crombie-architect          | claude-opus-4-8 / high   | 128,689 | 22        | 415s     | —             |
| apply   | crombie-worker (batch 1)   | claude-sonnet-4-6 / high | 31,534  | 9         | 38s      | T101–T105, T201–T204 |
| apply   | crombie-worker (batch 1)   | claude-sonnet-4-6 / high | 74,657  | 16        | 111s     | T401–T406, T501 |
| apply   | crombie-worker (batch 1)   | claude-sonnet-4-6 / low  | 19,463  | 4         | 25s      | T701, T702    |
| apply   | crombie-worker (batch 2)   | claude-sonnet-4-6 / medium | 60,640 | 22        | 162s     | T301–T303     |
| apply   | crombie-worker (batch 2)   | claude-sonnet-4-6 / medium | 19,517 | 5         | 22s      | T601          |
| verify  | crombie-reviewer           | claude-sonnet-4-6 / medium | 107,262 | 27        | 96s      | —             |
| verify  | crombie-security           | claude-sonnet-4-6 / high   | 47,105  | 14        | 54s      | —             |
| verify  | crombie-worker (gates)     | claude-sonnet-4-6 / low    | 12,193  | 2         | 15s      | T801          |
| apply   | crombie-worker (fix batch) | claude-sonnet-4-6 / high   | 22,969  | 10        | 42s      | T901–T904     |
| apply   | crombie-worker (fix T905)  | claude-sonnet-4-6 / high   | 43,240  | 7         | 116s     | T905          |

## Orchestrator

| Scope         | Model / Tier | Tokens (in / out) | Notes                  |
|---------------|--------------|-------------------|------------------------|
| whole feature | opus         | [unavailable]     | captured at sdd-archive |

## Aggregate

| Phase                  | Tokens                                                |
|------------------------|-------------------------------------------------------|
| explore                | 116,154                                               |
| design                 | 128,689                                               |
| apply (batch 1)        | 125,654                                               |
| apply (batch 2)        | 80,157                                                |
| verify                 | 166,560                                               |
| apply (fix batch)      | 22,969                                                |
| apply (fix T905)       | 43,240                                                |
| **Sub-agent total**    | 683,423                                               |
| **Orchestrator total** | [unavailable]                                         |
| **Grand total**        | 683,423 (sub-agents only; orchestrator [unavailable]) |
