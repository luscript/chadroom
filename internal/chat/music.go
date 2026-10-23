package chat

import (
	"regexp"
	"time"
)

const (
	musicInterval = 260 * time.Millisecond // 250 ms would let 5 controls into a closed 1 s window; 260 ms caps it at 4 (CHAT-30)
	maxMusicPos   = 24 * 60 * 60 * 1000    // ms

	musicLoad  = "load"
	musicPlay  = "play"
	musicPause = "pause"
	musicSeek  = "seek"
)

var videoID = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`) // Go's $ is end-of-text: no trailing \n

// musicCmd is a validated music control (CHAT-30).
type musicCmd struct {
	action string // one of the four action constants
	video  string // load only
	pos    int64  // seek only, ms
}

// player is a room's shared playback state (CHAT-30). Owned by Room.Run: no locks.
// Its methods take now so the position math is deterministic in tests.
type player struct {
	video   string // "" until the first load
	playing bool
	pos     int64     // ms into the video at at
	at      time.Time // monotonic reading when pos was taken
	by      string    // nickname behind the current state
}

// position is where playback is at now, in ms.
func (p *player) position(now time.Time) int64 {
	if !p.playing {
		return p.pos
	}
	return p.pos + now.Sub(p.at).Milliseconds()
}

// apply performs c on behalf of by and re-anchors the position at now. It returns false,
// leaving the state untouched, when c needs a loaded video and there is none.
func (p *player) apply(c musicCmd, by string, now time.Time) bool {
	if c.action != musicLoad && p.video == "" {
		return false
	}
	switch c.action {
	case musicLoad:
		p.video, p.pos, p.playing = c.video, 0, true
	case musicPlay:
		p.pos, p.playing = p.position(now), true
	case musicPause:
		p.pos, p.playing = p.position(now), false
	case musicSeek:
		p.pos = c.pos
	default:
		return false
	}
	p.at, p.by = now, by
	return true
}

// halt pauses at the current position when the room empties (CHAT-30). by is unchanged.
func (p *player) halt(now time.Time) {
	if p.playing {
		p.pos, p.at, p.playing = p.position(now), now, false
	}
}

// frame is the state as the `music` frame sent to clients; pos is taken at now.
func (p *player) frame(now time.Time) musicFrame {
	return musicFrame{typeMusic, p.video, p.playing, p.position(now), p.by, now.UnixMilli()}
}
