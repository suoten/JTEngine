package main

import (
	"github.com/suoten/jt-engine/internal/gateway"
	"github.com/suoten/jt-engine/pkg/handler"
	"github.com/suoten/jt-engine/pkg/storage"
)

// forwardCheckerAdapter  JT809Client  ShouldForward Ϊ handler.ForwardChecker ӿڡ
// AUTO-FIX-2026-07-02 [P1]: 809 Эģ鲻 import internal/gatewayѭ
// ͨϸע롣ۺϼƽ̨ͻˣ
// ֻҪһϼƽ̨תƥ伴 trueOR 壩
//
// ˵
//   - ת0x1401809 Handler  alarm.SourcePlatformID  MergeAlarm
//     MergeAlarm  EventTypeAlarmEventÿϼͻ˵ StartAutoForward 
//     Ե shouldForward˴ļ 809 Handler ǰ۲־
//   - Ƶת0x1B00809 Handler ǷӦ av_forward ϵͳ¼
//     ֻҪһϼƽ̨¼Ƶϵͳ RTP ת
type forwardCheckerAdapter struct {
	clients []*gateway.JT809Client
}

// newForwardCheckerAdapter ת
func newForwardCheckerAdapter(clients []*gateway.JT809Client) *forwardCheckerAdapter {
	return &forwardCheckerAdapter{clients: clients}
}

// ShouldForward ʵ handler.ForwardChecker ӿڡ
// ϼƽ̨ͻˣһƥ伴 true
// ޿ͻʱ falseϼƽ̨ת
func (a *forwardCheckerAdapter) ShouldForward(dataType, phone, sourcePlatformID string, alarm *storage.AlarmData) bool {
	for _, c := range a.clients {
		if c.ShouldForward(dataType, phone, sourcePlatformID, alarm) {
			return true
		}
	}
	return false
}

// compile-time interface assertion
var _ handler.ForwardChecker = (*forwardCheckerAdapter)(nil)
