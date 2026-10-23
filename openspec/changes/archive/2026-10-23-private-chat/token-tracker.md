# Token Tracker: private-chat

> Best-effort record of token usage for this SDD run. Missing data renders as
> `[unavailable]` and NEVER blocks the flow. Sub-agent tokens come from each Task
> completion notification's `usage` block; the orchestrator total is parsed post-hoc from
> the local session transcript JSONL (`[unavailable]` on failure).

**Status**: complete
**Generated**: 2026-10-22

## Per-agent / per-phase

| Phase   | Agent                      | Model / Tier            | Tokens | Tool uses | Duration | Tasks covered |
|---------|----------------------------|-------------------------|--------|-----------|----------|---------------|
| explore | crombie-explore (backend)  | claude-sonnet-4-6 / low | 53,909 | 12        | 48s      | —             |
| explore | crombie-explore (frontend) | claude-sonnet-4-6 / low | 29,549 | 8         | 28s      | —             |
| design  | crombie-architect          | claude-opus-5-5 / high  | 144,853 | 23       | 375s     | —             |
| apply   | crombie-worker (batch 1)   | claude-sonnet-5-5 / high | 87,235 | 23       | 177s     | 1.1–1.3, 2.1, 2.2, 4.1–4.5 |
| apply   | crombie-worker (batch 1)   | claude-sonnet-5-5 / high | 59,808 | 23       | 111s     | 3.1–3.4       |
| verify  | crombie-reviewer           | claude-sonnet-5-5 / high | 96,489 | 17       | 124s     | —             |
| verify  | crombie-security           | claude-sonnet-5-5 / high | 42,712 | 7        | 80s      | —             |
| verify  | crombie-worker (gates)     | claude-sonnet-5-5 / low  | 14,551 | 3        | 17s      | —             |
| verify  | crombie-worker (fixes)     | claude-sonnet-5-5 / medium | 29,914 | 12     | 52s      | V1–V6         |

## Orchestrator

| Scope         | Model / Tier  | Tokens (in / out) | Notes                         |
|---------------|---------------|-------------------|-------------------------------|
| whole feature | claude-opus-5-5 / orchestrator | 168 / 102,844 | Spec→archive session only (explore/propose ran in an earlier session: [unavailable]). Cache: 266,615 created / 5,449,185 read |

## Aggregate

| Phase                  | Tokens                                               |
|------------------------|------------------------------------------------------|
| explore                | 83,458                                               |
| propose                | inline (orchestrator)                                |
| spec                   | inline (orchestrator)                                |
| design                 | 144,853                                              |
| tasks                  | inline (orchestrator)                                |
| apply                  | 147,043                                              |
| verify                 | 183,666                                              |
| **Sub-agent total**    | 559,020                                              |
| **Orchestrator total** | 168 / 102,844 (partial; this session only)           |
| **Grand total**        | 662,032 (sub-agents + orchestrator in/out; cache excluded; partial) |
