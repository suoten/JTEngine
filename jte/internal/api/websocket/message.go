package websocket

import "time"

type Message struct {
	Topic string      `json:"topic"`
	Type  string      `json:"type"`
	Data  interface{} `json:"data"`
	Time  time.Time   `json:"time"`
}

func NewMessage(topic, msgType string, data interface{}) *Message {
	return &Message{
		Topic: topic,
		Type:  msgType,
		Data:  data,
		Time:  time.Now(),
	}
}