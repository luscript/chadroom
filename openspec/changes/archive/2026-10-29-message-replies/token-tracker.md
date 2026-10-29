# Token Tracker: message-replies

> Best-effort record of token usage for this SDD run. Missing data renders as
> `[unavailable]` and NEVER blocks the flow. Sub-agent tokens come from each Task
> completion notification's `usage` block; the orchestrator total is parsed post-hoc from
> the local session transcript JSONL (`[unavailable]` on failure).

**Status**: complete
**Generated**: 2026-10-29

## Per-agent / per-phase

| Phase   | Agent                      | Model / Tier               | Tokens  | Tool uses | Duration | Tasks covered |
|---------|----------------------------|----------------------------|---------|-----------|----------|---------------|
| explore | crombie-explore (backend)  | claude-sonnet-5-5 / low    | 45,951  | 16        | 41s      | —             |
| explore | crombie-explore (frontend) | claude-sonnet-5-5 / low    | 43,246  | 15        | 50s      | —             |
| design  | crombie-architect          | claude-opus-5-5 / high     | 115,342 | 28        | 341s     | —             |
| apply   | crombie-worker (batch 1)   | claude-sonnet-5-5 / high   | 71,899  | 19        | 155s     | 1.1, 1.2, 2.1–2.3, 4.1 |
| apply   | crombie-worker (batch 1)   | claude-sonnet-5-5 / high   | 50,694  | 11        | 61s      | 3.1–3.4       |
| verify  | crombie-worker (gates)     | claude-sonnet-5-5 / low    | 13,794  | 2         | 19s      | 5.1           |
| verify  | crombie-reviewer           | claude-sonnet-5-5 / medium | 87,003  | 20        | 93s      | —             |
| verify  | crombie-security           | claude-sonnet-5-5 / high   | 51,398  | 11        | 55s      | —             |
| apply   | crombie-worker (batch 2)   | claude-sonnet-5-5 / medium | 25,416  | 9         | 57s      | F1–F6         |

## Orchestrator

| Scope         | Model / Tier           | Tokens (in / out) | Notes |
|---------------|------------------------|-------------------|-------|
| whole session | claude-opus-5-5 / —    | 224 / 107,179     | cache creation 262,655, cache read 7,612,128; session-wide (also covers archiving youtube-playlist-import), not filtered to the feature window |

## Aggregate

| Phase                  | Tokens |
|------------------------|--------|
| explore                | 89,197 |
| design                 | 115,342 |
| apply                  | 148,009 |
| verify                 | 152,195 |
| **Sub-agent total**    | 504,743 |
| **Orchestrator total** | 224 / 107,179 (in/out) |
| **Grand total**        | 611,146 (sub-agents + orchestrator in/out; cache excluded) |
