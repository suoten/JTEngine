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
	"syscall"
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

	// 接受连接（每个连接一个 goroutine）
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go server.ServeConn(conn)
		}
	}()

	// 等待终止信号
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	<-sigCh

	// 优雅停止模块
	_ = mod.Stop()
	return nil
}
