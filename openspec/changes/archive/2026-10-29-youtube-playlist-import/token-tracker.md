# Token Tracker: youtube-playlist-import

> Best-effort record of token usage for this SDD run. Missing data renders as
> `[unavailable]` and NEVER blocks the flow. Sub-agent tokens come from each Task
> completion notification's `usage` block; the orchestrator total is parsed post-hoc from
> the local session transcript JSONL (`[unavailable]` on failure).

**Status**: complete
**Generated**: 2026-10-29

## Per-agent / per-phase

| Phase   | Agent                      | Model / Tier            | Tokens        | Tool uses     | Duration      | Tasks covered |
|---------|----------------------------|-------------------------|---------------|---------------|---------------|---------------|
| explore | crombie-explore (frontend) | claude-sonnet-4-6 / low | 29,596        | 11            | 38s           | —             |
| explore | crombie-explore (backend)  | claude-sonnet-4-6 / low | 46,448        | 16            | 47s           | —             |
| design  | crombie-architect          | [unavailable] / high    | 118,900       | 42            | 380s          | —             |
| apply   | crombie-worker (batch 1)   | claude-sonnet-4-6 / high   | 47,396        | 11            | 75s           | 1.1–1.4       |
| apply   | crombie-worker (batch 1)   | claude-sonnet-4-6 / high   | 59,832        | 12            | 107s          | 2.1–2.4       |
| apply   | crombie-worker (batch 1)   | claude-sonnet-4-6 / medium | 39,463        | 9             | 55s           | 3.1–3.3, 4.1–4.3 |
| verify  | crombie-worker (gates)     | claude-sonnet-4-6 / low    | 13,540        | 2             | 20s           | —             |
| verify  | crombie-security           | claude-sonnet-4-6 / high   | 39,649        | 11            | 56s           | —             |
| verify  | crombie-reviewer           | claude-sonnet-4-6 / high   | 70,493        | 21            | 80s           | —             |

## Orchestrator

| Scope         | Model / Tier           | Tokens (in / out) | Notes |
|---------------|------------------------|-------------------|-------|
| whole feature | claude-opus-5-5 / —    | 134 / 57,906      | Parsed from the session transcript JSONL (main thread, 56 messages). Window = whole session, so it also includes the initial bug diagnosis before the first artifact write (the artifact mtime was reset by a checkout). Cache: 177,491 created, 6,099,134 read |

## Aggregate

| Phase                  | Tokens                                                 |
|------------------------|--------------------------------------------------------|
| explore                | 76,044                                                 |
| design                 | 118,900                                                |
| apply                  | 146,691                                                |
| verify                 | 123,682                                                |
| **Sub-agent total**    | 465,317                                      |
| **Orchestrator total** | 58,040 (in/out; cache excluded)                        |
| **Grand total**        | 523,357 (sub-agents + orchestrator in/out; cache excluded) |
