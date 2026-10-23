# Token Tracker: gif-picker

> Best-effort record of token usage for this SDD run. Missing data renders as
> `[unavailable]` and NEVER blocks the flow. Sub-agent tokens come from each Task
> completion notification's `usage` block; the orchestrator total is parsed post-hoc from
> the local session transcript JSONL (`[unavailable]` on failure).

**Status**: complete
**Generated**: 2026-10-23

## Per-agent / per-phase

| Phase    | Agent                    | Model / Tier               | Tokens        | Tool uses     | Duration      | Tasks covered |
|----------|--------------------------|----------------------------|---------------|---------------|---------------|---------------|
| explore  | crombie-explore (backend)  | claude-sonnet-5-5 / low  | 34,204        | 12            | 35s           | —             |
| explore  | crombie-explore (frontend) | claude-sonnet-5-5 / low  | 27,587        | 9             | 36s           | —             |
| design   | crombie-architect          | claude-opus-5-5 / high   | 73,355        | 15            | 199s          | —             |
| apply    | crombie-worker (batch 1)   | claude-sonnet-5-5 / high | 55,057        | 9             | 89s           | 1.1, 1.2, 2.1, 2.5, 3.1–3.3 |
| apply    | crombie-worker (batch 1)   | claude-sonnet-5-5 / medium | 47,714      | 12            | 32s           | 2.2–2.4       |
| verify   | crombie-worker (gates)     | claude-sonnet-5-5 / low  | 12,558        | 2             | 20s           | —             |
| verify   | crombie-security           | claude-sonnet-5-5 / high | 29,443        | 9             | 60s           | —             |
| verify   | crombie-reviewer           | claude-sonnet-5-5 / medium | 59,043      | 13            | 76s           | —             |
| apply    | crombie-worker (batch 2)   | claude-sonnet-5-5 / medium | 36,040      | 10            | 80s           | F1–F8         |

## Orchestrator

| Scope         | Model / Tier           | Tokens (in / out) | Notes |
|---------------|------------------------|-------------------|-------|
| whole feature | claude-opus-5-5 / —    | 168 / 86,648      | Parsed from session transcript within the feature window (68 messages); cache creation 152,085, cache read 5,653,277 |

## Aggregate

| Phase                  | Tokens                                               |
|------------------------|------------------------------------------------------|
| explore                | 61,791                                               |
| design                 | 73,355                                               |
| apply                  | 138,811                                              |
| verify                 | 101,044                                              |
| **Sub-agent total**    | 374,961                                              |
| **Orchestrator total** | 86,816 (in/out)                                      |
| **Grand total**        | 461,777 (sub-agents + orchestrator in/out; cache excluded) |
