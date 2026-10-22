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
)

// message is the outbound JSON envelope. Field order is the JSON order and
// there is no omitempty: every frame carries all four fields.
type message struct {
	Type string `json:"type"` // chat | join | leave | welcome
	From string `json:"from"`
	Text string `json:"text"` // "" for everything but chat
	TS   int64  `json:"ts"`   // unix milliseconds
}

// inbound is a validated client text frame on its way to the hub.
type inbound struct {
	from *Client
	text string
}

// encode marshals m. It returns nil on error (unreachable for strings and an
// int64); callers skip a nil payload.
func encode(m message) []byte {
	b, err := json.Marshal(m)
	if err != nil {
		log.Println("Encode error:", err)
		return nil
	}
	return b
}
