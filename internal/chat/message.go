package chat

import (
	"encoding/json"
	"log"
)

const (
	typeChat    = "chat"
	typeJoin    = "join"
	typeLeave   = "leave"
	typeWelcome = "welcome"
	typeUsers   = "users"
	typeHistory = "history"
	historySize = 50 // chat messages kept for newcomers
)

// message is the outbound JSON envelope. Field order is the JSON order and
// there is no omitempty: every chat/join/leave/welcome frame carries all four
// fields. users and history frames have their own shapes below.
type message struct {
	Type string `json:"type"` // chat | join | leave | welcome
	From string `json:"from"`
	Text string `json:"text"` // "" for everything but chat
	TS   int64  `json:"ts"`   // unix milliseconds
}

// usersFrame is the private snapshot of connected nicknames sent on join.
type usersFrame struct {
	Type  string   `json:"type"`  // always "users"
	Users []string `json:"users"` // never nil: [] when empty (unreachable: includes the newcomer)
	TS    int64    `json:"ts"`
}

// historyFrame is the private replay of recent chat messages sent on join.
type historyFrame struct {
	Type     string    `json:"type"`     // always "history"
	Messages []message `json:"messages"` // oldest first, never nil: [] when empty
	TS       int64     `json:"ts"`
}

// inbound is a validated client text frame on its way to the hub.
type inbound struct {
	from *Client
	text string
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
