# Token Tracker: online-users-and-history

> Best-effort record of token usage for this SDD run. Missing data renders as
> `[unavailable]` and NEVER blocks the flow. Sub-agent tokens come from each Task
> completion notification's `usage` block; the orchestrator total is parsed post-hoc from
> the local session transcript JSONL (`[unavailable]` on failure).

**Status**: complete
**Generated**: 2026-10-22

## Per-agent / per-phase

| Phase   | Agent             | Model / Tier             | Tokens | Tool uses | Duration | Tasks covered |
|---------|-------------------|--------------------------|--------|-----------|----------|---------------|
| explore | crombie-explore   | claude-sonnet-5-5 / low  | 29,679 | 11        | 30s      | —             |
| explore | crombie-explore   | claude-sonnet-5-5 / low  | 44,716 | 9         | 39s      | —             |
| design  | crombie-architect | claude-opus-5-5 / high   | 84,528 | 19        | 244s     | —             |
| apply   | crombie-worker (batch 1) | claude-sonnet-5-5 / high | 74,356 | 20 | 238s | 1.1, 2.1–2.5, 3.1–3.5 |
| apply   | crombie-worker (batch 1) | claude-sonnet-5-5 / medium | 41,750 | 8 | 37s | 4.1–4.4, 5.1–5.3 |
| verify  | crombie-reviewer  | claude-sonnet-5-5 / high | 70,938 | 13 | 111s | — |
| verify  | crombie-security  | claude-sonnet-5-5 / medium | 30,186 | 7 | 44s | — |
| verify  | crombie-worker (tests) | claude-sonnet-5-5 / low | 12,841 | 5 | 47s | — |
| verify  | crombie-worker (fix round 1) | claude-sonnet-5-5 / low | 21,066 | 5 | 33s | V1–V4 |

## Orchestrator

| Scope         | Model / Tier    | Tokens (in / out) | Notes                    |
|---------------|-----------------|-------------------|--------------------------|
| whole feature | claude-opus-5-5 | 156 / 96,135      | Parsed from session transcript JSONL within the feature window (64 messages); cache: 186,656 created / 6,431,649 read |

## Aggregate

| Phase                  | Tokens                                                |
|------------------------|-------------------------------------------------------|
| explore                | 74,395                                                |
| design                 | 84,528                                                |
| apply                  | 116,106                                               |
| verify                 | 135,031                                               |
| **Sub-agent total**    | 410,060                                               |
| **Orchestrator total** | 96,291 (in/out)                                       |
| **Grand total**        | 506,351 (sub-agents + orchestrator in/out; cache excluded) |
