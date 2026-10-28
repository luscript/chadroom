# Archive Report: global-music

- Specs merged into `openspec/specs/chat/spec.md`: CHAT-31 MODIFIED (player keeps following the room off-pane while joined to `musica`; frame creation after the pane was seen once; leaving `musica` stops it), CHAT-34 ADDED (header 🎵 local mute persisted in `chadroom.musicMuted`, read-only now-playing text).
- Implementation: `cmd/chatroom/app.js`, `index.html`, `app.css`, `main_test.go`, `README.md`. Server/protocol unchanged.
- Verify: PASS (see verify-report.md). Manual browser checklist pending.
