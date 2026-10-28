# Token Tracker: chat-notifications

> Best-effort record of token usage for this SDD run. Missing data renders as
> `[unavailable]` and NEVER blocks the flow.

**Status**: partial (through verify)
**Generated**: 2026-10-28

## Per-agent / per-phase

| Phase   | Agent                    | Model / Tier | Tokens | Tool uses | Duration | Tasks covered |
|---------|--------------------------|--------------|--------|-----------|----------|---------------|
| explore | crombie-explore          | sonnet / low | 36,635 | 11        | 35s      | —             |
| design  | crombie-architect        | opus / medium | 54,075 | 15       | 128s     | —             |
| apply   | crombie-worker (batch 1) | sonnet / low | 23,068 | 4         | 23s      | T101, T102, T103 |
| apply   | crombie-worker (batch 1) | sonnet / medium | 30,743 | 10 | 28s | T201–T204 |
| verify  | crombie-worker (gates)   | sonnet / low | 12,462 | 2 | 10s | T301 |
| verify  | crombie-reviewer         | sonnet / medium | 45,167 | 16 | 51s | — |
| verify  | crombie-security         | sonnet / medium | 17,153 | 8 | 18s | — |
| verify  | crombie-worker (fix)     | sonnet / low | 15,285 | 4 | 17s | T901, T902 |

## Aggregate

| Phase               | Tokens |
|---------------------|--------|
| explore             | 36,635 |
| design              | 54,075 |
| apply               | 53,811 |
| verify              | 90,067 |
| **Sub-agent total** | 234,588 |
