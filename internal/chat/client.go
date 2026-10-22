package chat

import (
	"errors"
	"log"
	"net"
	"time"
	"unicode/utf8"

	"github.com/gorilla/websocket"
)

// Client is one connected websocket peer. send is closed only by Room.Run;
// conn is nil in the hub unit tests, which never start the pumps.
type Client struct {
	conn     *websocket.Conn
	send     chan []byte
	nickname string
}

// logError logs abnormal connection errors with the nickname, skipping the
// expected ways a connection ends (normal close, local close).
func (c *Client) logError(op string, err error) {
	if errors.Is(err, net.ErrClosed) || errors.Is(err, websocket.ErrCloseSent) ||
		websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway, websocket.CloseNoStatusReceived) {
		return
	}
	log.Printf("%s error (%q): %v", op, c.nickname, err)
}

// writePump sends messages and keepalive pings to the client.
// It is the only goroutine that calls WriteMessage on conn.
func (c *Client) writePump(cr *Room) {
	ticker := time.NewTicker(cr.pingPeriod)
	defer ticker.Stop()
	defer c.conn.Close()
	for {
		select {
		case message, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(cr.writeWait)) // only fails on a dead conn; the write below reports it
			if !ok {
				_ = c.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")) // best-effort goodbye
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, message); err != nil {
				c.logError("Write", err)
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(cr.writeWait)) // only fails on a dead conn; the ping below reports it
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				c.logError("Ping", err)
				return
			}
		}
	}
}

// closeWith sends a close control frame with the given code. WriteControl is
// safe to call concurrently with writePump's WriteMessage.
func (c *Client) closeWith(cr *Room, code int) {
	msg := websocket.FormatCloseMessage(code, "")
	_ = c.conn.WriteControl(websocket.CloseMessage, msg, time.Now().Add(cr.writeWait)) // best-effort; peer may be gone
}

// readPump processes incoming messages
func (c *Client) readPump(cr *Room) {
	defer func() {
		select {
		case cr.unregister <- c:
		case <-cr.done:
		}
		c.conn.Close()
	}()
	c.conn.SetReadLimit(cr.maxMessageSize)
	_ = c.conn.SetReadDeadline(time.Now().Add(cr.pongWait)) // only fails on a dead conn; ReadMessage reports it
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(cr.pongWait))
	})
	for {
		mt, message, err := c.conn.ReadMessage()
		if err != nil {
			c.logError("Read", err)
			return
		}
		// Any received frame proves the peer is alive (CHAT-07).
		_ = c.conn.SetReadDeadline(time.Now().Add(cr.pongWait)) // only fails on a dead conn; next read reports it
		if mt != websocket.TextMessage {
			c.closeWith(cr, websocket.CloseUnsupportedData)
			return
		}
		if !utf8.Valid(message) {
			c.closeWith(cr, websocket.CloseInvalidFramePayloadData)
			return
		}
		select {
		case cr.broadcast <- []byte(c.nickname + ": " + string(message)):
		case <-cr.done:
			return
		}
	}
}
