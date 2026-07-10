package websocket

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type Client struct {
	id      string
	hub     *Hub
	conn    *websocket.Conn
	send    chan *Message
	topics  map[string]bool
	mu      sync.Mutex
}

func NewClient(id string, hub *Hub, conn *websocket.Conn) *Client {
	return &Client{
		id:     id,
		hub:    hub,
		conn:   conn,
		send:   make(chan *Message, 256),
		topics: make(map[string]bool),
	}
}

func (c *Client) WritePump() {
	defer func() {
		c.conn.Close()
	}()

	for msg := range c.send {
		c.mu.Lock()
		data, err := json.Marshal(msg)
		c.mu.Unlock()
		if err != nil {
			continue
		}

		c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if err := c.conn.WriteMessage(websocket.TextMessage, data); err != nil {
			return
		}
	}
}

func (c *Client) ReadPump() {
	defer func() {
		c.hub.unregister <- c
		c.conn.Close()
	}()

	c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			break
		}

		var cmd struct {
			Action string `json:"action"`
			Topic  string `json:"topic"`
		}
		if err := json.Unmarshal(message, &cmd); err != nil {
			continue
		}

		switch cmd.Action {
		case "subscribe":
			c.hub.Subscribe(c, cmd.Topic)
		case "unsubscribe":
			c.hub.Unsubscribe(c, cmd.Topic)
		}
	}
}