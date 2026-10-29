# Proposal: message-replies

## Intent

Let users reply to a specific message in a room or a direct-message conversation, WhatsApp
style: the reply shows the quoted message (author + text) above the reply, and clicking the
quote scrolls to and highlights the original.

## Scope

**In scope**
- Room chat and DMs.
- Server-assigned message `id` on `chat` and `dm` frames (and history entries).
- Client sends `reply_to: <id>` on `chat` and `dm` envelopes.
- Room replies: the server resolves the target and embeds a truncated snapshot
  `reply: {id, from, text}` → unforgeable, visible to late joiners via `history`.
- DM replies: the server forwards only `reply_to` (no storage, CHAT-24 unchanged); each page
  resolves the quote from its own copy of the conversation.
- Desktop UI: one reply button per message (shown on hover / keyboard focus), composer
  preview "Respondiendo a X" with cancel button (and Escape), quote click → scroll + highlight.
- Quoted GIFs/images rendered as the text "🖼 GIF".

**Out of scope**
- Mobile gestures (swipe / long press), nested quote rendering (a reply to a reply quotes only
  its text), editing/deleting, reply notifications, persistence beyond the process lifetime.

## Approach

Server is the source of truth for what was said in a room. `Room` gets a `nextID` counter
(one per room, shared by chat and dm frames, starting at 1) and a bounded ring of the last
**200** room-message snapshots `{id, from, text truncated to 100 runes}`, both owned by
`Room.Run` (no locks, hub pattern). A `reply_to` that doesn't resolve (unknown, evicted, not a
room message, malformed) is ignored: the message is delivered without a quote, no error frame.

DMs keep CHAT-24 (never stored): the dm frame carries the sender's `reply_to` number as is.
Both participants received every DM (recipient + echo), so each page renders the quote from its
local message map; if the original isn't on the page it shows "Mensaje no disponible". This also
makes DM quotes unforgeable (each side uses its own copy).

Frontend: `addChat` gains `id` and a reply descriptor; each pane keeps `Map(id → li)`; the
reply target is a single page-level state cleared on pane switch and after a successful send.

## Affected Areas

| Area | Change |
|------|--------|
| `internal/chat/message.go` | `id`, `reply` on `message`; `id`, `reply_to` on `dmFrame`; `reply_to` parsing in `parseInbound` |
| `internal/chat/room.go` | id counter, reply snapshot ring, resolve in `relay`, forward in `direct` |
| `internal/chat/*_test.go` | update exact-shape helpers, new unit + integration tests |
| `cmd/chatroom/app.js` | render ids/quotes, reply button, composer state, send `reply_to`, scroll/highlight |
| `cmd/chatroom/index.html`, `app.css` | composer preview bar, quote/button/highlight styles |
| `cmd/chatroom/main_test.go` | markup presence checks |
| `openspec/specs/chat/spec.md`, `README.md` | protocol amendments (CHAT-01/10/11/18/21/22) + new requirements |

## Risks & mitigations

- **Frame shape changes** break exact-shape tests/specs → fields `omitempty`, appended after
  `avatar`; join/leave/welcome unchanged.
- **History frame growth** → 100-rune quote, flat snapshot.
- **Id probing** (small sequential ids) → room messages are visible to every member anyway; DM
  `reply_to` is never resolved server-side, so no cross-conversation leak.
- **XSS** → quote rendered with `textContent`, quoted image URLs never rendered as `<img>`.

## Rollback plan

Revert the PR. Clients that ignore `id`/`reply` keep working; there is no persisted state or
migration.
