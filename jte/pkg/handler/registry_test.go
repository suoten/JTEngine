package handler

import (
	"testing"

	"github.com/jte-engine/jte/pkg/protocol"
)

func TestHandlerRegistry_RegisterAndGet(t *testing.T) {
	registry := NewHandlerRegistry()

	mockHandler := &mockProtocolHandler{protoType: protocol.ProtocolJT809}
	registry.Register(mockHandler)

	got, ok := registry.Get(protocol.ProtocolJT809)
	if !ok {
		t.Fatal("expected to find handler for JT809")
	}
	if got.ProtocolType() != protocol.ProtocolJT809 {
		t.Errorf("ProtocolType() = %v, want %v", got.ProtocolType(), protocol.ProtocolJT809)
	}
}

func TestHandlerRegistry_GetNotFound(t *testing.T) {
	registry := NewHandlerRegistry()

	_, ok := registry.Get(protocol.ProtocolJT1045)
	if ok {
		t.Fatal("expected not to find handler for JT1045")
	}
}

func TestHandlerRegistry_ListHandlers(t *testing.T) {
	registry := NewHandlerRegistry()

	if len(registry.ListHandlers()) != 0 {
		t.Fatal("expected empty handlers list")
	}

	registry.Register(&mockProtocolHandler{protoType: protocol.ProtocolJT809})
	registry.Register(&mockProtocolHandler{protoType: protocol.ProtocolJT1045})

	handlers := registry.ListHandlers()
	if len(handlers) != 2 {
		t.Fatalf("expected 2 handlers, got %d", len(handlers))
	}
}

type mockProtocolHandler struct {
	protoType protocol.ProtocolType
}

func (h *mockProtocolHandler) ProtocolType() protocol.ProtocolType {
	return h.protoType
}

func (h *mockProtocolHandler) HandleMessage(session Session, msg *protocol.Message, hub *protocol.Hub) error {
	return nil
}
