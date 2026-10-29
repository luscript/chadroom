# Tasks: youtube-playlist-import

Source of truth: `design.md` (decisions, code shapes, test plan) and `specs/` (CHAT-21/30/31
deltas, SRV-09). Gates: `/snap/go/current/bin/gofmt -l .`, `go vet ./...`, `go test -race ./...`.

## 1. Hub: `add_many` and queue cap 50 (`internal/chat`)

- [x] 1.1 `music.go`: `maxMusicQueue` 30 → 50, add `maxMusicAddMany = 50`, `musicAddMany = "add_many"`, `musicCmd.videos []string`, and the `apply` `add_many` case (idle → first becomes current via `start`; append `min(len, free)` with consecutive ids; nothing fits → `false`)
- [x] 1.2 `message.go`: `parseMusic` `add_many` case (raw starts with `[`, 1..50 elements, every element a JSON string matching `videoID`; anything else invalid)
- [x] 1.3 `client_test.go`: `TestParseInbound` → `reflect.DeepEqual`; valid/invalid `add_many` rows; `TestMusicAddManyOverWebSocket` (one frame; second batch within 260 ms dropped)
- [x] 1.4 `room_test.go`: `TestPlayerApply` `add_many` subtests (idle, playing, finished+empty, partial fit, full, idle with 50); `TestMusicAddManyBroadcast`, `TestMusicAddManyFull`; `TestMusicLimitsMirroredInJS` (reads `../../cmd/chatroom/app.js`); comment at the queue-cap hub test

## 2. Server: `GET /playlist` (`cmd/chatroom`)

- [x] 2.1 `playlist.go` (new): `playlistImport` per design (header `X-Goog-Api-Key`, no redirects, one 5 s deadline for both calls, 1 MiB cap, `playlistItems` → `videos.list` filter, not-found mapping, sanitized errors, TTL cache 10 min / ≤100, empty not cached, JSON headers like `/gifs`)
- [x] 2.2 `main.go`: `config.youtubeKey` from trimmed `YOUTUBE_API_KEY`; build or log disabled; `newRouter(chatSrv, gifs, playlists)` registers `GET /playlist` only when non-nil
- [x] 2.3 `playlist_test.go` (new) per the design test table; `gifs_test.go`: `fakeGiphy` → `fakeUpstream`, third `nil` to `newRouter`
- [x] 2.4 `main_test.go`: third `nil` to `newRouter`; `TestPlaylistIDMirroredInJS`

## 3. Page (`cmd/chatroom/app.js`)

- [x] 3.1 Constants: `YT_LIST_ID`, `YT_MIX_PREFIX`, `MUSIC_QUEUE_MAX` 50, `MUSIC_ADD_MANY_MAX` 50, `PLAYLIST_TEXT`; `music.importing` / `music.importSeq`
- [x] 3.2 `parseYouTubeLink` replaces `parseYouTubeId` (returns `{video, list}`); `freeSlots()`
- [x] 3.3 Submit handler + `importPlaylist` (token-guarded, X first and deduped, mix fallback, truncation + "Se agregaron N de M.", fixed texts only); `renderMusic` disables Poner while importing; `resetMusic` invalidates an in-flight import

## 4. Docs

- [x] 4.1 `README.md`: `YOUTUBE_API_KEY` (restrict to YouTube Data API v3), `GET /playlist`, cap 50, `add_many`, accepted link forms
- [x] 4.2 `CLAUDE.md`: `playlist.go` in the structure; key-hygiene rule covers the YouTube key; "En curso: youtube-playlist-import"
- [x] 4.3 `openspec/backlog.md`: replace the playlists item; add rate limiting `/playlist` and `youtu.be/X?list=Y` follow-up

## Batches (file-independent)

- Batch A (parallel): group 1 (worker 1), group 2 (worker 2), groups 3 + 4 (worker 3)
- Mirror tests (1.4, 2.4) pass only once 3.1 is in; verify runs after Batch A.
