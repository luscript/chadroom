# Archive report: chat-notifications

Archived 2026-10-28. Verdict PASS (see `verify-report.md`), plus two fix rounds from user testing.

## Spec merge

- `specs/chat/spec.md`: CHAT-32 (notification sound + global mute) and CHAT-33 (tab indicator)
  added, including the "unattended" definition (tab hidden or window without focus).
- `specs/server/spec.md`: SRV-02 now serves `/favicon.svg` and `/favicon-unread.svg`
  (`image/svg+xml`); new "Favicons" scenario. CSP unchanged.
- No removals.

## Delivered

Web Audio blip for live `chat`/`dm` messages from others when the page is unattended or the
message lands in another pane (≤1/s); 🔔/🔕 global mute in `localStorage` (`chadroom.muted`);
`(N)` title and unread favicon while unattended, reset on focus/visible/leave.

User-test fixes: T911 (visible-but-unfocused window counts as unattended — Linux rarely reports
occlusion); T912 (first sound after a quiet period: resume-then-schedule and 120 ms lead-in for
idle audio sinks). Documented in README ("Notificaciones").

## Pending

- Backlog: JS unit tests for notification rules; per-route CSP for the embedded SVGs.
