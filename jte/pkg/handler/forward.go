package handler

import (
	"github.com/jte-engine/jte/pkg/storage"
)

// ForwardChecker 转发规则检查器接口（AUTO-FIX-2026-07-02 [P1]）。
//
// 用于协议扩展模块（如 module-protocol-809）在发布级联转发事件前，
// 检查持久化转发规则（按车辆/消息类型/时间段/源平台过滤）。
//
// 设计原因：协议扩展模块（github.com/jte-engine/module-protocol-809）不能
// import internal/gateway（会导致循环依赖），通过此接口解耦。网关层提供适配器，
// 内部委托给 JT809Client.shouldForward 进行实际规则匹配。
//
// 此接口定义在 pkg/handler 共享包中，使协议模块和组合根（cmd/jte）都能访问，
// 无需协议模块与主模块之间建立编译期依赖。
//
// nil 语义：未注入时协议模块回退到"始终发布转发事件"的旧行为，保证向后兼容。
type ForwardChecker interface {
	// ShouldForward 判断指定数据是否应转发到上级平台。
	//   dataType: "location" | "alarm" | "video"
	//   phone: 车辆手机号/车牌号
	//   sourcePlatformID: 源下级平台 ID（空=本平台直连终端）
	//   alarm: 报警数据（dataType="alarm" 时用于类型/级别过滤，其他类型为 nil）
	ShouldForward(dataType, phone, sourcePlatformID string, alarm *storage.AlarmData) bool
}

// ForwardCheckerSetter 由需要接收 ForwardChecker 的协议 Handler 实现。
// 组合根（cmd/jte）通过类型断言检查 Handler 是否实现此接口，若实现则注入适配器。
// 这样协议模块无需暴露具体类型即可完成注入，保持模块独立性。
type ForwardCheckerSetter interface {
	SetForwardChecker(fc ForwardChecker)
}
