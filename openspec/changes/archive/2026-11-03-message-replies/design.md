# Design: Message replies (rooms and DMs)

## Technical Approach

Replies are added in the same two places that already own messages:

- **Server (`internal/chat`)**: `Room.Run` is the only goroutine that sees every chat and dm
  frame in order. It gets three new fields: an id counter, a fixed 200-slot ring of reply
  targets, and the ring's write index. `relay` gives each room message an id, resolves
  `reply_to` against the ring, embeds the quote and then adds the new message to the ring.
  `direct` gives the dm frame an id and copies `reply_to` into it without looking it up
  (CHAT-24 still holds: nothing about a DM is kept). `parseInbound` reads `reply_to` with the
  existing `jsonInt` helper. Nothing changes in `client.go`, `server.go` or `nickname.go`.
- **Page (`cmd/chatroom`)**: `addChat` gets an `id` and a quote descriptor. Each pane keeps
  `msgs: Map(id → {li, from, text})`. A single page-level `replyTarget` drives the composer
  preview and the `reply_to` that `sendText` adds. Room quotes come from the server's
  `reply`. DM quotes come from the conversation's own `msgs`.

This follows the proposal and spec delta (CHAT-10/18/21/22 amended, CHAT-35/36 added) without
deviation.

## Architecture Decisions

### Decision: Counter and reply ring live on `Room`, touched only by `Run`

**Choice**: Add `nextID int64` (the last id assigned), `targets [replyTargets]quote` and
`targetNext int` as plain `Room` fields. Only `relay` and `direct` use them, and both are
called only from `dispatch`, which runs only in `Run`.
**Alternatives considered**: (a) An `atomic.Int64` counter incremented in `readPump`. Ids would
then not match the order frames are published in, and it adds a second concurrency mechanism.
(b) One global counter in `Server`. It needs a lock and couples rooms that CHAT-19 isolates.
**Rationale**: This is the hub pattern from CLAUDE.md: no locks, and the order ids are assigned
in is the order clients receive frames. An id is used only after `encode` succeeds
(`cr.nextID = m.ID` after the nil check). Blank, invalid, `dm_error`, self/offline DMs and
frames from removed senders never reach that line, so they don't use an id (CHAT-35).

### Decision: One id sequence shared by chat and dm frames

**Choice**: `chat` and `dm` frames draw ids from the same `nextID` (spec CHAT-35).
**Alternatives considered**: Separate counters for room and DM, or random 53-bit ids.
**Rationale**: Because ids are unique across both kinds, a room `reply_to` that names a DM id
can't match anything: DMs are never put in the ring. A DM `reply_to` is only looked up in that
conversation's pane. So the shared counter is never ambiguous and needs no extra state.
**Accepted cost (metadata)**: Room members can see gaps in room ids, which shows that *some*
DMs were sent in the room, and when. They can't see who sent them or what they said. For a
friends chat this is acceptable (memory: "pragmatic scope"). If it ever matters, a separate DM
counter is a one-line change and goes in `backlog.md`.

### Decision: Reply ring is a fixed `[200]quote` array with a wrapping write index and a linear lookup

**Choice**:
```go
const replyTargets = 200 // room messages that can still be quoted (CHAT-35)
const quoteRunes   = 100 // runes kept in a quote

targets    [replyTargets]quote // ring; a zero ID marks an empty slot (reply_to is always ≥ 1)
targetNext int                 // slot the next target overwrites
```
`keepTarget(q)` writes `targets[targetNext]` and advances `targetNext = (targetNext+1) % replyTargets`.
This evicts the oldest entry automatically. `replyTarget(id)` scans all 200 slots and
returns a copy as `*quote`, or nil when `id < 1` or nothing matches.
**Alternatives considered**: (a) `map[int64]quote` plus a FIFO slice for eviction. Lookup is
O(1), but two structures must be kept in sync, and 200 comparisons per reply costs nothing.
(b) Reuse `history`. It holds only 50 entries and stores full 4 KB texts, and making it bigger
would change what CHAT-18 replays.
**Rationale**: The array is allocated once (about 8 KB per room) and never resized. Eviction
needs no code beyond the wrap, and there are no pointers into it. 200 is more than the 50
replayed messages because people who stay online can scroll further back than the replay
goes. Each snapshot is limited to `quoteRunes`, so the worst case is about 200 × 400 B per
room.

### Decision: Quote text is truncated and stored by the server, so it can't be forged

**Choice**: `quote{ID, From, Text: quoteText(m.Text)}`, where `quoteText` keeps the first 100
runes and removes trailing whitespace with `strings.TrimRightFunc(…, unicode.IsSpace)`.
**Alternatives considered**: Reuse `truncateRunes` from `nickname.go`. It also trims
*leading* space and trims only when it cuts, which doesn't match the spec. A client-supplied
quote would let anyone put words in someone else's mouth.
**Rationale**: `from` is the nickname at the moment the target was relayed, so it stays correct
after that person leaves. The text is valid UTF-8 (CHAT-09), so cutting with `[]rune` is safe.
Quotes are flat: a reply's own ring entry keeps only its text, never its `Reply`.

### Decision: DM replies are resolved by the page, not the server

**Choice**: The dm frame carries `reply_to` exactly as sent (validated as an integer ≥ 1). Each
page looks it up in that conversation's `msgs`. When it's not there, the page shows
"Mensaje no disponible".
**Alternatives considered**: Keep DM snapshots on the server. That needs CHAT-24 to be amended
(DMs are never kept), and the server would become a place DM text is stored.
**Rationale**: Both participants already received every DM (the recipient copy and the echo),
so each page has the original. Each side renders from its own copy, so a peer can't forge a
quote. It can only name an id, and an id the page doesn't know renders as "no disponible". The
server never looks anything up for a DM `reply_to`, so it can't be used to read someone else's
DMs.

### Decision: Field placement and `omitempty`

**Choice**: Append fields at the end of each struct. `message` gets `ID int64 json:"id,omitempty"`
and `Reply *quote json:"reply,omitempty"`, so join, leave and welcome are byte-for-byte the
same as today. `dmFrame` gets `ID int64 json:"id"` (always present) and
`ReplyTo int64 json:"reply_to,omitempty"`.
**Rationale**: This matches the spec's field order (`…, ts, avatar, id, reply` and
`…, ts, id, reply_to`). A pointer is the only way `omitempty` can drop a struct. The `quote`
is built fresh for each message and never changed afterwards, so the pointer shared between
the published frame and `history` is safe.

### Decision: Size limits stay where they are

- **Inbound (4096 bytes)**: At most `reply_to` adds `,"reply_to":9007199254740991` (28 bytes).
  `readPump`'s read limit already covers it. `sendText` measures the *whole encoded frame*,
  `reply_to` included, so when a message is too long the page says so before sending. It keeps
  the draft and the reply target so the user can shorten the text.
- **Outbound (no cap)**: A reply frame can be larger than 4096 bytes: a 4096-byte text plus a
  quote of at most 100 runes, which Go's HTML escaping can expand to about 600 bytes. Browsers
  and gorilla clients have no read limit, so this is fine. The worst-case history frame grows by
  about 50 × 0.7 KB. An integration test checks that an over-4096 reply frame is delivered.

## Data Flow

### Room reply

```
Page B                 readPump(B)            Room.Run (relay)                       A, B, C pages
  │ {"type":"chat","text":"dale","reply_to":5}
  ├──────────────────────▶ parseInbound → inbound{text:"dale", replyTo:5}
  │                        ├── broadcast chan ───▶ dispatch → relay
  │                        │                        q := replyTarget(5)  // ring scan, copy or nil
  │                        │                        m := message{chat,B,"dale",ts,avatar,
  │                        │                                     ID: nextID+1, Reply: q}
  │                        │                        encode(m) ok → nextID = m.ID
  │                        │                        remember(m)          // history (50)
  │                        │                        keepTarget({m.ID,"B",quoteText("dale")})
  │                        │                        publish ───────────────────────▶ addRaw 'chat'
  │                        │                                                          parseReply(msg.reply)
  │                        │                                                          addChat(..., id, quote)
  late joiner D ── admit → history frame (entries keep id + reply) ───────────▶ renderHistory
```

### DM reply

```
Page A                 readPump(A)            Room.Run (direct)                     Pages B and A
  │ {"type":"dm","to":"B","text":"sí","reply_to":7}
  ├──────────────────────▶ parseInbound → inbound{DM, to:"B", text:"sí", replyTo:7}
  │                        └── broadcast ───────▶ direct: self/offline checks (no id used on error)
  │                                                f := dmFrame{dm,A,B,"sí",ts, ID: nextID+1, ReplyTo: 7}
  │                                                encode ok → nextID = f.ID   // 7 is never looked up
  │                                                deliverPrivate(f, B, A) ─────▶ addRaw 'dm'
  │                                                                               conv.msgs.get(7)?
  │                                                                               ├─ yes → quote {from,text}
  │                                                                               └─ no  → "Mensaje no disponible"
```

## Backend Changes (exact)

### `internal/chat/message.go`

```go
const (
    // … existing …
    replyTargets = 200 // room messages that can still be quoted (CHAT-35)
    quoteRunes   = 100 // runes kept in a quote (CHAT-35)
)

// quote is a reply target: a flat, truncated snapshot of a room message (CHAT-35).
type quote struct {
    ID   int64  `json:"id"`
    From string `json:"from"`
    Text string `json:"text"`
}

type message struct {
    Type   string `json:"type"`
    From   string `json:"from"`
    Text   string `json:"text"`
    TS     int64  `json:"ts"`
    Avatar string `json:"avatar,omitempty"`
    ID     int64  `json:"id,omitempty"`    // chat only (CHAT-35); 0 on welcome/join/leave
    Reply  *quote `json:"reply,omitempty"` // chat only, when reply_to resolved
}

type dmFrame struct {
    Type    string `json:"type"`
    From    string `json:"from"`
    To      string `json:"to"`
    Text    string `json:"text"`
    TS      int64  `json:"ts"`
    ID      int64  `json:"id"`                 // same in both copies (CHAT-35)
    ReplyTo int64  `json:"reply_to,omitempty"` // forwarded as is, never looked up (CHAT-24)
}

type inbound struct {
    from    *Client
    kind    inboundKind
    to      string
    text    string
    replyTo int64    // inboundChat/inboundDM: valid reply_to (≥ 1), else 0
    music   musicCmd
}

// replyTo returns env's reply_to when it is a JSON integer ≥ 1, else 0 (CHAT-21: ignored).
func replyTo(env map[string]json.RawMessage) int64 {
    if n, ok := jsonInt(env["reply_to"]); ok && n >= 1 {
        return n
    }
    return 0
}

// quoteText is the reply-target form of a message text: first quoteRunes runes, no trailing space.
func quoteText(s string) string { … utf8.RuneCountInString / []rune cut / strings.TrimRightFunc(s, unicode.IsSpace) }
```

Changes to `parseInbound`:
- `case typeDM`: on success, `return inbound{kind: inboundDM, to: to, text: text, replyTo: replyTo(env)}, true`.
  The `inboundBadDM` and blank paths are unchanged, and a bad `reply_to` never turns a DM into
  `inboundBadDM`.
- `case typeChat` with string text: `return inbound{text: text, replyTo: replyTo(env)}, …`.
- These stay unchanged: the plain-text fallback (including `chat` with a non-string `text`,
  where the whole frame becomes the text and `replyTo` is 0), typing and music. An inbound
  `id` or `reply` key is never read, so it is ignored.

Because `jsonInt` rejects `null`, strings, fractions, exponents and out-of-range values,
`"3"`, `0`, `-1`, `1.5`, `1e2`, `null` and `99999999999999999999` all become 0.

### `internal/chat/room.go`

- `Room` gains `nextID`, `targets` and `targetNext`, as in the decision above. `NewRoom` needs
  no change because the zero values are correct.
- `relay`:
  ```go
  m := message{Type: typeChat, From: in.from.nickname, Text: in.text, Avatar: in.from.avatar,
      TS: time.Now().UnixMilli(), ID: cr.nextID + 1, Reply: cr.replyTarget(in.replyTo)}
  if chat := encode(m); chat != nil {
      cr.nextID = m.ID
      cr.remember(m)
      cr.keepTarget(quote{ID: m.ID, From: m.From, Text: quoteText(m.Text)})
      cr.publish(chat)
  }
  ```
  The target is resolved *before* the new message is added to the ring, so a `reply_to` equal
  to the message's own id can't match.
- `direct`: use a keyed literal with `ID: cr.nextID + 1, ReplyTo: in.replyTo` and set
  `cr.nextID = f.ID` after encoding succeeds. The self and offline branches stay as they are
  and don't use an id.
- New Run-only helpers: `keepTarget(q quote)` and `replyTarget(id int64) *quote`.
- `remember` is unchanged. History entries carry `ID` and `Reply` because they are the same
  `message` value.

## Frontend Changes (`cmd/chatroom`)

### `app.js`

New page state and helpers:
- `let replyTarget = null; // {pane, id, from, text}: the message the composer replies to`
- `const validId = (v) => Number.isSafeInteger(v) && v >= 1;`
- `parseReply(r)` returns `{id, from, text}` only when `r` is a non-null, non-array object,
  `validId(r.id)` holds, and `from` and `text` are strings. Otherwise it returns `null`.
- `dmQuote(conv, id)`: `conv.msgs.get(id)`, giving `{id, from, text}` when found and
  `{id, missing: true}` when not.
- `isImageQuote(text)`: true when the text has no whitespace, parses as a URL with `https:`,
  and its `hostname` is in `IMAGE_HOSTS`. The path is not checked, because a room quote is cut
  at 100 runes and the `.gif` ending may be gone. `quoteLabel(text)` returns `'🖼 GIF'` for
  those and the text otherwise.
- `forgetIds(pane)`: `pane.msgs = new Map()`. For a conversation it also removes the old
  `.msg-reply` buttons (`pane.list.querySelectorAll('.msg-reply')`). It calls `cancelReply()`
  when `replyTarget?.pane === pane`.
- `startReply(pane, id)`: does nothing unless `pane === current()`, the entry exists, and (for
  a conversation) `pane.open`. It sets `replyTarget`, calls `renderReplyPreview()` and then
  `draft.focus()`.
- `cancelReply()`: sets `replyTarget = null` and calls `renderReplyPreview()`.
- `renderReplyPreview()`: sets `$('reply-preview').hidden = !replyTarget`, then fills
  `#reply-preview-author` with `'Respondiendo a ' + from` and `#reply-preview-text` with
  `quoteLabel(text)`, both with `textContent`.
- `jumpTo(pane, id)`: does nothing unless `pane.msgs.get(id)?.li.isConnected`. It calls
  `li.scrollIntoView({block: 'center'})`, adds the `msg-flash` class and removes it with
  `setTimeout(…, 1500)`, after first clearing any pending timer stored on the entry.

Changes to existing functions:
- `createRoom` and `createConversation`: add `msgs: new Map()` to the pane object.
- `joinRoom`, `teardown` and `renderHistory` (right after `replaceChildren`): call
  `forgetIds(room)`.
- `reopenConversation`: call `forgetIds(conv)` first. It may be a different person, and the
  server may have restarted, so old ids are no longer reliable. Old quotes stop doing anything
  and old messages lose their reply buttons.
- `removeConversations`: when `replyTarget?.pane === conv`, call `cancelReply()`.
- `activate`: at the start, call `cancelReply()` when `replyTarget && replyTarget.pane !== current()`
  after `active` and `activeConv` are set.
- `addChat(pane, author, text, ts, avatarUrl, id = 0, quote = null)`:
  - When `quote` is set, build the quote first inside the bubble. A found quote is a
    `<button type="button" class="msg-quote">` holding `span.msg-quote-author` (`quote.from`)
    and `span.msg-quote-text` (`quoteLabel(quote.text)`), with
    `aria-label = 'Ir al mensaje de ' + quote.from` and a click that calls
    `jumpTo(pane, quote.id)`. A missing quote is `<div class="msg-quote missing">` with the text
    "Mensaje no disponible".
  - The message text is added with `bubble.append(text)` (a text node) instead of
    `bubble.textContent = text`, so the quote isn't overwritten. In the image path,
    `img.onerror` becomes
    `bubble.classList.remove('media'); img.replaceWith(document.createTextNode(text));`.
  - When `validId(id)`, call `pane.msgs.set(id, {li, from: author, text})` and append
    `<button type="button" class="btn btn-ghost msg-reply" title="Responder"
    aria-label="Responder a {author}">↩</button>` to `li` after `body`. Its click calls
    `startReply(pane, id)`.
- `addRaw`:
  - For `'chat'`:
    `addChat(room, msg.from, msg.text, msg.ts, msg.avatar, validId(msg.id) ? msg.id : 0, parseReply(msg.reply))`.
  - For `'dm'`: after `reopenConversation`,
    `const quote = validId(msg.reply_to) ? dmQuote(conv, msg.reply_to) : null;`, then
    `addChat(conv, …, validId(msg.id) ? msg.id : 0, quote)`. The lookup happens *before*
    `addChat` registers the new id.
  - Update the header comment, which currently lists only `{type, from, text, ts}`.
- `renderHistory`: pass `m.id` and `m.reply` the same way as the `'chat'` case.
- `sendText(text)`: build `env` as today, then
  `if (replyTarget && replyTarget.pane === current()) env.reply_to = replyTarget.id;`. The
  existing size check runs on `JSON.stringify(env)`. After `room.ws.send(frame)`, call
  `cancelReply()`. Draft and GIF-URL submissions and GIF-grid clicks all go through
  `sendText`, so GIFs carry `reply_to` and clear the preview automatically.
- Escape listener:
  `if (e.key !== 'Escape') return; if (Object.values(panels).some((p) => !p.panel.hidden)) closePanels(); else if (replyTarget) cancelReply();`
- Wiring: `$('reply-cancel').addEventListener('click', () => { cancelReply(); draft.focus(); });`

Ids and quote strings are only used as `Map` keys and `textContent`. They never go into element
ids, classes, selectors or attributes other than `aria-label` and `title`.

### `index.html`

Inside `form#send-form.composer`, immediately before `<div class="composer-bar">`:
```html
<div id="reply-preview" class="reply-preview" aria-live="polite" hidden>
  <div class="reply-preview-body">
    <span id="reply-preview-author" class="reply-preview-author"></span>
    <span id="reply-preview-text" class="reply-preview-text"></span>
  </div>
  <button id="reply-cancel" type="button" class="btn btn-ghost reply-cancel" aria-label="Cancelar respuesta" title="Cancelar respuesta">✕</button>
</div>
```
This needs no inline style or script. The global `[hidden]{display:none !important}` rule
(`app.css:42`) already hides it.

### `app.css`

Append a `/* Replies (CHAT-36) */` block at the end of the file, after the retro skin, so it
wins over the skin. It uses only existing tokens:
```css
.msg-reply{flex:none;align-self:center;margin-left:auto;padding:2px 6px;font-size:13px;opacity:0;pointer-events:none}
.msg:hover .msg-reply,.msg:focus-within .msg-reply{opacity:1;pointer-events:auto}
.msg-quote{display:block;width:100%;margin:0 0 var(--space-2);padding:var(--space-1) var(--space-3);border:0;border-left:3px solid var(--color-accent-600);border-radius:var(--radius-sm);background:var(--color-accent-900);color:inherit;font:inherit;font-size:12px;text-align:left;cursor:pointer}
.msg-quote.missing{cursor:default;font-style:italic;color:var(--color-neutral-500)}
.msg-quote-author{display:block;font-weight:bold;color:var(--color-accent-700)}
.msg-quote-text{display:-webkit-box;-webkit-box-orient:vertical;-webkit-line-clamp:2;line-clamp:2;overflow:hidden;overflow-wrap:anywhere}
.msg-flash{background:var(--color-accent-900);border-radius:var(--radius-md)}
@media (prefers-reduced-motion:no-preference){.msg{transition:background-color .6s}}
.reply-preview{display:flex;align-items:center;gap:var(--space-3);margin-bottom:var(--space-2);padding:var(--space-2) var(--space-3);border-left:3px solid var(--color-accent-600);border-radius:var(--radius-sm);background:var(--color-accent-900);font-size:12px}
.reply-preview-body{flex:1;min-width:0}
.reply-preview-author{display:block;font-weight:bold;color:var(--color-accent-700)}
.reply-preview-text{display:block;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
```
The explicit `font-size:12px` on `.msg-quote` keeps quotes small inside emoji-only bubbles. The
reply button is a third flex item in `.msg` (with `margin-left:auto`), so it never covers the
text. It uses `opacity`, not `display:none`, so it stays in the tab order and `:focus-within`
can reveal it.

## File Changes

| File | Action | Description |
|------|--------|-------------|
| `internal/chat/message.go` | Modify | `quote` type; `ID` and `Reply` on `message`; `ID` and `ReplyTo` on `dmFrame`; `replyTo` on `inbound`; `replyTo()` and `quoteText()`; `replyTargets` and `quoteRunes` constants; `parseInbound` reads `reply_to` for chat and dm |
| `internal/chat/room.go` | Modify | `nextID`, `targets` and `targetNext` fields; ids and quotes in `relay`; id and `reply_to` in `direct`; `keepTarget` and `replyTarget` helpers |
| `internal/chat/room_test.go` | Modify | Update `decode`, `decodeDM` and `decodeHistory`; new hub unit tests |
| `internal/chat/client_test.go` | Modify | `TestParseInbound` rows; test that forged `id`/`reply` are ignored; fix the comment on `dial` ("4-field envelope") |
| `internal/chat/server_test.go` | Modify | End-to-end tests for replies over WebSocket |
| `cmd/chatroom/app.js` | Modify | Reply state, helpers, `addChat`/`addRaw`/`renderHistory`/`sendText`/`activate`/Escape changes |
| `cmd/chatroom/index.html` | Modify | `#reply-preview` block before `.composer-bar` |
| `cmd/chatroom/app.css` | Modify | Rules for the reply button, quote, highlight and preview |
| `cmd/chatroom/main_test.go` | Modify | `TestIndexHasReplyPreview` markup and JS checks |
| `README.md` | Modify | Protocol: `id`, `reply`, `reply_to`; the reply-target ring; DM forwarding |

`client.go`, `server.go`, `nickname.go`, `music.go` and `cmd/chatroom/main.go` do not change.

## Test Plan

Run `go test -race ./...` and `gofmt -l .` (with snap, use `/snap/go/current/bin/…`).

**Existing helpers that change (`room_test.go`)**
- `decode`: always require `type`, `from`, `text` and `ts`. A `chat` frame must also have `id`
  (an integer ≥ 1) and may have `reply`, which must have exactly `id`, `from` and `text`.
  Every other type keeps exactly 4 fields. Remove the hard `len(fields) != 4` check and count
  the allowed keys per type. Now every chat frame decoded in existing tests also checks that
  ids are present.
- `decodeDM`: use `exactFields` with `type, from, to, text, ts, id`, plus `reply_to` when it
  is present.
- `expectHistory` compares only type, from and text, so it needs no change. The `dial`
  comment in `client_test.go` changes.

**New hub unit tests (`room_test.go`, using `NewRoom` with fake clients and `next`/`nextRaw`)**
- `TestMessageIDsSharedByChatAndDM`: chat, DM, chat get ids 1, 2, 3, and both DM copies have
  the same id.
- `TestDiscardedFramesUseNoID`: a self or offline DM (`dm_error`) and then a chat gives the
  chat id 1. A frame from a removed sender uses no id.
- `TestReplyResolved`: a reply embeds `{id, from, text}`, and a late joiner's history entry
  keeps both `id` and `reply`.
- `TestReplyUnresolvedIgnored`: `reply_to` 999, a DM id, the message's own next id, and id 1
  after 201 more messages each relay with an id and no `reply`. `expectEmpty` checks that no
  other frame is sent.
- `TestReplyQuoteTruncated`: a 300-rune text gives a 100-rune quote, and a space at rune 100
  is trimmed.
- `TestReplyQuotesDoNotNest`: replying to a reply quotes only that reply's text.
- `TestDMReplyToForwarded`: both copies carry `reply_to` unchanged, and the ring holds no DM
  entry (afterwards a room `reply_to` equal to the DM id doesn't resolve).
- `TestJoinLeaveWelcomeHaveNoID`: `exactFields` on welcome, join and leave.

**`client_test.go` → `TestParseInbound` new rows**
- chat with `reply_to:3` gives `replyTo:3`.
- chat with `"3"`, `0`, `-1`, `1.5`, `1e2`, `null` or `99999999999999999999` gives
  `replyTo:0`, and the text is still relayed.
- dm with `reply_to:7` gives `inboundDM` with `replyTo:7`.
- dm with a bad `to` and a valid `reply_to` is still `inboundBadDM`.
- typing with `reply_to` gives no `replyTo`.
- chat with a non-string text and `reply_to` falls back to plain text with `replyTo:0`.

Existing rows still pass with `reflect.DeepEqual` because `replyTo` is 0. Add
`TestInboundIDAndReplyIgnored`: sending
`{"type":"chat","text":"hi","id":99,"reply":{"id":1,"from":"X","text":"forged"}}` is relayed
with the server's id and no `reply`.

**`server_test.go` integration tests (httptest + gorilla)**
- A sends, B replies with `reply_to`: A and B both get the quote, and C joining later gets it
  through `history`.
- A DMs B, B replies by DM with `reply_to`: both get the same `id` and `reply_to`, and a third
  client gets nothing.
- An inbound frame of about 4000 bytes plus `reply_to` is accepted, and the outbound reply
  frame, which is larger than 4096 bytes, is delivered intact.

**`cmd/chatroom/main_test.go` → `TestIndexHasReplyPreview`**
- `index.html` contains `id="reply-preview"`, `id="reply-preview-author"`,
  `id="reply-preview-text"` and `id="reply-cancel"`, and the index of `id="reply-preview"` is
  lower than the index of `class="composer-bar"`.
- `app.js` contains `reply_to`, `'Mensaje no disponible'` and `'🖼 GIF'`.
- `app.css` contains `.msg:focus-within .msg-reply`.
- `TestIndexHasNoInlineCode` already covers CSP.

**Manual two-browser checklist** (`go run ./cmd/chatroom`, browser A and a private window B)
1. A sends `¿pizza?` in `#general`. In B, hover over it, press ↩, and check that the preview
   says "Respondiendo a A" and the draft is focused. Send `dale`: both pages show the quote,
   and the preview is gone.
2. In B, Tab to a message: the ↩ button appears and Enter starts a reply.
3. Escape with the emoji panel open closes the panel first, and a second Escape cancels the
   reply. Switching tabs also cancels it.
4. Reply with a GIF from the grid: it has a quote, and quoting that GIF shows "🖼 GIF".
5. Click a quote far up the pane: the original scrolls to the center and flashes.
6. Reload B and check that `#general` history still shows the quotes and clicking them still
   works.
7. Open a DM, reply on both sides, and check the quotes. Reload B, have A reply to an old DM,
   and check that B shows "Mensaje no disponible".
8. Reply to a 300-character message: the quote is cut to 2 lines.

## Security Notes

- **XSS**: Every quoted string (`from`, `text`) and the preview are rendered with `textContent`
  or text nodes. A quoted image URL is never turned into an `<img>`. CSP stays strict: no
  inline script or style, and the new markup is static HTML with classes.
- **Forgery**: `id` and `reply` are created only by the server, and inbound copies are
  ignored (tested). A room quote is a server snapshot. A DM quote comes from each page's own
  copy, so a peer can only name an id.
- **Id probing**: ids are small and sequential. A room member can send `reply_to` values to get
  100-rune snapshots of up to the last 200 room messages, more than the 50 in the history
  replay. This is accepted: room messages are public to the room, and every probe is itself a
  room message that everyone sees. A DM `reply_to` is never looked up on the server, so DM text
  can't be probed. The shared counter lets room members see that DMs happened and when, but not
  who sent them or what they said (accepted; see the decision above).
- **Page input validation**: `id`, `reply.id` and `reply_to` are used only when
  `Number.isSafeInteger` and ≥ 1 hold. `reply` is used only when `from` and `text` are strings.
  Ids are only `Map` keys and never reach the DOM, so there is no selector injection.
- **Resource bounds**: The ring has a fixed size and is allocated once. The lookup scans at most
  200 entries. Quotes are at most 100 runes. No logging is added, so DM `reply_to` and text are
  never logged (CHAT-24).
