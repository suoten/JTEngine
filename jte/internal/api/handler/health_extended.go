package handler

// ===================================================================
// AUTO-FIX-2026-07-02 [可观测性完善]: 健康检查端点（等保2.0 + 云原生）
//
// 端点设计（Kubernetes/蓝绿部署兼容）：
//   /health        — 基础健康状态（进程存活 + 版本 + 运行时长）
//   /health/live   — 存活检查（进程存活即 200，不检查依赖）
//   /health/ready  — 就绪检查（依赖服务检查：MySQL/Redis/TDengine/MinIO）
//   /healthz       — 兼容旧端点（等价 /health/live）
//   /readyz        — 兼容旧端点（等价 /health/ready，含维护模式检查）
//
// 依赖检查器（DependencyChecker）：
//   - MySQL/达梦：SELECT 1
//   - Redis：PING
//   - TDengine：SHOW DATABASES
//   - MinIO：ListBuckets
//
// 响应格式：
//   200 OK    {"status":"ok","checks":{"mysql":"ok","redis":"ok",...}}
//   503 Down  {"status":"degraded","checks":{"mysql":"down","redis":"ok",...}}
// ===================================================================

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"runtime"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/suoten/jt-engine/internal/gateway"
	"github.com/suoten/jt-engine/internal/maintenance"
	"go.uber.org/zap"
)

// DependencyChecker 依赖服务健康检查接口
// 每个依赖服务（MySQL/Redis/TDengine/MinIO）实现该接口，
// HealthHandler 在 /health/ready 时并行调用所有检查器。
type DependencyChecker interface {
	// Name 返回依赖服务名（如 "mysql"、"redis"、"tdengine"、"minio"）
	Name() string
	// Check 执行健康检查，返回 nil 表示健康，error 表示不健康
	Check(ctx context.Context) error
}

// ==================== 内置依赖检查器 ====================

// SQLChecker MySQL/达梦/SQLite 健康检查器
type SQLChecker struct {
	name string
	db   *sql.DB
}

func NewSQLChecker(name string, db *sql.DB) *SQLChecker {
	return &SQLChecker{name: name, db: db}
}

func (c *SQLChecker) Name() string { return c.name }
func (c *SQLChecker) Check(ctx context.Context) error {
	if c.db == nil {
		return errDependencyNotConfigured(c.name)
	}
	return c.db.PingContext(ctx)
}

// RedisChecker Redis 健康检查器（通过通用 ping 函数注入，避免硬依赖 redis 客户端）
type RedisChecker struct {
	name string
	ping func(ctx context.Context) error
}

func NewRedisChecker(name string, ping func(ctx context.Context) error) *RedisChecker {
	return &RedisChecker{name: name, ping: ping}
}

func (c *RedisChecker) Name() string { return c.name }
func (c *RedisChecker) Check(ctx context.Context) error {
	if c.ping == nil {
		return errDependencyNotConfigured(c.name)
	}
	return c.ping(ctx)
}

// HTTPChecker 通用 HTTP 健康检查器（MinIO/TDengine REST API 等）
type HTTPChecker struct {
	name string
	url  string
}

func NewHTTPChecker(name, url string) *HTTPChecker {
	return &HTTPChecker{name: name, url: url}
}

func (c *HTTPChecker) Name() string { return c.name }
func (c *HTTPChecker) Check(ctx context.Context) error {
	if c.url == "" {
		return errDependencyNotConfigured(c.name)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 {
		return errHTTPStatus(c.name, resp.StatusCode)
	}
	return nil
}

// errDependencyNotConfigured 依赖未配置错误
func errDependencyNotConfigured(name string) error {
	return &dependencyError{name: name, reason: "not configured"}
}

// errHTTPStatus HTTP 状态码异常
func errHTTPStatus(name string, code int) error {
	return &dependencyError{name: name, reason: "http status " + http.StatusText(code)}
}

type dependencyError struct {
	name   string
	reason string
}

func (e *dependencyError) Error() string { return e.name + ": " + e.reason }

// ZLMediaKitChecker ZLMediaKit 流媒体引擎健康检查器
// FIXED-2026-07-23 [P2]: 健康检查端点增加 ZLMediaKit 连通状态
type ZLMediaKitChecker struct {
	name      string
	connected func() bool
}

// NewZLMediaKitChecker 创建 ZLMediaKit 健康检查器
// connected: 返回 ZLMediaKit 是否连通的函数（通常为 client.IsConnected）
func NewZLMediaKitChecker(name string, connected func() bool) *ZLMediaKitChecker {
	return &ZLMediaKitChecker{name: name, connected: connected}
}

func (c *ZLMediaKitChecker) Name() string { return c.name }
func (c *ZLMediaKitChecker) Check(ctx context.Context) error {
	if c.connected == nil {
		return errDependencyNotConfigured(c.name)
	}
	if !c.connected() {
		return &dependencyError{name: c.name, reason: "not connected"}
	}
	return nil
}

// JT809ClientStatus 809 客户端状态信息接口（避免直接依赖 gateway.JT809Client）
type JT809ClientStatus interface {
	GetPlatformID() string
	IsCircuitOpen() bool
	IsRunning() bool
}

// JT809Checker 809 上级平台连通状态检查器
// FIXED-2026-07-23 [P2]: 健康检查端点增加 809 上级平台连通状态
type JT809Checker struct {
	name    string
	clients []JT809ClientStatus
}

// NewJT809Checker 创建 809 连通状态检查器
// clients: 所有 809 上级平台客户端的状态接口列表
func NewJT809Checker(name string, clients []JT809ClientStatus) *JT809Checker {
	return &JT809Checker{name: name, clients: clients}
}

func (c *JT809Checker) Name() string { return c.name }
func (c *JT809Checker) Check(ctx context.Context) error {
	if len(c.clients) == 0 {
		// 无 809 平台配置时不算故障
		return nil
	}
	var failed []string
	for _, cl := range c.clients {
		if cl == nil {
			continue
		}
		if !cl.IsRunning() {
			failed = append(failed, cl.GetPlatformID()+": disconnected")
		} else if cl.IsCircuitOpen() {
			failed = append(failed, cl.GetPlatformID()+": circuit breaker open")
		}
	}
	if len(failed) > 0 {
		return &dependencyError{name: c.name, reason: fmt.Sprintf("%d/%d platforms unhealthy: %v", len(failed), len(c.clients), failed)}
	}
	return nil
}

// MemoryChecker 内存使用率检查器
// FIXED-2026-07-23 [P2]: 健康检查端点增加内存使用率检查（与 OOM 阈值对比）
type MemoryChecker struct {
	name       string
	warnMB     int // 内存告警阈值（MB）
	criticalMB int // 内存危险阈值（MB）
	fatalMB    int // 内存致命阈值（MB）
}

// NewMemoryChecker 创建内存使用率检查器
func NewMemoryChecker(name string, warnMB, criticalMB, fatalMB int) *MemoryChecker {
	return &MemoryChecker{name: name, warnMB: warnMB, criticalMB: criticalMB, fatalMB: fatalMB}
}

func (c *MemoryChecker) Name() string { return c.name }
func (c *MemoryChecker) Check(ctx context.Context) error {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	memMB := int(ms.Sys / (1024 * 1024))

	if c.fatalMB > 0 && memMB >= c.fatalMB {
		return &dependencyError{name: c.name, reason: fmt.Sprintf("memory usage %dMB >= fatal threshold %dMB", memMB, c.fatalMB)}
	}
	if c.criticalMB > 0 && memMB >= c.criticalMB {
		return &dependencyError{name: c.name, reason: fmt.Sprintf("memory usage %dMB >= critical threshold %dMB", memMB, c.criticalMB)}
	}
	if c.warnMB > 0 && memMB >= c.warnMB {
		return &dependencyError{name: c.name, reason: fmt.Sprintf("memory usage %dMB >= warn threshold %dMB", memMB, c.warnMB)}
	}
	return nil
}

// ==================== 健康检查 Handler ====================

// ExtendedHealthHandler 扩展健康检查处理器
// 提供 /health、/health/live、/health/ready 端点，支持依赖服务检查。
type ExtendedHealthHandler struct {
	*HealthHandler
	checkers      []DependencyChecker
	checkersMu    sync.RWMutex // [R61-P2] 保护 checkers 和 dependencies 的并发访问
	checkTimeout  time.Duration
	startTime     time.Time
	version       string
	logger        *zap.Logger
	dependencies  map[string]bool // 配置的依赖名集合（用于区分"未配置"和"未检查"）
}

// NewExtendedHealthHandler 创建扩展健康检查处理器
// checkers: 依赖服务检查器列表（MySQL/Redis/TDengine/MinIO）
// version: 应用版本号
// dependencies: 已配置的依赖名集合（用于 /health/ready 报告配置状态）
func NewExtendedHealthHandler(
	sessions *gateway.SessionManager,
	mm *maintenance.Mode,
	startTime time.Time,
	version string,
	logger *zap.Logger,
	checkers ...DependencyChecker,
) *ExtendedHealthHandler {
	deps := make(map[string]bool, len(checkers))
	for _, c := range checkers {
		deps[c.Name()] = true
	}
	return &ExtendedHealthHandler{
		HealthHandler: NewHealthHandler(sessions, mm, startTime),
		checkers:      checkers,
		checkTimeout:  3 * time.Second,
		startTime:     startTime,
		version:       version,
		logger:        logger,
		dependencies:  deps,
	}
}

// Health 基础健康状态端点 /health
// 返回进程存活状态 + 版本 + 运行时长 + 连接数 + 内存使用 + 各组件独立状态
// FIXED-2026-07-23 [P2]: 加固健康检查端点，返回结构化 JSON，各组件状态独立标识
func (h *ExtendedHealthHandler) Health(c *gin.Context) {
	uptime := int64(time.Since(h.startTime).Seconds())
	connections := 0
	if h.sessions != nil {
		connections = h.sessions.OnlineCount()
	}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	memoryMB := ms.Sys / (1024 * 1024)
	goroutines := runtime.NumGoroutine()

	status := "ok"
	httpCode := http.StatusOK
	if h.maintenanceMode != nil && h.maintenanceMode.IsActive() {
		status = "maintenance"
	}

	// FIXED-2026-07-23 [P2]: 并行执行依赖检查，各组件状态独立标识
	checks := h.runChecks(c.Request.Context())

	// 汇总依赖检查状态
	depsHealthy := true
	for _, result := range checks {
		if result["status"] != "ok" && result["status"] != "skipped" {
			depsHealthy = false
			break
		}
	}
	if !depsHealthy && status == "ok" {
		status = "degraded"
	}

	c.JSON(httpCode, gin.H{
		"status":      status,
		"version":     h.version,
		"uptime":      uptime,
		"connections": connections,
		"memory_mb":   memoryMB,
		"goroutines":  goroutines,
		"checks":      checks,
		"timestamp":   time.Now().Format(time.RFC3339),
	})
}

// Live 存活检查端点 /health/live
// 仅检查进程存活（不检查依赖），用于 Kubernetes livenessProbe
func (h *ExtendedHealthHandler) Live(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":    "alive",
		"uptime":    int64(time.Since(h.startTime).Seconds()),
		"timestamp": time.Now().Format(time.RFC3339),
	})
}

// Ready 就绪检查端点 /health/ready
// 检查所有依赖服务健康状态，任一依赖不健康返回 503
// 用于 Kubernetes readinessProbe 和蓝绿部署流量切换
func (h *ExtendedHealthHandler) Ready(c *gin.Context) {
	// 维护模式期间不就绪
	if h.maintenanceMode != nil && h.maintenanceMode.IsActive() {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status":    "not_ready",
			"reason":    "maintenance mode active",
			"timestamp": time.Now().Format(time.RFC3339),
		})
		return
	}

	// 并行执行所有依赖检查
	checks := h.runChecks(c.Request.Context())

	// 汇总状态
	allHealthy := true
	for _, result := range checks {
		if result["status"] != "ok" {
			allHealthy = false
		}
	}

	httpCode := http.StatusOK
	status := "ready"
	if !allHealthy {
		httpCode = http.StatusServiceUnavailable
		status = "degraded"
	}

	c.JSON(httpCode, gin.H{
		"status":    status,
		"checks":    checks,
		"timestamp": time.Now().Format(time.RFC3339),
	})
}

// checkResult 单个依赖检查结果
type checkResult struct {
	name    string
	status  string // "ok" / "down" / "skipped"
	err     error
	latency time.Duration
}

// runChecks 并行执行所有依赖检查，返回结果 map
func (h *ExtendedHealthHandler) runChecks(parent context.Context) map[string]map[string]interface{} {
	// [R61-P2] 在读锁下复制 checkers 切片，避免与 AddChecker 并发时产生数据竞争。
	// 不持锁执行 Check()，以免长时间持锁阻塞 AddChecker。
	h.checkersMu.RLock()
	checkers := make([]DependencyChecker, len(h.checkers))
	copy(checkers, h.checkers)
	h.checkersMu.RUnlock()

	if len(checkers) == 0 {
		return map[string]map[string]interface{}{
			"_summary": {"status": "ok", "message": "no dependencies configured"},
		}
	}

	ctx, cancel := context.WithTimeout(parent, h.checkTimeout)
	defer cancel()

	var wg sync.WaitGroup
	results := make([]checkResult, len(checkers))

	for i, checker := range checkers {
		wg.Add(1)
		// FIXED: [P1] 依赖检查 goroutine 缺少 recover()，panic 会扩散至整个进程 [2026-07-17]
		go func(idx int, ck DependencyChecker) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					results[idx] = checkResult{
						name:   ck.Name(),
						status: "down",
						err:    fmt.Errorf("health check panic: %v", r),
					}
				}
			}()
			start := time.Now()
			err := ck.Check(ctx)
			latency := time.Since(start)

			result := checkResult{
				name:    ck.Name(),
				latency: latency,
			}
			if err != nil {
				result.status = "down"
				result.err = err
			} else {
				result.status = "ok"
			}
			results[idx] = result
		}(i, checker)
	}
	wg.Wait()

	// 转为 map
	checks := make(map[string]map[string]interface{}, len(results))
	for _, r := range results {
		entry := map[string]interface{}{
			"status":  r.status,
			"latency": r.latency.String(),
		}
		if r.err != nil {
			entry["error"] = r.err.Error()
		}
		checks[r.name] = entry
	}
	return checks
}

// AddChecker 动态添加依赖检查器（供 SetStorageLayers 后注入 TDengine/MinIO 检查器）
// AddChecker 动态添加依赖检查器（供 SetStorageLayers 后注入 TDengine/MinIO 检查器）
// [R61-P2] 加锁保护，防止与 runChecks 并发执行时产生数据竞争。
func (h *ExtendedHealthHandler) AddChecker(checker DependencyChecker) {
	if checker == nil {
		return
	}
	h.checkersMu.Lock()
	defer h.checkersMu.Unlock()
	h.checkers = append(h.checkers, checker)
	h.dependencies[checker.Name()] = true
}

// SetCheckTimeout 设置依赖检查超时时间（默认 3 秒）
func (h *ExtendedHealthHandler) SetCheckTimeout(d time.Duration) {
	if d > 0 {
		h.checkTimeout = d
	}
}
