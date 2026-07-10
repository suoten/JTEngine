package handler

import (
	"fmt"
	"sync"

	"github.com/suoten/jt-engine/pkg/protocol"
	"go.uber.org/zap"
)

type Session interface {
	GetID() string
	GetPhone() string
	GetProtocol() protocol.ProtocolType
	UpdateActivity()
	SetProtocol(pt protocol.ProtocolType)
	Write(data []byte) (int, error)
}

type ProtocolHandler interface {
	ProtocolType() protocol.ProtocolType
	HandleMessage(session Session, msg *protocol.Message, hub *protocol.Hub) error
}

type HandlerRegistry struct {
	mu       sync.RWMutex
	handlers map[protocol.ProtocolType]ProtocolHandler
	logger   *zap.Logger
}

func NewHandlerRegistry() *HandlerRegistry {
	return &HandlerRegistry{
		handlers: make(map[protocol.ProtocolType]ProtocolHandler),
		logger:   zap.NewNop(),
	}
}

func (r *HandlerRegistry) SetLogger(logger *zap.Logger) {
	r.logger = logger
}

func (r *HandlerRegistry) Register(handler ProtocolHandler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.handlers[handler.ProtocolType()]; exists {
		r.logger.Warn("handler registry: overwriting existing handler",
			zap.String("protocol", string(handler.ProtocolType())))
	}
	r.handlers[handler.ProtocolType()] = handler
}

func (r *HandlerRegistry) Get(pt protocol.ProtocolType) (ProtocolHandler, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	h, ok := r.handlers[pt]
	return h, ok
}

func (r *HandlerRegistry) ListHandlers() []protocol.ProtocolType {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]protocol.ProtocolType, 0, len(r.handlers))
	for pt := range r.handlers {
		result = append(result, pt)
	}
	return result
}

type ErrHandlerNotRegistered struct {
	Protocol protocol.ProtocolType
}

func (e *ErrHandlerNotRegistered) Error() string {
	return fmt.Sprintf("protocol handler not registered for %s, install the corresponding module-protocol-*", e.Protocol)
}