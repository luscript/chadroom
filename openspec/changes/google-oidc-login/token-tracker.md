# Token Tracker: google-oidc-login

> Best-effort record of token usage for this SDD run. Missing data renders as
> `[unavailable]` and NEVER blocks the flow. Sub-agent tokens come from each Task
> completion notification's `usage` block; the orchestrator total is parsed post-hoc from
> the local session transcript JSONL (`[unavailable]` on failure).

**Status**: partial (through explore)
**Generated**: 2026-11-03

## Per-agent / per-phase

| Phase    | Agent                    | Model / Tier               | Tokens        | Tool uses     | Duration      | Tasks covered |
|----------|--------------------------|----------------------------|---------------|---------------|---------------|---------------|
| explore  | crombie-explore (backend)  | sonnet / low             | 38,126        | 13            | 41s           | —             |
| explore  | crombie-explore (frontend) | sonnet / low             | 56,366        | 27            | 63s           | —             |

## Orchestrator

| Scope         | Model / Tier  | Tokens (in / out) | Notes |
|---------------|---------------|-------------------|-------|
| whole feature | opus          | [unavailable]     | captured at archive |

## Aggregate

| Phase                  | Tokens            |
|------------------------|-------------------|
| explore                | 94,492            |
| **Sub-agent total**    | 94,492            |
| **Orchestrator total** | [unavailable]     |
| **Grand total**        | 94,492 (sub-agents only; orchestrator [unavailable]) |
