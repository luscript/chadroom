# Token Tracker: google-oidc-login

> Best-effort record of token usage for this SDD run. Missing data renders as
> `[unavailable]` and NEVER blocks the flow. Sub-agent tokens come from each Task
> completion notification's `usage` block; the orchestrator total is parsed post-hoc from
> the local session transcript JSONL (`[unavailable]` on failure).

**Status**: partial (through verify)
**Generated**: 2026-11-03

## Per-agent / per-phase

| Phase   | Agent                          | Model / Tier     | Tokens        | Tool uses     | Duration      | Tasks covered |
|---------|--------------------------------|------------------|---------------|---------------|---------------|---------------|
| explore | crombie-explore (backend)      | sonnet / low     | 38,126        | 13            | 41s           | —             |
| explore | crombie-explore (frontend)     | sonnet / low     | 56,366        | 27            | 63s           | —             |
| design  | crombie-architect              | opus / xhigh     | 103,670       | 32            | 462s          | —             |
| apply   | crombie-worker (batch 1)       | sonnet / high    | 71,358        | 16            | 214s          | 1.1, 2.1–2.4  |
| apply   | crombie-worker (batch 1)       | sonnet / low     | 21,831        | 8             | 44s           | 3.1           |
| apply   | crombie-worker (batch 1)       | sonnet / medium  | 35,073        | 8             | 43s           | 4.2           |
| apply   | crombie-worker (batch 2)       | sonnet / high    | 56,947        | 10            | 161s          | 2.5           |
| apply   | crombie-worker (batch 2)       | sonnet / low     | 36,338        | 4             | 34s           | 5.1–5.3       |
| apply   | crombie-worker (batch 3)       | sonnet / high    | 73,632        | 13            | 113s          | 4.1           |
| verify  | crombie-security               | sonnet / high    | 49,453        | 15            | 94s           | —             |
| verify  | crombie-reviewer               | sonnet / medium  | 161,086       | 47            | 276s          | —             |
| verify  | crombie-worker (tests)         | sonnet / low     | 16,663        | 7             | 45s           | —             |
| verify  | crombie-worker (fixes, auth)   | sonnet / high    | 37,206        | 8             | 60s           | F1–F6         |
| verify  | crombie-worker (fixes, server) | sonnet / high    | 45,476        | 14            | 104s          | G1–G8         |
| verify  | crombie-worker (fixes, docs)   | sonnet / low     | 30,295        | 5             | 24s           | D1–D7         |
| verify  | crombie-worker (re-run)        | sonnet / low     | 13,494        | 2             | 49s           | —             |

## Orchestrator

| Scope         | Model / Tier | Tokens (in / out) | Notes               |
|---------------|--------------|-------------------|---------------------|
| whole feature | opus         | [unavailable]     | captured at archive |

## Aggregate

| Phase                  | Tokens                                                |
|------------------------|-------------------------------------------------------|
| explore                | 94,492                                                |
| design                 | 103,670                                               |
| apply                  | 295,179                                               |
| verify                 | 353,673                                               |
| **Sub-agent total**    | 847,014                                               |
| **Orchestrator total** | [unavailable]                                         |
| **Grand total**        | 847,014 (sub-agents only; orchestrator [unavailable]) |
