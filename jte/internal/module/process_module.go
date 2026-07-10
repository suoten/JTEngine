package module

// ===================================================================
// AUTO-FIX-2026-06-30 [集成-5]: 进程模式模块实现
//
// ProcessModule 实现 Module 接口，将所有调用代理到独立子进程：
//   1. Loader 启动模块二进制（exec.Command）
//   2. 子进程监听 Unix socket（Linux）或 TCP localhost（Windows）
//   3. ProcessModule 通过 net/rpc 调用子进程的 Module 方法
//   4. 子进程崩溃 → RPC 调用失败 → supervisor 重启
//
// 子进程协议（环境变量约定）：
//   JTE_MODULE_SOCKET=<socket path>   子进程监听地址
//   JTE_MODULE_NAME=<module name>     模块名（子进程用于日志）
//   子进程启动后向 stdout 输出 "READY\n" 表示就绪
// ===================================================================

import (
	"errors"
	"fmt"
	"net/rpc"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"go.uber.org/zap"
)

// rpcModuleArgs RPC 调用参数/返回值（net/rpc 要求可序列化）
type rpcModuleArgs struct {
	App interface{} // Init 参数（进程模式下序列化受限，实际传递配置路径）
}

type rpcModuleResult struct {
	Error string
}

type rpcInfoResult struct {
	Name    string
	Version string
	Error   string
}

type rpcHealthResult struct {
	Healthy bool
	Error   string
}

// ProcessModule 进程模式模块实现。
// 实现 Module 接口 + HealthModule 接口，所有调用通过 RPC 代理到子进程。
type ProcessModule struct {
	mu sync.Mutex

	name       string
	binaryPath string
	socketPath string
	config     LoadModeConfig
	logger     *zap.Logger

	cmd    *exec.Cmd
	client *rpc.Client

	startTime time.Time
}

// NewProcessModule 创建进程模式模块。
func NewProcessModule(name, binaryPath string, cfg LoadModeConfig, logger *zap.Logger) *ProcessModule {
	socketName := fmt.Sprintf("jte-module-%s-%d.sock", name, time.Now().UnixNano())
	return &ProcessModule{
		name:       name,
		binaryPath: binaryPath,
		socketPath: filepath.Join(cfg.SocketDir, socketName),
		config:     cfg,
		logger:     logger,
	}
}

func (pm *ProcessModule) Name() string    { return pm.name }
func (pm *ProcessModule) Version() string { return "(process mode)" }

// Init 启动子进程并建立 RPC 连接。
func (pm *ProcessModule) Init(app interface{}) error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if pm.cmd != nil {
		return fmt.Errorf("module %s process already started", pm.name)
	}

	// 检查二进制是否存在
	if _, err := os.Stat(pm.binaryPath); err != nil {
		return fmt.Errorf("module binary not found: %s (%w)", pm.binaryPath, err)
	}

	// 准备 socket 路径（清理旧文件）
	_ = os.Remove(pm.socketPath)

	// 启动子进程
	cmd := exec.Command(pm.binaryPath)
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("JTE_MODULE_SOCKET=%s", pm.socketPath),
		fmt.Sprintf("JTE_MODULE_NAME=%s", pm.name),
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start module process %s: %w", pm.name, err)
	}
	pm.cmd = cmd

	// 等待子进程就绪（连接 RPC server）
	timeout := time.Duration(pm.config.StartTimeoutSec) * time.Second
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	deadline := time.Now().Add(timeout)

	var client *rpc.Client
	var lastErr error
	for time.Now().Before(deadline) {
		// 检查子进程是否已退出
		if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
			pm.cmd = nil
			return fmt.Errorf("module %s process exited prematurely (code=%d)",
				pm.name, cmd.ProcessState.ExitCode())
		}

		client, lastErr = rpc.Dial("unix", pm.socketPath)
		if lastErr == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if client == nil {
		// 连接失败，杀死子进程
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		pm.cmd = nil
		return fmt.Errorf("module %s RPC connect timeout: %w", pm.name, lastErr)
	}

	pm.client = client
	pm.startTime = time.Now()

	// 获取模块信息（验证 RPC 通路）
	var info rpcInfoResult
	if err := client.Call("ModuleRPC.Info", rpcModuleArgs{}, &info); err != nil {
		pm.cleanup()
		return fmt.Errorf("module %s RPC Info failed: %w", pm.name, err)
	}
	if info.Error != "" {
		pm.cleanup()
		return fmt.Errorf("module %s Info error: %s", pm.name, info.Error)
	}

	if pm.logger != nil {
		pm.logger.Info("module process started",
			zap.String("name", pm.name),
			zap.String("binary", pm.binaryPath),
			zap.String("socket", pm.socketPath),
			zap.Int("pid", cmd.Process.Pid))
	}

	return nil
}

// Start 通过 RPC 调用子进程的 Start 方法。
func (pm *ProcessModule) Start() error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if pm.client == nil {
		return fmt.Errorf("module %s not initialized", pm.name)
	}

	var result rpcModuleResult
	if err := pm.client.Call("ModuleRPC.Start", rpcModuleArgs{}, &result); err != nil {
		return fmt.Errorf("module %s RPC Start: %w", pm.name, err)
	}
	if result.Error != "" {
		return fmt.Errorf("module %s Start: %s", pm.name, result.Error)
	}
	return nil
}

// Stop 通过 RPC 调用子进程的 Stop 方法，然后终止子进程。
func (pm *ProcessModule) Stop() error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if pm.client == nil {
		return nil
	}

	// 优雅停止：先 RPC 调用 Stop
	stopTimeout := time.Duration(pm.config.StopTimeoutSec) * time.Second
	if stopTimeout == 0 {
		stopTimeout = 5 * time.Second
	}
	done := make(chan error, 1)
	go func() {
		var result rpcModuleResult
		err := pm.client.Call("ModuleRPC.Stop", rpcModuleArgs{}, &result)
		if err == nil && result.Error != "" {
			err = errors.New(result.Error)
		}
		done <- err
	}()

	select {
	case <-time.After(stopTimeout):
		// RPC 超时，强制杀死
	case <-done:
		// RPC 完成
	}

	// 关闭 RPC client
	_ = pm.client.Close()
	pm.client = nil

	// 终止子进程
	if pm.cmd != nil && pm.cmd.Process != nil {
		_ = pm.cmd.Process.Kill()
		_, _ = pm.cmd.Process.Wait()
		pm.cmd = nil
	}

	// 清理 socket 文件
	_ = os.Remove(pm.socketPath)

	if pm.logger != nil {
		pm.logger.Info("module process stopped", zap.String("name", pm.name))
	}

	return nil
}

// Health 通过 RPC 调用子进程的健康检查（实现 HealthModule）。
func (pm *ProcessModule) Health() error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if pm.client == nil {
		return fmt.Errorf("module %s RPC client not connected", pm.name)
	}

	var result rpcHealthResult
	// 设置短超时避免长时间阻塞
	call := pm.client.Go("ModuleRPC.Health", rpcModuleArgs{}, &result, nil)
	select {
	case <-time.After(5 * time.Second):
		return fmt.Errorf("module %s health check timeout", pm.name)
	case reply := <-call.Done:
		if reply.Error != nil {
			return fmt.Errorf("module %s health RPC: %w", pm.name, reply.Error)
		}
		if !result.Healthy {
			return fmt.Errorf("module %s unhealthy: %s", pm.name, result.Error)
		}
		return nil
	}
}

// IsAlive 检查子进程是否存活。
func (pm *ProcessModule) IsAlive() bool {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	return pm.cmd != nil && pm.cmd.ProcessState == nil
}

// cleanup 清理 RPC 连接和子进程（内部调用，需持锁）。
func (pm *ProcessModule) cleanup() {
	if pm.client != nil {
		_ = pm.client.Close()
		pm.client = nil
	}
	if pm.cmd != nil && pm.cmd.Process != nil {
		_ = pm.cmd.Process.Kill()
		_, _ = pm.cmd.Process.Wait()
		pm.cmd = nil
	}
	_ = os.Remove(pm.socketPath)
}

// discoverModuleBinaries 在指定目录查找模块二进制文件。
// 约定：二进制文件名 = 模块名（如 module-storage）。
// Windows 平台自动追加 .exe 后缀。
func discoverModuleBinaries(binDir string) (map[string]string, error) {
	entries, err := os.ReadDir(binDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	modules := make(map[string]string)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		// Windows: 去除 .exe 后缀作为模块名
		if runtime.GOOS == "windows" {
			if ext := filepath.Ext(name); ext == ".exe" {
				modName := name[:len(name)-len(ext)]
				modules[modName] = filepath.Join(binDir, name)
			}
		} else {
			// Linux/macOS: 跳过 .so（plugin 模式用），仅加载可执行文件
			if ext := filepath.Ext(name); ext == "" || ext == ".bin" {
				modName := name
				if ext == ".bin" {
					modName = name[:len(name)-len(ext)]
				}
				// 验证可执行权限
				info, err := entry.Info()
				if err != nil {
					continue
				}
				if info.Mode()&0o111 != 0 {
					modules[modName] = filepath.Join(binDir, name)
				}
			}
		}
	}
	return modules, nil
}
