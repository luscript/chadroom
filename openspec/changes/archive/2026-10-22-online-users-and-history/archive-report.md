# Archive Report: online-users-and-history

**Archived**: 2026-10-22
**Branch**: `feat/online-users-and-history`
**Commits**: `4b8cda5` feat(chat) hub, `fb1e5b1` feat(web) page + README, plus this docs commit.
**Verify**: PASS (see `verify-report.md`).

## Specs merged into `openspec/specs/chat/spec.md`
- Purpose paragraph updated (presence, history, new snapshot frames).
- ADDED: CHAT-17 Online users list, CHAT-18 Recent message history.
- MODIFIED: CHAT-02 (slow drops now announced with `leave`; join after users/history),
  CHAT-05 (slow-drop scenario expects `leave`), CHAT-10 (envelope applies to event frames;
  users/history are the only other frames), CHAT-11 (welcome followed by users + history),
  CHAT-14 (handles users/history, field type checks), CHAT-16 (reconnect replaces transcript
  and list; "Reconectado" after history).
- No requirement removed. `specs/server/spec.md` unchanged.

## Deferred
- Backlog group `presence-history-polish` (`openspec/backlog.md`).
- Manual browser checklist from `verify-report.md` → tracked in the PR description.
- Next change: multiple rooms (`chat-features`).
