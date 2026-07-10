package merge

import (
	"sync"
	"sync/atomic"

	"go.uber.org/zap"

	"github.com/suoten/jt-engine/internal/util"
)

type EventType string

const (
	EventTypeLocationUpdate EventType = "location_update"
	EventTypeAlarmEvent     EventType = "alarm_event"
	EventTypeAIAlert        EventType = "ai_alert"
	EventTypeSystemEvent    EventType = "system_event"
)

type Event struct {
	Type EventType
	Data interface{}
}

type EventHandler func(event Event)

type SubscriptionID uint64

type subscription struct {
	id      SubscriptionID
	handler EventHandler
}

type EventBus struct {
	mu          sync.RWMutex
	handlers    map[EventType][]*subscription
	logger      *zap.Logger
	nextSubID   atomic.Uint64
	eventBuffer chan Event
	stopCh      chan struct{}
	stopOnce    sync.Once
}

func NewEventBus(logger *zap.Logger) *EventBus {
	eb := &EventBus{
		handlers:    make(map[EventType][]*subscription),
		logger:      logger,
		eventBuffer: make(chan Event, 1024),
		stopCh:      make(chan struct{}),
	}
	util.SafeGo(eb.logger, "merge.eventBus.asyncDispatchLoop", eb.asyncDispatchLoop)
	return eb
}

func (eb *EventBus) Subscribe(eventType EventType, handler EventHandler) SubscriptionID {
	eb.mu.Lock()
	defer eb.mu.Unlock()
	id := SubscriptionID(eb.nextSubID.Add(1))
	sub := &subscription{id: id, handler: handler}
	eb.handlers[eventType] = append(eb.handlers[eventType], sub)
	return id
}

func (eb *EventBus) Unsubscribe(id SubscriptionID) {
	eb.mu.Lock()
	defer eb.mu.Unlock()
	for eventType, subs := range eb.handlers {
		for i, sub := range subs {
			if sub.id == id {
				eb.handlers[eventType] = append(subs[:i], subs[i+1:]...)
				return
			}
		}
	}
}

func (eb *EventBus) Publish(eventType EventType, data interface{}) {
	eb.dispatchEvent(Event{Type: eventType, Data: data})
}

func (eb *EventBus) PublishAsync(eventType EventType, data interface{}) {
	event := Event{Type: eventType, Data: data}
	select {
	case eb.eventBuffer <- event:
	default:
		eb.logger.Warn("event buffer full, dropping event",
			zap.String("type", string(eventType)))
	}
}

func (eb *EventBus) asyncDispatchLoop() {
	for {
		select {
		case event := <-eb.eventBuffer:
			eb.dispatchEvent(event)
		case <-eb.stopCh:
			return
		}
	}
}

func (eb *EventBus) dispatchEvent(event Event) {
	eb.mu.RLock()
	var handlers []EventHandler
	if subs, ok := eb.handlers[event.Type]; ok {
		for _, sub := range subs {
			handlers = append(handlers, sub.handler)
		}
	}
	eb.mu.RUnlock()

	for _, handler := range handlers {
		func() {
			defer func() {
				if r := recover(); r != nil {
					eb.logger.Error("event handler panic",
						zap.String("type", string(event.Type)),
						zap.Any("recover", r))
				}
			}()
			handler(event)
		}()
	}
}

func (eb *EventBus) Stop() {
	eb.stopOnce.Do(func() { close(eb.stopCh) })
}