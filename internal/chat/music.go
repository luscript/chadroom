package chat

import (
	"regexp"
	"slices"
	"time"
)

const (
	musicInterval = 260 * time.Millisecond // 250 ms would let 5 controls into a closed 1 s window; 260 ms caps it at 4 (CHAT-30); also the ended gate's interval
	maxMusicPos   = 24 * 60 * 60 * 1000    // ms
	maxMusicQueue = 30                     // queued tracks, not counting the current one (CHAT-30)

	musicAdd     = "add"
	musicAddMany = "addmany" // a whole list of tracks in one control (one rate-gate slot)
	musicRemove  = "remove"
	musicSkip    = "skip"
	musicEnded   = "ended"
	musicPlay    = "play"
	musicPause   = "pause"
	musicSeek    = "seek"
)

var videoID = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`) // Go's $ is end-of-text: no trailing \n

// queueItem is a track waiting its turn (CHAT-30). ID is never reused within a room.
type queueItem struct {
	ID    int64  `json:"id"`
	Video string `json:"video"`
	By    string `json:"by"` // who added it
}

// musicCmd is a validated music control (CHAT-30).
type musicCmd struct {
	action string   // one of the action constants
	video  string   // add only
	videos []string // addmany only: 1..maxMusicQueue+1 valid ids
	pos    int64    // seek only, ms
	id     int64    // remove only
	seq    int64    // ended only
}

// player is a room's shared playback state (CHAT-30). Owned by Room.Run: no locks.
// Its methods take now so the position math is deterministic in tests.
type player struct {
	video    string // "" until the first add
	playing  bool
	pos      int64     // ms into the video at at
	at       time.Time // monotonic reading when pos was taken
	by       string    // nickname behind the current state
	seq      int64     // counts tracks that became current; 0 until the first
	queue    []queueItem
	nextID   int64 // last id handed out
	finished bool  // the current track ended with nothing queued
}

// position is where playback is at now, in ms.
func (p *player) position(now time.Time) int64 {
	if !p.playing {
		return p.pos
	}
	return p.pos + now.Sub(p.at).Milliseconds()
}

// start makes item the current track: from 0, playing, attributed to whoever added it.
func (p *player) start(item queueItem, now time.Time) {
	p.video, p.pos, p.at, p.playing, p.finished = item.Video, 0, now, true, false
	p.seq++
	p.by = item.By
}

// advance starts the head of the queue. The caller checks the queue is not empty.
func (p *player) advance(now time.Time) {
	next := p.queue[0]
	p.queue = slices.Delete(p.queue, 0, 1)
	p.start(next, now)
}

// add makes video current when nothing is, or when the last track finished with an empty
// queue; otherwise it queues it. It returns false, leaving the state untouched, when the
// queue is full.
func (p *player) add(video, by string, now time.Time) bool {
	if p.video == "" || (p.finished && len(p.queue) == 0) {
		p.start(queueItem{Video: video, By: by}, now)
		return true
	}
	if len(p.queue) >= maxMusicQueue {
		return false
	}
	p.nextID++
	p.queue = append(p.queue, queueItem{p.nextID, video, by})
	return true
}

// apply performs c on behalf of by and re-anchors the position at now. It returns false,
// leaving the state untouched, when c is not applicable (CHAT-30).
func (p *player) apply(c musicCmd, by string, now time.Time) bool {
	switch c.action {
	case musicAdd:
		return p.add(c.video, by, now)
	case musicAddMany:
		added := false
		for _, v := range c.videos {
			// The first id may start playback; the rest queue until the cap, and the
			// extras are dropped silently.
			added = p.add(v, by, now) || added
		}
		return added
	case musicRemove:
		i := slices.IndexFunc(p.queue, func(it queueItem) bool { return it.ID == c.id })
		if i < 0 {
			return false
		}
		p.queue = slices.Delete(p.queue, i, i+1)
		return true
	case musicSkip:
		if len(p.queue) == 0 {
			return false
		}
		p.advance(now)
		return true
	case musicEnded:
		if p.video == "" || c.seq != p.seq || p.finished {
			return false
		}
		if len(p.queue) > 0 {
			p.advance(now)
			return true
		}
		p.pos, p.at, p.playing, p.finished = p.position(now), now, false, true
		return true
	}
	if p.video == "" {
		return false
	}
	switch c.action {
	case musicPlay:
		p.pos, p.playing, p.finished = p.position(now), true, false
	case musicPause:
		p.pos, p.playing = p.position(now), false
	case musicSeek:
		p.pos, p.finished = c.pos, false
	default:
		return false
	}
	p.at, p.by = now, by
	return true
}

// halt pauses at the current position when the room empties (CHAT-30). by, the queue,
// seq and finished are unchanged.
func (p *player) halt(now time.Time) {
	if p.playing {
		p.pos, p.at, p.playing = p.position(now), now, false
	}
}

// frame is the state as the `music` frame sent to clients; pos is taken at now. The queue
// is copied (never nil, so JSON is []) because the frame is encoded after later applies.
func (p *player) frame(now time.Time) musicFrame {
	return musicFrame{
		Type:    typeMusic,
		Video:   p.video,
		Playing: p.playing,
		Pos:     p.position(now),
		By:      p.by,
		Seq:     p.seq,
		Queue:   append([]queueItem{}, p.queue...),
		TS:      now.UnixMilli(),
	}
}
