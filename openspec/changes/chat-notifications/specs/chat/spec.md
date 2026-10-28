# Delta for Chat

## ADDED Requirements

### Requirement: CHAT-32 Chat page notification sound

A **notifying message** is a `chat` frame (CHAT-10) or a `dm` frame (CHAT-22) received live
whose `from` is not the user's own nickname in that room. Messages delivered in a `history`
frame, the user's own echoed messages, `join`/`leave`, `typing`, `music`, `dm_error` frames
and system notices MUST NOT be notifying messages.

The page is **unattended** when its tab is hidden or its window does not have the focus (the
user is in another tab, window or application; `document.hidden` alone misses a visible but
unfocused window).

When a notifying message arrives and the page is unattended, or the message belongs to a pane
(room or private conversation) other than the one currently shown, the page MUST play a short
sound (at most 200 ms), synthesized in the page (no audio file, no change to the
Content-Security-Policy). The page MUST play at most one sound per second; extra notifying
messages inside that window are silent. If the browser does not allow audio, the page MUST
fail silently and keep working.

The chat header MUST offer a single global toggle (🔔 on / 🔕 muted, with `aria-pressed`)
that mutes the sound for every room and private conversation. The choice MUST be remembered
in the browser (`localStorage`, best effort) and restored on the next visit. Muting MUST NOT
affect the tab indicator (CHAT-33) nor the unread badges.

#### Scenario: Message in another room

- GIVEN Ana has joined `general` and `juegos`, is looking at `general`, sound not muted
- WHEN Bob sends a message in `juegos`
- THEN Ana's page plays the notification sound once

#### Scenario: Message in the pane being read

- GIVEN Ana's tab is visible and shows `general`
- WHEN Bob sends a message in `general`
- THEN no sound plays

#### Scenario: Hidden tab

- GIVEN Ana's tab is hidden while showing `general`
- WHEN Bob sends a message in `general` or a private message to Ana
- THEN the notification sound plays

#### Scenario: Visible window without focus

- GIVEN Ana's browser shows `general` but she is typing in another application
- WHEN Bob, from another computer, sends a message in `general`
- THEN the notification sound plays and the title reads `(1) ★chadroom★`
- AND when Ana clicks back into the browser window, the title and favicon reset

#### Scenario: Own messages and history are silent

- GIVEN Ana's tab is hidden
- WHEN Ana's own message is echoed, or she rejoins and receives the room history
- THEN no sound plays

#### Scenario: Other events are silent

- GIVEN Ana's tab is hidden
- WHEN someone joins, leaves, types or changes the music
- THEN no sound plays

#### Scenario: Burst

- GIVEN Ana's tab is hidden
- WHEN five messages from others arrive within one second
- THEN the sound plays once

#### Scenario: Mute is global and remembered

- GIVEN Ana presses 🔔 and it turns into 🔕
- WHEN messages arrive in any room or private conversation while the tab is hidden
- THEN no sound plays
- AND after reloading the page and joining again, the toggle still shows 🔕

### Requirement: CHAT-33 Chat page tab indicator

While the page is unattended, every notifying message (CHAT-32), muted or not and including those
in the pane being shown, MUST increment an unread counter. While the counter is above zero the
document title MUST be `(N) ` followed by the page's original title, and the favicon MUST be
the unread variant (`/favicon-unread.svg`). When the page is attended again (visible and focused), or
the user leaves the chat, the counter MUST reset to zero and the title and favicon MUST return to the
original (`/favicon.svg`). An attended page MUST NOT change title or favicon.

#### Scenario: Unread while away

- GIVEN Ana's tab is hidden
- WHEN Bob sends three messages across her rooms and conversations
- THEN the tab title reads `(3) ★chadroom★` and the favicon shows the unread variant

#### Scenario: Coming back resets

- GIVEN Ana's tab shows `(3) ★chadroom★`
- WHEN she switches back to the tab
- THEN the title is `★chadroom★` and the favicon is the normal one

#### Scenario: Muted still indicates

- GIVEN Ana muted the sound and her tab is hidden
- WHEN Bob sends a message
- THEN no sound plays AND the title reads `(1) ★chadroom★`
