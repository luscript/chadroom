package chat

import (
	"encoding/json"
	"log"
	"strings"
)

const (
	typeChat    = "chat"
	typeJoin    = "join"
	typeLeave   = "leave"
	typeWelcome = "welcome"
	typeUsers   = "users"
	typeHistory = "history"
	typeDM      = "dm"
	typeDMError = "dm_error"
	typeTyping  = "typing"
	typeMusic   = "music"
	historySize = 50 // chat messages kept for newcomers

	reasonInvalid = "invalid"
	reasonSelf    = "self"
	reasonOffline = "offline"
)

// message is the outbound JSON envelope. Field order is the JSON order. type, from,
// text and ts are always present; avatar only when the sender has one (welcome, join,
// chat and history entries, never leave), so frames without a picture are unchanged
// (CHAT-28). users and history frames have their own shapes below.
type message struct {
	Type   string `json:"type"` // chat | join | leave | welcome
	From   string `json:"from"`
	Text   string `json:"text"` // "" for everything but chat
	TS     int64  `json:"ts"`   // unix milliseconds
	Avatar string `json:"avatar,omitempty"`
}

// usersFrame is the private snapshot of connected nicknames sent on join.
type usersFrame struct {
	Type    string            `json:"type"`              // always "users"
	Users   []string          `json:"users"`             // never nil: [] when empty (unreachable: includes the newcomer)
	Avatars map[string]string `json:"avatars,omitempty"` // nick -> url, only members with one (CHAT-28)
	TS      int64             `json:"ts"`
}

// historyFrame is the private replay of recent chat messages sent on join.
type historyFrame struct {
	Type     string    `json:"type"`     // always "history"
	Messages []message `json:"messages"` // oldest first, never nil: [] when empty
	TS       int64     `json:"ts"`
}

// dmFrame is a direct message, sent to the recipient and echoed to the sender.
type dmFrame struct {
	Type string `json:"type"` // always "dm"
	From string `json:"from"` // sender's assigned nickname
	To   string `json:"to"`   // recipient's assigned nickname
	Text string `json:"text"`
	TS   int64  `json:"ts"` // same in both copies
}

// dmErrorFrame tells only the sender why a direct message was not delivered.
type dmErrorFrame struct {
	Type   string `json:"type"`   // always "dm_error"
	To     string `json:"to"`     // cleaned requested recipient; "" for invalid
	Reason string `json:"reason"` // invalid | self | offline
	TS     int64  `json:"ts"`
}

// typingFrame tells room members that from is typing (CHAT-26). Never stored.
type typingFrame struct {
	Type string `json:"type"` // always "typing"
	From string `json:"from"`
	TS   int64  `json:"ts"`
}

// typingDMFrame tells only the recipient that from is typing to them (CHAT-26).
type typingDMFrame struct {
	Type string `json:"type"` // always "typing"
	From string `json:"from"`
	To   string `json:"to"` // recipient's assigned nickname
	TS   int64  `json:"ts"`
}

// musicFrame is a room's shared player state (CHAT-30). pos is computed when the frame is
// built, so receivers treat it as "now" and server/client clock skew doesn't matter. Never stored.
type musicFrame struct {
	Type    string `json:"type"` // always "music"
	Video   string `json:"video"`
	Playing bool   `json:"playing"`
	Pos     int64  `json:"pos"` // ms
	By      string `json:"by"`
	TS      int64  `json:"ts"`
}

type inboundKind uint8

const (
	inboundChat   inboundKind = iota // zero value: inbound{from, text} literals stay room chat
	inboundDM                        // to and text set
	inboundBadDM                     // dm envelope with a bad to/text; answered with dm_error invalid
	inboundTyping                    // typing signal; to == "" is a room signal
	inboundMusic                     // validated music control (CHAT-30)
)

// inbound is a classified client text frame on its way to the hub. It travels on
// Room.broadcast whatever its kind, so one client's frames keep their order.
type inbound struct {
	from  *Client
	kind  inboundKind
	to    string // inboundDM: cleanNickname(to), never "". inboundTyping: "" for a room signal
	text  string
	music musicCmd // inboundMusic only
}

// parseInbound classifies a size- and UTF-8-checked text frame (CHAT-21). ok is
// false when the frame must be discarded silently (CHAT-13). from is left for the caller.
func parseInbound(frame []byte) (in inbound, ok bool) {
	// Exact keys: a struct would match "TYPE" case-insensitively.
	var env map[string]json.RawMessage
	isObject := json.Unmarshal(frame, &env) == nil && env != nil
	if isObject {
		typ, _ := jsonString(env["type"])
		text, textOK := jsonString(env["text"])
		switch typ {
		case typeDM:
			to, toOK := jsonString(env["to"])
			to = cleanNickname(to)
			if !toOK || !textOK || to == "" {
				return inbound{kind: inboundBadDM}, true
			}
			if strings.TrimSpace(text) == "" {
				return inbound{}, false
			}
			return inbound{kind: inboundDM, to: to, text: text}, true
		case typeTyping:
			// CHAT-21 rule 2: text is ignored and the blank rule does not apply.
			raw, hasTo := env["to"]
			if !hasTo {
				return inbound{kind: inboundTyping}, true // room signal
			}
			to, toOK := jsonString(raw)
			to = cleanNickname(to)
			return inbound{kind: inboundTyping, to: to}, toOK && to != "" // invalid: silent discard
		case typeMusic:
			// CHAT-21 rule 2a: validated completely here, and an invalid control is
			// discarded instead of falling back to chat.
			cmd, valid := parseMusic(env)
			if !valid {
				return inbound{}, false
			}
			return inbound{kind: inboundMusic, music: cmd}, true
		case typeChat:
			// A non-string text intentionally falls through to the plain-text rule (CHAT-21).
			if textOK {
				return inbound{text: text}, strings.TrimSpace(text) != ""
			}
		}
	}
	whole := string(frame)
	return inbound{text: whole}, strings.TrimSpace(whole) != ""
}

// parseMusic validates a music control's fields (CHAT-30). Whether a video is loaded is
// the hub's business.
func parseMusic(env map[string]json.RawMessage) (musicCmd, bool) {
	action, _ := jsonString(env["action"])
	switch action {
	case musicLoad:
		video, ok := jsonString(env["video"])
		return musicCmd{action: action, video: video}, ok && videoID.MatchString(video)
	case musicPlay, musicPause:
		return musicCmd{action: action}, true
	case musicSeek:
		pos, ok := jsonMillis(env["pos"])
		return musicCmd{action: action, pos: pos}, ok && pos >= 0 && pos <= maxMusicPos
	}
	return musicCmd{}, false
}

// jsonMillis decodes a JSON integer. "null" (which Unmarshal would read as 0), strings,
// booleans, objects, fractions and exponents are all rejected.
func jsonMillis(raw json.RawMessage) (int64, bool) {
	if len(raw) == 0 || (raw[0] != '-' && (raw[0] < '0' || raw[0] > '9')) {
		return 0, false
	}
	var n int64
	err := json.Unmarshal(raw, &n)
	return n, err == nil
}

// jsonString reports whether raw is a JSON string and returns its value ("null" is not a string).
func jsonString(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 || raw[0] != '"' {
		return "", false
	}
	var s string
	err := json.Unmarshal(raw, &s)
	return s, err == nil
}

// encode marshals v. It returns nil on error (unreachable for strings and an
// int64); callers skip a nil payload.
func encode(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		log.Println("Encode error:", err)
		return nil
	}
	return b
}
