// Package util 提供跨模块通用工具。当前核心是 SafeGo——为后台 goroutine 提供 panic 兜底，
// 避免单个连接/单条消息处理 panic 导致整个 JTE 进程崩溃（车联网网关 7x24 可用性硬要求）。
package util

import (
	"runtime/debug"

	"go.uber.org/zap"
)

// SafeGo 启动一个带 panic 兜底的 goroutine。
// 任一后台 goroutine panic 时，捕获 panic、记录堆栈与 reason，避免进程崩溃。
// reason 用于在日志中标识该 goroutine 的职责，便于运维定位。
//
// 用法：
//
//	util.SafeGo(logger, "gateway.acceptLoop", func() { s.acceptLoop() })
//
// 注意：SafeGo 仅防进程崩溃，不掩盖 bug——panic 必然记录 Error 级日志与完整堆栈。
func SafeGo(logger *zap.Logger, reason string, fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				stack := debug.Stack()
				if logger != nil {
					logger.Error("goroutine panic recovered",
						zap.String("reason", reason),
						zap.Any("panic", r),
						zap.ByteString("stack", stack))
				}
			}
		}()
		fn()
	}()
}

// SafeGoWithRecover 启动带 panic 兜底的 goroutine，并在 panic 时执行自定义 onPanic 回调。
// 适用于需要在 panic 后做资源清理（如关闭连接、从索引移除）的场景。
func SafeGoWithRecover(logger *zap.Logger, reason string, onPanic func(r interface{}), fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				stack := debug.Stack()
				if logger != nil {
					logger.Error("goroutine panic recovered",
						zap.String("reason", reason),
						zap.Any("panic", r),
						zap.ByteString("stack", stack))
				}
				if onPanic != nil {
					// onPanic 自身不应再 panic；用二级 recover 兜底防止清理逻辑崩溃
					func() {
						defer func() { _ = recover() }()
						onPanic(r)
					}()
				}
			}
		}()
		fn()
	}()
}
