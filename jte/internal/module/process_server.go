// FIXED: [P1] process_server.go accept 循环和 ServeConn goroutine 缺少 recover()，panic 会崩溃整个模块进程 [2026-07-17]
package module

// ===================================================================
// AUTO-FIX-2026-06-30 [集成-5]: 进程模式服务端辅助
//
// 模块作者使用 ServeProcess() 将模块以独立进程模式运行：
//
//   // cmd/module-storage/main.go
//   func main() {
//       mod := storage.NewModule()
//       if err := module.ServeProcess(mod); err != nil {
//           log.Fatal(err)
//       }
//   }
//
// ServeProcess 会：
//   1. 读取 JTE_MODULE_SOCKET 环境变量获取监听地址
//   2. 注册 ModuleRPC service，监听 Unix socket
//   3. 输出 "READY\n" 通知宿主进程
//   4. 阻塞直到子进程被终止
// ===================================================================

import (
	"fmt"
	"net"
	"net/rpc"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

// ModuleRPCServer RPC 服务端，包装 Module 接口。
// 通过 net/rpc 暴露 Module 的方法供 ProcessModule 调用。
type ModuleRPCServer struct {
	mod Module
}

// Info 返回模块名称和版本。
func (s *ModuleRPCServer) Info(args rpcModuleArgs, reply *rpcInfoResult) error {
	reply.Name = s.mod.Name()
	reply.Version = s.mod.Version()
	return nil
}

// Init 初始化模块。
// 注意：进程模式下 app 参数无法跨进程传递，子进程应通过环境变量/配置文件获取依赖。
// args.App 为 nil，子进程需自行从 JTE_MODULE_CONFIG 环境变量读取配置路径。
func (s *ModuleRPCServer) Init(args rpcModuleArgs, reply *rpcModuleResult) error {
	if err := s.mod.Init(nil); err != nil {
		reply.Error = err.Error()
	}
	return nil
}

// Start 启动模块。
func (s *ModuleRPCServer) Start(args rpcModuleArgs, reply *rpcModuleResult) error {
	if err := s.mod.Start(); err != nil {
		reply.Error = err.Error()
	}
	return nil
}

// Stop 停止模块。
func (s *ModuleRPCServer) Stop(args rpcModuleArgs, reply *rpcModuleResult) error {
	if err := s.mod.Stop(); err != nil {
		reply.Error = err.Error()
	}
	return nil
}

// Health 健康检查（若模块实现 HealthModule）。
func (s *ModuleRPCServer) Health(args rpcModuleArgs, reply *rpcHealthResult) error {
	if hm, ok := s.mod.(HealthModule); ok {
		if err := hm.Health(); err != nil {
			reply.Healthy = false
			reply.Error = err.Error()
		} else {
			reply.Healthy = true
		}
	} else {
		// 未实现 HealthModule 视为健康
		reply.Healthy = true
	}
	return nil
}

// ServeProcess 以进程模式运行模块。
// 模块二进制的 main() 应调用此函数。
// 返回时子进程退出。
// FIXED: [P1-4] 原实现收到 SIGTERM 后直接返回，未等待活跃 ServeConn goroutine 完成，
// 正在执行的 RPC 调用（如存储写入）被截断可能导致数据损坏。
// 改为：关闭 listener → 停止模块 → 等待 ServeConn 退出（带超时）→ 返回
func ServeProcess(mod Module) error {
	socketPath := os.Getenv("JTE_MODULE_SOCKET")
	if socketPath == "" {
		return fmt.Errorf("JTE_MODULE_SOCKET not set (not running in process mode)")
	}

	// 清理旧 socket 文件
	_ = os.Remove(socketPath)

	// 监听 Unix socket
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("listen %s: %w", socketPath, err)
	}
	defer listener.Close()

	// 注册 RPC service
	server := rpc.NewServer()
	if err := server.RegisterName("ModuleRPC", &ModuleRPCServer{mod: mod}); err != nil {
		return fmt.Errorf("register RPC: %w", err)
	}

	// 通知宿主进程就绪
	fmt.Fprintln(os.Stdout, "READY")

	// FIXED: [P1-4] 使用 WaitGroup 跟踪活跃连接，优雅停机时等待
	var connWg sync.WaitGroup

	// 接受连接（每个连接一个 goroutine）
	acceptDone := make(chan struct{})
	go func() {
		defer func() {
			if r := recover(); r != nil {
				// FIXED: [P1] accept 循环 panic recovery，防进程崩溃 [2026-07-17]
				fmt.Fprintf(os.Stderr, "process_server accept loop panic: %v\n", r)
			}
			close(acceptDone)
		}()
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			connWg.Add(1)
			go func(c net.Conn) {
				defer connWg.Done()
				defer func() {
					if r := recover(); r != nil {
						// FIXED: [P1] ServeConn panic recovery，单个连接 panic 不影响其他连接 [2026-07-17]
						fmt.Fprintf(os.Stderr, "process_server ServeConn panic: %v\n", r)
					}
				}()
				server.ServeConn(c)
			}(conn)
		}
	}()

	// 等待终止信号
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	<-sigCh

	// 1. 关闭 listener（拒绝新连接）
	listener.Close()
	<-acceptDone

	// 2. 优雅停止模块（完成在途 RPC 逻辑）
	_ = mod.Stop()

	// 3. 等待所有 ServeConn goroutine 退出（带超时，避免无限阻塞）
	waitDone := make(chan struct{})
	go func() {
		connWg.Wait()
		close(waitDone)
	}()
	select {
	case <-waitDone:
		// 所有连接已优雅退出
	case <-time.After(10 * time.Second):
		fmt.Fprintln(os.Stderr, "process_server: timeout waiting for connections to drain")
	}

	return nil
}
