# Token Tracker: account-sessions

> Best-effort record of token usage for this SDD run. Missing data renders as
> `[unavailable]` and NEVER blocks the flow. Sub-agent tokens come from each Task
> completion notification's `usage` block; the orchestrator total is parsed post-hoc from
> the local session transcript JSONL (`[unavailable]` on failure).

**Status**: complete
**Generated**: 2026-11-03

## Per-agent / per-phase

| Phase   | Agent             | Model / Tier       | Tokens  | Tool uses | Duration | Tasks covered |
|---------|-------------------|--------------------|---------|-----------|----------|---------------|
| explore | crombie-explore   | [unavailable] / low  | 47,997  | 21        | 56s      | —             |
| explore | crombie-explore   | [unavailable] / low  | 38,286  | 14        | 41s      | —             |
| design  | crombie-architect | [unavailable] / high | 116,425 | 32        | 385s     | —             |
| apply   | crombie-worker (batch 1) | [unavailable] / high   | 95,178 | 29 | 299s | 1.1–1.3, 2.1–2.8, 4.1–4.3 |
| apply   | crombie-worker (batch 1) | [unavailable] / medium | 19,624 | 7  | 23s  | 3.2 |
| apply   | crombie-worker (batch 2) | [unavailable] / medium | 28,873 | 7  | 65s  | 3.1, 4.4, 4.5 |
| verify  | crombie-reviewer         | [unavailable] / high   | 99,446 | 36 | 139s | — |
| verify  | crombie-security         | [unavailable] / medium | 33,251 | 11 | 28s  | — |
| verify  | crombie-worker           | [unavailable] / low    | 14,416 | 3  | 82s  | — |
| verify  | crombie-worker (fixes)   | [unavailable] / medium | 31,888 | 8  | 52s  | F1–F6 |

## Orchestrator

| Scope         | Model / Tier  | Tokens (in / out) | Notes |
|---------------|---------------|-------------------|-------|
| whole feature | [unavailable] | 218 / 112,531     | Parsed from the session transcript within the feature window; cache creation 259,300, cache read 8,354,646 (excluded) |

## Aggregate

| Phase                  | Tokens |
|------------------------|--------|
| explore                | 86,283 |
| design                 | 116,425 |
| apply                  | 143,675 |
| verify                 | 179,001 |
| **Sub-agent total**    | 525,384 |
| **Orchestrator total** | 112,749 (in / out) |
| **Grand total**        | 638,133 (sub-agents + orchestrator in/out; cache excluded) |
