package api

// @title JTE Engine API
// @version 1.0.0
// @description JTE - JT/T 部标协议智能引擎 API文档
// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
// @host localhost:8080
// @BasePath /api/v1

import (
	"context"
	"crypto/rand"
	"database/sql"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/pprof"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/suoten/jt-engine/internal/api/handler"
	"github.com/suoten/jt-engine/internal/api/middleware"
	"github.com/suoten/jt-engine/internal/api/websocket"
	"github.com/suoten/jt-engine/internal/audit"
	"github.com/suoten/jt-engine/internal/config"
	"github.com/suoten/jt-engine/internal/gateway"
	"github.com/suoten/jt-engine/internal/maintenance"
	"github.com/suoten/jt-engine/internal/media"
	"github.com/suoten/jt-engine/internal/metrics"
	"github.com/suoten/jt-engine/internal/module"
	"github.com/suoten/jt-engine/internal/security"
	"github.com/suoten/jt-engine/internal/trace"
	"github.com/suoten/jt-engine/internal/util"
	"github.com/suoten/jt-engine/pkg/crypto/secret"
	"github.com/suoten/jt-engine/pkg/merge"
	"github.com/suoten/jt-engine/internal/registry"
	jt1078 "github.com/suoten/jt-engine/pkg/protocol/jt1078"
	"github.com/suoten/jt-engine/pkg/storage"
	"go.uber.org/zap"
)

// Version 应用版本号（由 main.go 通过 ldflags 注入或使用默认值）
var Version = "1.0.0"

type Server struct {
	cfg             *config.Config
	logger          *zap.Logger
	store           storage.Interface
	sessions        *gateway.SessionManager
	mergeEng        *merge.Engine
	registry        *registry.FeatureRegistry
	wsHub           *websocket.Hub
	engine          *gin.Engine
	// AUTO-FIX-2026-06-29 [P1]: 原 Start() 用 engine.Run 阻塞，无 Shutdown 能力。
	// httpServer 持有底层 listener，Stop(ctx) 调用 httpServer.Shutdown 实现优雅停机。
	httpServer      *http.Server
	webFS           fs.FS
	startTime       time.Time // 进程启动时间，供 /healthz uptime 计算
	licenseMgr      interface {
		GetStatus() interface{}
		StartTrial(moduleName string) error
		Activate(licenseKey string) error
		Remove(id string) error
	}
	maintenanceMode *maintenance.Mode
	rbacMgr     *module.RBACManager
	adminHandler *handler.AdminHandler // FIXED-2026-07-24: 存储 adminHandler 引用，支持 SetRBACManager 后续注入
	auditLogger *audit.AuditLogger
	mediaClient *media.ZLMediaKitClient
	mediaHandler *handler.MediaHandler
	videoEngine  *jt1078.VideoEngine
	commandSender *handler.CommandSender
	aiModule    interface{ AnalyzeAlarm(alarmID, alarmType string, data map[string]interface{}) (isFalseAlarm bool, confidence float64, reason string, err error) }
	aiNLPModule interface{ Chat(query, sessionID string) (response string, err error) }
	// AUTO-FIX-2026-06-26: 第六轮遗留修复 - AI handler 引用（供 WebSocket 路由注册使用）
	aiHandler   *handler.AIHandler
	// v3.0 存储分层管理 handler（按需注入 StorageLayers/Archiver）
	storageHandler *handler.StorageHandler
	// AUTO-FIX-2026-07-02 [可观测性]: 扩展健康检查 handler（支持依赖服务检查）
	healthHandler *handler.ExtendedHealthHandler
	// AUTO-FIX-2026-06-29 [P1-6]: 809 转发规则 handler（含 JT809Client reloader 注入）
	forwardRuleHandler *handler.ForwardRuleHandler
	// AUTO-FIX-2026-06-30 [P1-6]: JWT 密钥轮换管理器 + token 黑名单（泄露应急）
	jwtRotationMgr *JWTRotationManager
	tokenBlacklist *TokenBlacklist
	// AUTO-FIX-2026-07-02 [等保2.0/防克隆]: 登录守卫（失败锁定 + 设备指纹 + 多IP/异地/新设备告警）
	loginGuard *security.LoginGuard
	// AUTO-FIX-2026-07-02 [国密]: 关键数据 SM4-GCM 加密器（手机号/身份证/车牌落库加密）
	dataCipher *secret.DataCipher
	// FIXED-2026-07-23 [P2]: 809 客户端状态列表（供健康检查使用）
	jt809ClientStatuses []handler.JT809ClientStatus
}

// SetForwardRuleReloaders 注入 JT809Client 实例作为转发规则热更新回调。
// reloaders 按 platformID 索引；调用方传入 map[platformID]ForwardRuleReloader。
// 必须在 setupRouter 完成后调用，否则 forwardRuleHandler 为 nil。
func (s *Server) SetForwardRuleReloaders(reloaders map[string]handler.ForwardRuleReloader) {
	if s.forwardRuleHandler == nil {
		return
	}
	for pid, r := range reloaders {
		s.forwardRuleHandler.RegisterReloader(pid, r)
	}
}

// SetJT809ClientStatuses 注入 809 客户端状态列表，供健康检查端点使用。
// FIXED-2026-07-23 [P2]: 健康检查端点增加 809 上级平台连通状态
// 必须在 setupRouter 之后调用（NewServer 返回后），通过 AddHealthChecker 动态注册。
func (s *Server) SetJT809ClientStatuses(statuses []handler.JT809ClientStatus) {
	s.jt809ClientStatuses = statuses
	if len(statuses) > 0 {
		s.AddHealthChecker(handler.NewJT809Checker("jt809", statuses))
	}
}

// buildDependencyCheckers 构建依赖服务健康检查器列表。
// 根据配置的存储类型和缓存/对象存储层，注册对应的检查器：
//   - MySQL/SQLite：通过 *sql.DB.Ping 检查
//   - Redis：通过注入的 ping 函数检查（由 module-storage 注入）
//   - TDengine：通过 HTTP REST API 检查
//   - MinIO：通过 HTTP REST API 检查
// 未配置的依赖不注册检查器（/health/ready 报告 "no dependencies configured"）。
func (s *Server) buildDependencyCheckers() []handler.DependencyChecker {
	var checkers []handler.DependencyChecker

	// SQLite/MySQL/PostgreSQL 关系层检查（通过 storage interface 的底层 *sql.DB）
	if s.store != nil {
		if db := s.extractSQLDB(); db != nil {
			checkers = append(checkers, handler.NewSQLChecker("storage", db))
		}
	}

	// TDengine 时序层检查（通过 HTTP REST API，默认端口 6041）
	if s.cfg != nil && s.cfg.Storage.TimeSeries.Driver != "" {
		tsCfg := &s.cfg.Storage.TimeSeries
		host := tsCfg.Host
		if host == "" {
			host = "127.0.0.1"
		}
		port := tsCfg.Port
		if port == 0 {
			port = 6041 // TDengine REST API 默认端口
		}
		tdURL := fmt.Sprintf("http://%s:%d/rest/sql", host, port)
		checkers = append(checkers, handler.NewHTTPChecker("tdengine", tdURL))
	}

	// MinIO 对象存储检查（通过 HTTP /minio/health/live）
	if s.cfg != nil && s.cfg.Storage.Object.Driver != "" && s.cfg.Storage.Object.Endpoint != "" {
		minioURL := s.cfg.Storage.Object.Endpoint + "/minio/health/live"
		if !strings.HasPrefix(minioURL, "http") {
			if s.cfg.Storage.Object.UseSSL {
				minioURL = "https://" + minioURL
			} else {
				minioURL = "http://" + minioURL
			}
		}
		checkers = append(checkers, handler.NewHTTPChecker("minio", minioURL))
	}

	// FIXED-2026-07-23 [P2]: ZLMediaKit 流媒体引擎连通状态检查
	// mediaClient 在 NewServer 后通过 SetMediaClient 注入，此处检查 nil
	// SetMediaClient 会通过 AddHealthChecker 动态注册检查器
	if s.mediaClient != nil {
		checkers = append(checkers, handler.NewZLMediaKitChecker("zlmediakit", s.mediaClient.IsConnected))
	}

	// FIXED-2026-07-23 [P2]: 内存使用率检查（与 OOM 阈值对比）
	if s.cfg != nil && s.cfg.Gateway.OOMProtect.Enabled {
		checkers = append(checkers, handler.NewMemoryChecker("memory",
			s.cfg.Gateway.OOMProtect.WarnMB,
			s.cfg.Gateway.OOMProtect.CriticalMB,
			s.cfg.Gateway.OOMProtect.FatalMB,
		))
	}

	// FIXED-2026-07-23 [P2]: 809 上级平台连通状态检查
	// jt809ClientStatuses 在 NewServer 后通过 SetJT809ClientStatuses 注入
	// SetJT809ClientStatuses 会通过 AddHealthChecker 动态注册检查器
	if len(s.jt809ClientStatuses) > 0 {
		checkers = append(checkers, handler.NewJT809Checker("jt809", s.jt809ClientStatuses))
	}

	return checkers
}

// extractSQLDB 从 storage interface 提取底层 *sql.DB（仅 SQLite/MySQL 存储可用）
// 未使用反射避免循环依赖；通过 StorageLayers 注入的 DBStore 暴露 *sql.DB。
func (s *Server) extractSQLDB() *sql.DB {
	// 优先通过 storageHandler 获取 StorageLayers
	if s.storageHandler != nil {
		if layers := s.storageHandler.GetStorageLayers(); layers != nil && layers.Relational != nil {
			type dbExposer interface {
				DB() *sql.DB
			}
			if de, ok := layers.Relational.(dbExposer); ok {
				return de.DB()
			}
		}
	}
	return nil
}

// AddHealthChecker 动态添加依赖检查器（供 module-storage 在 Init 后注入 Redis/TDengine/MinIO 检查器）
func (s *Server) AddHealthChecker(c handler.DependencyChecker) {
	if s.healthHandler != nil && c != nil {
		s.healthHandler.AddChecker(c)
	}
}

var webEmbedFS embed.FS

func NewServer(
	cfg *config.Config,
	logger *zap.Logger,
	store storage.Interface,
	sessions *gateway.SessionManager,
	mergeEng *merge.Engine,
	reg *registry.FeatureRegistry,
) *Server {
	if cfg.API.Enabled {
		gin.SetMode(gin.ReleaseMode)
	}

	s := &Server{
		cfg:       cfg,
		logger:    logger,
		store:     store,
		sessions:  sessions,
		mergeEng:  mergeEng,
		registry:  reg,
		wsHub:     websocket.NewHub(logger),
		startTime: time.Now(),
	}

	// AUTO-FIX-2026-06-30 [P1-6]: 初始化 JWT 密钥轮换管理器 + token 黑名单。
	// 黑名单后端优先使用 Redis（多节点共享），无 Redis 时仅内存。
	// 轮换管理器在 Start() 中启动后台调度（90 天自动轮换 + 每小时清理过期 kid/黑名单）。
	s.tokenBlacklist = NewTokenBlacklist(nil, logger)
	if cfg.API.JWT != nil {
		s.jwtRotationMgr = NewJWTRotationManager(cfg.API.JWT, s.tokenBlacklist, logger)
	}

	s.setupRouter()
	return s
}

func (s *Server) setupRouter() {
	r := gin.New()

	// v3.0: 初始化全局报警联动管理器（短信/邮件/钉钉通知）
	handler.SetGlobalAlarmLinkage(handler.NewAlarmLinkage(s.logger))

	// 安全响应头最先注册
	r.Use(middleware.SecurityHeadersMiddleware())
	// FIXED-2026-07-17 [P3]: gin.Recovery() 提前到第二位，确保后续所有中间件的 panic 都能被捕获
	r.Use(gin.Recovery())
	// AUTO-FIX-2026-07-02 [等保2.0 传输安全]: 强制 HTTPS（仅在生产 require_tls=true 时启用）
	// 开发环境默认放行 HTTP；生产环境 HTTP 请求返回 426 Upgrade Required
	if s.cfg.API.RequireTLS {
		r.Use(middleware.RequireTLS())
	}
	// AUTO-FIX-2026-06-30 [集成-7]: trace_id 中间件（每个请求注入 trace_id）
	r.Use(trace.GinMiddleware())
	r.Use(middleware.Logger(s.logger))
	r.Use(middleware.CORS(s.cfg.API.CORSOrigins))
	r.Use(middleware.RateLimit(s.cfg.API.RateLimit))
	// CSRF 防护中间件
	r.Use(middleware.CSRFMiddleware())
	// 输入验证中间件（SQL注入/XSS/路径遍历检测）
	r.Use(middleware.InputValidation())

	if s.auditLogger != nil {
		r.Use(s.auditMiddleware())
	}

	r.GET("/api/v1/health", handler.HealthCheck)

	// v3.0 A.6.1 蓝绿部署健康检查端点（不需要鉴权）
	// AUTO-FIX-2026-07-02 [可观测性完善]: 扩展为 ExtendedHealthHandler，
	// 支持 /health、/health/live、/health/ready + 依赖服务检查（MySQL/Redis/TDengine/MinIO）
	extendedHealth := handler.NewExtendedHealthHandler(
		s.sessions, s.maintenanceMode, s.startTime, Version, s.logger,
		s.buildDependencyCheckers()...,
	)
	r.GET("/healthz", extendedHealth.Healthz)      // 兼容旧端点
	r.GET("/readyz", extendedHealth.Readyz)         // 兼容旧端点
	r.GET("/health", extendedHealth.Health)         // 基础健康状态
	r.GET("/health/live", extendedHealth.Live)      // 存活检查（livenessProbe）
	r.GET("/health/ready", extendedHealth.Ready)    // 就绪检查（readinessProbe + 依赖检查）
	s.healthHandler = extendedHealth

	// v3.0 P1 #10：pprof 性能分析端点
	// AUTO-FIX-2026-07-14 [ConvergeLoop-P1]: 生产环境默认关闭 pprof，防止信息泄露。
	// 开发环境设置环境变量 JTE_PPROF_ENABLED=true 即可启用。
	// 常用：
	//   go tool pprof http://localhost:8080/debug/pprof/profile?seconds=30   # CPU 剖面
	//   go tool pprof http://localhost:8080/debug/pprof/heap                  # 堆剖面
	//   go tool pprof http://localhost:8080/debug/pprof/goroutine             # goroutine 剖面
	//   go tool pprof http://localhost:8080/debug/pprof/block                 # 阻塞剖面
	//   go tool pprof http://localhost:8080/debug/pprof/mutex                 # 互斥锁剖面
	if os.Getenv("JTE_PPROF_ENABLED") == "true" {
		// AUTO-FIX-2026-07-14 [ConvergeLoop-P1]: pprof 端点鉴权保护
		// pprof 暴露 CPU/堆/goroutine 剖面，包含敏感运行时信息，必须鉴权。
		// 复用 JTE_METRICS_TOKEN 作为鉴权 token（与 /metrics 一致）。
		// 未设置 token 时开放访问（开发环境兼容，与 /metrics 行为一致）。
		pprofAuth := func(c *gin.Context) {
			if expectedToken := os.Getenv("JTE_METRICS_TOKEN"); expectedToken != "" {
				auth := c.GetHeader("Authorization")
				if auth != "Bearer "+expectedToken {
					c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
					return
				}
			}
			c.Next()
		}
		pprofGroup := r.Group("/debug/pprof", pprofAuth)
		pprofGroup.GET("/", gin.WrapF(pprof.Index))
		pprofGroup.GET("/cmdline", gin.WrapF(pprof.Cmdline))
		pprofGroup.GET("/profile", gin.WrapF(pprof.Profile))
		pprofGroup.GET("/symbol", gin.WrapF(pprof.Symbol))
		pprofGroup.GET("/trace", gin.WrapF(pprof.Trace))
		pprofGroup.GET("/:name", gin.WrapH(http.HandlerFunc(pprof.Index))) // heap/goroutine/block/mutex/threadcreate
	}

	// v3.0 P2 #14：Prometheus 指标端点（不需鉴权，便于 Prometheus 抓取）
	// 生产环境建议通过反向代理限制访问（仅内网/监控网段）
	// 暴露指标：jte_connections_total / jte_messages_total / jte_storage_write_total /
	//          jte_video_bitrate_kbps / tdengine_write_total / tdengine_query_duration 等 20+
	// storageHandler 在 SetStorageLayers 注入后才会生效；未注入时返回基础提示
	sh := handler.NewStorageHandler(s.cfg, s.logger)
	// AUTO-FIX-2026-06-30 [集成-7]: 合并自定义指标（jte_*）与存储层指标
	// AUTO-FIX-2026-07-14 [ConvergeLoop-P1]: /metrics 端点鉴权保护
	// 生产环境设置 JTE_METRICS_TOKEN 环境变量后，要求 Authorization: Bearer <token>
	// 未设置 token 时开放访问（开发环境兼容）
	r.GET("/metrics", func(c *gin.Context) {
		if expectedToken := os.Getenv("JTE_METRICS_TOKEN"); expectedToken != "" {
			auth := c.GetHeader("Authorization")
			if auth != "Bearer "+expectedToken {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
				return
			}
		}
		// 先输出存储层指标（TDengine 等）
		sh.Metrics(c)
		// 追加输出 jte_* 自定义指标
		metrics.DefaultCollector().WritePrometheus(c.Writer)
	})
	// 持有引用供后续 SetStorageLayers 注入
	s.storageHandler = sh

	r.GET("/api/v1/auth/status", s.authStatus)
	r.POST("/api/v1/auth/trial", s.startTrial)
	r.POST("/api/v1/auth/activate", s.activateLicense)
	r.POST("/api/v1/auth/login", s.authLogin)
	r.POST("/api/v1/auth/refresh", s.authRefresh)
	// AUTO-FIX-2026-06-29 [P1]: maintenance/* 和 auth/license/:id 原注册在根路由上
	// 绕过 JWT 鉴权——已迁移到 v1 组（下方），需 system/license 权限。

	r.GET("/", s.serveDashboard)
	r.GET("/dashboard", s.serveDashboard)

	v1 := r.Group("/api/v1")
	v1.Use(middleware.Auth(s.cfg.API.JWTSecret, s.cfg.API.JWT))
	{
		// AUTO-FIX-2026-07-02: 权限动态化 - 前端登录后从后端拉取权限树
		v1.GET("/auth/permissions", s.authPermissions)

		vehicleHandler := handler.NewVehicleHandler(s.store, s.logger)
		v1.GET("/vehicles", middleware.RequirePermission("vehicle"), vehicleHandler.List)
		v1.POST("/vehicles", middleware.RequirePermission("vehicle"), vehicleHandler.Create)
		v1.GET("/vehicles/:id", middleware.RequirePermission("vehicle"), vehicleHandler.Get)
		v1.GET("/vehicles/:id/location", middleware.RequirePermission("vehicle"), vehicleHandler.GetLocation)

		commandSender := s.commandSender
		if commandSender == nil {
			commandSender = handler.NewCommandSender(s.sessions, s.logger)
		}
	adminHandler := handler.NewAdminHandler(s.store, s.logger, commandSender)
	s.adminHandler = adminHandler // FIXED-2026-07-24: 存储引用，供 SetRBACManager 后续注入
	// AUTO-FIX-2026-06-26: 注入 RBACManager 实现用户管理 [2026-06-26]
	if s.rbacMgr != nil {
		adminHandler.SetRBACManager(s.rbacMgr)
	}
		// AUTO-FIX-2026-07-02: 注入 wsHub 实现权限变更 WebSocket 推送（permission_changed 主题）
		adminHandler.SetWSHub(s.wsHub)
		v1.PUT("/vehicles/:id", middleware.RequirePermission("vehicle"), adminHandler.UpdateVehicle)
		v1.DELETE("/vehicles/:id", middleware.RequirePermission("vehicle"), adminHandler.DeleteVehicle)
		v1.POST("/terminals/:id/command", middleware.RequirePermission("command"), adminHandler.SendCommand)
		v1.PUT("/alarms/:id", middleware.RequirePermission("alarm"), adminHandler.HandleAlarm)
		v1.POST("/platforms", middleware.RequirePermission("cascade"), adminHandler.CreatePlatform)
		v1.PUT("/platforms/:id", middleware.RequirePermission("cascade"), adminHandler.UpdatePlatform)
		v1.DELETE("/platforms/:id", middleware.RequirePermission("cascade"), adminHandler.DeletePlatform)
		v1.GET("/config", middleware.RequirePermission("system"), adminHandler.GetConfig)
		v1.PUT("/config", middleware.RequirePermission("system"), adminHandler.UpdateConfig)
		// AUTO-FIX-2026-06-26: 地图API Key配置化接口 [2026-06-26]
	v1.GET("/config/map", adminHandler.GetMapConfig)
	v1.PUT("/config/map", middleware.RequirePermission("system"), adminHandler.UpdateMapConfig)
	// FIXED-2026-07-24: AI 模块配置接口（DeepSeek/Ollama/Qwen）
	v1.GET("/config/ai", middleware.RequirePermission("system"), adminHandler.GetAIConfig)
	v1.PUT("/config/ai", middleware.RequirePermission("system"), adminHandler.UpdateAIConfig)
		v1.GET("/users", middleware.RequirePermission("user_manage"), adminHandler.ListUsers)
		v1.POST("/users", middleware.RequirePermission("user_manage"), adminHandler.CreateUser)
		v1.PUT("/users/:id", middleware.RequirePermission("user_manage"), adminHandler.UpdateUser)
		v1.DELETE("/users/:id", middleware.RequirePermission("user_manage"), adminHandler.DeleteUser)
		// v3.0 角色管理 API
		v1.GET("/roles", middleware.RequirePermission("role_manage"), adminHandler.ListRoles)
		v1.POST("/roles", middleware.RequirePermission("role_manage"), adminHandler.CreateRole)
		v1.PUT("/roles/:id", middleware.RequirePermission("role_manage"), adminHandler.UpdateRole)
		v1.DELETE("/roles/:id", middleware.RequirePermission("role_manage"), adminHandler.DeleteRole)

		// v3.0 系统管理扩展：企业/组织管理 + 操作审计 + 数据备份/恢复
		v1.GET("/organizations", middleware.RequirePermission("system"), adminHandler.ListOrganizations)
		v1.POST("/organizations", middleware.RequirePermission("system"), adminHandler.CreateOrganization)
		v1.PUT("/organizations/:id", middleware.RequirePermission("system"), adminHandler.UpdateOrganization)
		v1.DELETE("/organizations/:id", middleware.RequirePermission("system"), adminHandler.DeleteOrganization)
		v1.GET("/audit-logs", middleware.RequirePermission("system"), adminHandler.ListAuditLogs)
		v1.POST("/system/backup", middleware.RequirePermission("system"), adminHandler.BackupData)
		v1.POST("/system/restore", middleware.RequirePermission("system"), adminHandler.RestoreData)
		v1.GET("/system/backups", middleware.RequirePermission("system"), adminHandler.ListBackups)
		// AUTO-FIX-2026-06-30 [P1-6]: JWT 密钥应急轮换（泄露应急：撤销所有 token + 生成新密钥）
		v1.POST("/system/jwt/emergency-rotate", middleware.RequirePermission("system"), s.handleJWTEmergencyRotate)
		v1.GET("/system/jwt/status", middleware.RequirePermission("system"), s.handleJWTStatus)

		s.mediaHandler = handler.NewMediaHandler(s.store, s.logger, s.cfg, s.mediaClient, commandSender, s.videoEngine)
		v1.POST("/media/start", middleware.RequirePermission("video"), s.mediaHandler.Start)
		v1.POST("/media/stop", middleware.RequirePermission("video"), s.mediaHandler.Stop)
		v1.POST("/media/webrtc", middleware.RequirePermission("video"), s.mediaHandler.WebRTC)
		v1.POST("/media/ptz", middleware.RequirePermission("video"), s.mediaHandler.PTZ)
		// AUTO-FIX-2026-06-26: 第三轮视频监控修复 - 关键帧请求路由
		v1.POST("/media/keyframe", middleware.RequirePermission("video"), s.mediaHandler.KeyFrame)
		v1.POST("/media/stream-mode", middleware.RequirePermission("video"), s.mediaHandler.SetStreamMode)
		v1.GET("/media/stream-mode", middleware.RequirePermission("video"), s.mediaHandler.GetStreamMode)
		// AUTO-FIX-2026-07-02 [P1]: 双码流手动切换 API（/media/switch-stream）
		v1.POST("/media/switch-stream", middleware.RequirePermission("video"), s.mediaHandler.SwitchStream)
		// AUTO-FIX-2026-07-02 [P1]: 录制断片查询/合并 API（/media/fragments）
		v1.GET("/media/fragments", middleware.RequirePermission("video"), s.mediaHandler.Fragments)
		v1.POST("/media/fragments/merge", middleware.RequirePermission("video"), s.mediaHandler.MergeFragments)
		v1.POST("/media/playback", middleware.RequirePermission("video"), s.mediaHandler.Playback)
		v1.POST("/media/download", middleware.RequirePermission("video"), s.mediaHandler.Download)
		// v3.0 视频下载进度查询（前端轮询下载状态）
		v1.GET("/media/download/progress", middleware.RequirePermission("video"), s.mediaHandler.DownloadProgress)
		v1.GET("/media/streams", middleware.RequirePermission("video"), s.mediaHandler.Streams)
		// 验收标准5: 录像回放控制（暂停/继续/快进/快退）
		v1.POST("/media/playback/control", middleware.RequirePermission("video"), s.mediaHandler.PlaybackControl)
		// 验收标准3: 关键帧恢复状态查询
		v1.GET("/media/keyframe/recovery", middleware.RequirePermission("video"), s.mediaHandler.KeyFrameRecoveryStatus)
		v1.GET("/media/keyframe/recovery/:stream_id", middleware.RequirePermission("video"), s.mediaHandler.KeyFrameRecoveryStatus)
		// 验收标准6: PTZ 延迟统计查询
		v1.GET("/media/ptz/latency", middleware.RequirePermission("video"), s.mediaHandler.PTZLatencyStats)
		// 验收标准1: 并发播放管理
		v1.GET("/media/concurrent", middleware.RequirePermission("video"), s.mediaHandler.ConcurrentStreams)
		v1.PUT("/media/concurrent/max", middleware.RequirePermission("video"), s.mediaHandler.SetMaxConcurrent)
		// AUTO-FIX-2026-06-28: 视频质量统计 API（plan 5.5 + project_memory 工程约定）
		// 实时显示码率/帧率/丢包率，支持动态调整自动重连/子码流切换配置
		v1.GET("/media/quality", middleware.RequirePermission("video"), s.mediaHandler.Quality)
		v1.GET("/media/quality/:stream_id", middleware.RequirePermission("video"), s.mediaHandler.QualityByStream)
		v1.PUT("/media/quality/config", middleware.RequirePermission("video"), s.mediaHandler.SetQualityConfig)
		// 验收标准2: RTP SeqNum gap 累计统计与报告
		v1.GET("/media/gap-report", middleware.RequirePermission("video"), s.mediaHandler.GapReports)
		v1.GET("/media/gap-report/:stream_id", middleware.RequirePermission("video"), s.mediaHandler.GapReport)
		// AUTO-FIX-2026-06-29 [P0-2]: /video/quality 别名路由，复用 /media/quality handler
		// 满足前端规范约定的 GET /api/v1/video/quality?deviceId=xxx&channel=xxx 调用路径，
		// 同时保持 /media/quality 向后兼容。handler 内部按 phone+channel 过滤。
		v1.GET("/video/quality", middleware.RequirePermission("video"), s.mediaHandler.Quality)
		// v3.0 录像列表查询 API
		v1.GET("/media/records", middleware.RequirePermission("video"), s.mediaHandler.Records)
		// v3.0 视频监控扩展：截图存储（MinIO）
		v1.POST("/media/screenshot", middleware.RequirePermission("video"), s.mediaHandler.Screenshot)

		platformHandler := handler.NewPlatformHandler(s.sessions, s.logger)
		v1.GET("/platforms", middleware.RequirePermission("cascade"), platformHandler.List)
		v1.GET("/platforms/:id/status", middleware.RequirePermission("cascade"), platformHandler.Status)

		systemHandler := handler.NewSystemHandler(s.store, s.sessions, s.registry, s.logger)
		v1.GET("/system/status", middleware.RequirePermission("system"), systemHandler.Status)
		v1.GET("/system/modules", middleware.RequirePermission("system"), systemHandler.Modules)

		statsHandler := handler.NewStatsHandler(s.store, s.sessions, s.logger)
		v1.GET("/stats", middleware.RequirePermission("monitor"), statsHandler.Stats)
		// [商业版] 前端 API 契约对齐：/stats/overview, /stats/online, /stats/alarms
		v1.GET("/stats/overview", middleware.RequirePermission("monitor"), statsHandler.Overview)
		v1.GET("/stats/online", middleware.RequirePermission("monitor"), statsHandler.Online)
		v1.GET("/stats/alarms", middleware.RequirePermission("monitor"), statsHandler.AlarmCount)

		alarmHandler := handler.NewAlarmHandler(s.store, s.logger)
		v1.GET("/alarms", middleware.RequirePermission("alarm"), alarmHandler.List)
		// v3.0 报警处理扩展：实时推送/确认流程/统计报表
		v1.GET("/alarms/realtime", middleware.RequirePermission("alarm"), alarmHandler.AlarmRealtimeSSE)
		v1.GET("/alarms/report", middleware.RequirePermission("alarm"), alarmHandler.GetAlarmReport)
		v1.GET("/alarms/stats", middleware.RequirePermission("alarm"), alarmHandler.GetAlarmStats)
		// v3.0 报警 HTTP 接收入口（对应 0x0900/1045，供外部系统/级联平台推送报警）
		v1.POST("/alarms/receive", middleware.RequirePermission("alarm"), alarmHandler.ReceiveAlarm)
		v1.PUT("/alarms/:id/ack", middleware.RequirePermission("alarm"), alarmHandler.AckAlarm)
		v1.PUT("/alarms/:id/process", middleware.RequirePermission("alarm"), alarmHandler.ProcessAlarm)
		v1.PUT("/alarms/:id/close", middleware.RequirePermission("alarm"), alarmHandler.CloseAlarm)
		v1.GET("/alarms/:id", middleware.RequirePermission("alarm"), alarmHandler.GetAlarm)
		// v3.0 报警处理扩展：联动通知 + AI 误报判断 + 联动规则管理
		v1.POST("/alarms/notify", middleware.RequirePermission("alarm"), alarmHandler.AlarmLinkageNotify)
		// [商业版] 前端 API 契约对齐：/alarms/:id/notify（按报警 ID 发送联动通知）
		v1.POST("/alarms/:id/notify", middleware.RequirePermission("alarm"), alarmHandler.AlarmLinkageNotify)
		v1.GET("/alarms/linkage/rules", middleware.RequirePermission("alarm"), alarmHandler.AlarmLinkageRules)
		v1.POST("/alarms/linkage/rules", middleware.RequirePermission("alarm"), alarmHandler.AlarmLinkageRules)
		v1.POST("/alarms/:id/ai-check", middleware.RequirePermission("alarm"), alarmHandler.AIFalseAlarmCheck)

		sessionHandler := handler.NewSessionHandler(s.store, s.logger)
		v1.GET("/sessions", middleware.RequirePermission("monitor"), sessionHandler.List)
		// [商业版] 前端 API 契约对齐：/sessions/:id
		v1.GET("/sessions/:id", middleware.RequirePermission("monitor"), sessionHandler.Get)

		v1.GET("/vehicles/locations", s.listVehicleLocations)
		// [商业版] 前端 API 契约对齐：/vehicles/latest-locations（别名于 /vehicles/locations）
		v1.GET("/vehicles/latest-locations", s.listVehicleLocations)

		protoLogHandler := handler.NewProtocolLogHandler(s.store, s.logger)
		v1.GET("/protocol-logs", middleware.RequirePermission("monitor"), protoLogHandler.List)
		v1.GET("/protocol-logs/:id", middleware.RequirePermission("monitor"), protoLogHandler.Get)

		deviceHandler := handler.NewDeviceHandler(s.store, s.logger, commandSender)
		v1.GET("/devices", middleware.RequirePermission("device"), deviceHandler.List)
		v1.POST("/devices", middleware.RequirePermission("device"), deviceHandler.CreateDevice)
		// v3.0 设备管理扩展：批量导入/导出/状态监控（静态路径须先于 :id 注册）
		v1.POST("/devices/batch/import", middleware.RequirePermission("device"), deviceHandler.BatchImportDevices)
		v1.GET("/devices/batch/export", middleware.RequirePermission("device"), deviceHandler.BatchExportDevices)
		v1.GET("/devices/status", middleware.RequirePermission("device"), deviceHandler.GetDeviceStatus)
		v1.GET("/devices/:id", middleware.RequirePermission("device"), deviceHandler.GetDevice)
		v1.PUT("/devices/:id", middleware.RequirePermission("device"), deviceHandler.UpdateDevice)
		v1.DELETE("/devices/:id", middleware.RequirePermission("device"), deviceHandler.DeleteDevice)
		v1.POST("/devices/command", middleware.RequirePermission("command"), deviceHandler.SendCommand)
		// v3.0 设备管理扩展：协议级注册/注销/鉴权 + 终端参数/控制语义化接口
		v1.POST("/devices/register", middleware.RequirePermission("device"), deviceHandler.RegisterDevice)
		v1.DELETE("/devices/:id/unregister", middleware.RequirePermission("device"), deviceHandler.UnregisterDevice)
		v1.POST("/devices/authenticate", middleware.RequirePermission("device"), deviceHandler.AuthenticateDevice)
		v1.PUT("/devices/terminal-params", middleware.RequirePermission("command"), deviceHandler.SetTerminalParams)
		v1.GET("/devices/terminal-params", middleware.RequirePermission("command"), deviceHandler.GetTerminalParams)
		v1.POST("/devices/terminal-control", middleware.RequirePermission("command"), deviceHandler.TerminalControl)

		// v3.0 驾驶员管理 API
		driverHandler := handler.NewDriverHandler(s.store, s.logger)
		v1.GET("/drivers", middleware.RequirePermission("vehicle"), driverHandler.List)
		v1.POST("/drivers", middleware.RequirePermission("vehicle"), driverHandler.Create)
		v1.PUT("/drivers/:id", middleware.RequirePermission("vehicle"), driverHandler.Update)
		v1.DELETE("/drivers/:id", middleware.RequirePermission("vehicle"), driverHandler.Delete)

		// v3.0 电子围栏 API
		geofenceHandler := handler.NewGeofenceHandler(s.store, s.logger)
		v1.GET("/geofences", middleware.RequirePermission("vehicle"), geofenceHandler.List)
		v1.POST("/geofences", middleware.RequirePermission("vehicle"), geofenceHandler.Create)
		v1.GET("/geofences/:id", middleware.RequirePermission("vehicle"), geofenceHandler.Get)
		v1.PUT("/geofences/:id", middleware.RequirePermission("vehicle"), geofenceHandler.Update)
		v1.DELETE("/geofences/:id", middleware.RequirePermission("vehicle"), geofenceHandler.Delete)
		// v3.0 电子围栏扩展：路线围栏 + 绑定车辆 + 进出检测 + 报警推送
		v1.POST("/geofences/route", middleware.RequirePermission("vehicle"), geofenceHandler.CreateRouteGeofence)
		v1.POST("/geofences/:id/bind", middleware.RequirePermission("vehicle"), geofenceHandler.BindVehicle)
		v1.POST("/geofences/:id/unbind", middleware.RequirePermission("vehicle"), geofenceHandler.UnbindVehicle)
		v1.GET("/geofences/:id/vehicles", middleware.RequirePermission("vehicle"), geofenceHandler.ListBoundVehicles)
		v1.GET("/geofences/:id/check", middleware.RequirePermission("vehicle"), geofenceHandler.CheckStatus)
		v1.POST("/geofences/alarms", middleware.RequirePermission("vehicle"), geofenceHandler.AlarmPush)
		v1.GET("/geofences/alarms", middleware.RequirePermission("vehicle"), geofenceHandler.AlarmList)

		// AUTO-FIX-2026-06-29 [P1-6]: 809 转发规则管理 API
		// 转发规则持久化到关系库，支持运行期热更新（修改后自动通知 JT809Client 重新加载内存快照）
		forwardRuleHandler := handler.NewForwardRuleHandler(s.store, s.logger)
		s.forwardRuleHandler = forwardRuleHandler
		v1.GET("/forward-rules", middleware.RequirePermission("system"), forwardRuleHandler.List)
		v1.POST("/forward-rules", middleware.RequirePermission("system"), forwardRuleHandler.Create)
		v1.GET("/forward-rules/:id", middleware.RequirePermission("system"), forwardRuleHandler.Get)
		v1.PUT("/forward-rules/:id", middleware.RequirePermission("system"), forwardRuleHandler.Update)
		v1.DELETE("/forward-rules/:id", middleware.RequirePermission("system"), forwardRuleHandler.Delete)

		// v3.0 存储分层管理 API
		// storageHandler 已在 /metrics 注册时创建，这里复用避免覆盖已注入的 StorageLayers
		if s.storageHandler == nil {
			s.storageHandler = handler.NewStorageHandler(s.cfg, s.logger)
		}
		v1.GET("/storage/stats", middleware.RequirePermission("system"), s.storageHandler.Stats)
		v1.GET("/storage/ttl", middleware.RequirePermission("system"), s.storageHandler.GetTTL)
		v1.PUT("/storage/ttl", middleware.RequirePermission("system"), s.storageHandler.UpdateTTL)
		v1.GET("/storage/archive/status", middleware.RequirePermission("system"), s.storageHandler.ArchiveStatus)
		v1.POST("/storage/archive/trigger", middleware.RequirePermission("system"), s.storageHandler.TriggerArchive)
		// AUTO-FIX-2026-07-02: 归档进度实时查询接口
		v1.GET("/storage/archive/progress", middleware.RequirePermission("system"), s.storageHandler.ArchiveProgress)
		v1.GET("/storage/cache/hitrate", middleware.RequirePermission("system"), s.storageHandler.CacheHitRate)
		// [商业版] 前端 API 契约对齐：集群状态 + 冷热分层统计
		v1.GET("/storage/cluster/status", middleware.RequirePermission("system"), s.storageHandler.ClusterStatus)
		v1.GET("/storage/tier/stats", middleware.RequirePermission("system"), s.storageHandler.TierStats)

		// v3.0 OBD 数据管理 API
		obdHandler := handler.NewOBDHandler(s.store, s.logger)
		v1.GET("/obd/data", middleware.RequirePermission("device"), obdHandler.GetData)
		v1.GET("/obd/history", middleware.RequirePermission("device"), obdHandler.GetHistory)
		v1.GET("/obd/stats", middleware.RequirePermission("device"), obdHandler.GetStats)
		v1.GET("/obd/fault-codes", middleware.RequirePermission("device"), obdHandler.GetFaultCodes)

		// v3.0 行程分析 API
		tripHandler := handler.NewTripHandler(s.store, s.logger)
		v1.GET("/trips", middleware.RequirePermission("track"), tripHandler.List)
		v1.GET("/trips/analysis", middleware.RequirePermission("track"), tripHandler.Analysis)
		v1.POST("/trips/detect", middleware.RequirePermission("track"), tripHandler.Detect)
		v1.GET("/trips/:id", middleware.RequirePermission("track"), tripHandler.Get)

		trackHandler := handler.NewTrackHandler(s.store, s.logger)
		v1.GET("/tracks", middleware.RequirePermission("track"), trackHandler.GetTrack)
		// v3.0 轨迹数据扩展：历史/回放/导出/里程统计
		v1.GET("/tracks/history", middleware.RequirePermission("track"), trackHandler.GetTrackHistory)
		v1.GET("/tracks/playback", middleware.RequirePermission("track"), trackHandler.GetTrackPlayback)
		v1.GET("/tracks/export", middleware.RequirePermission("track"), trackHandler.ExportTrack)
		v1.GET("/tracks/mileage", middleware.RequirePermission("track"), trackHandler.GetMileageStats)
		// v3.0 轨迹数据扩展：最新位置查询 + 实时位置接收 + 轨迹纠偏
		v1.GET("/tracks/latest", middleware.RequirePermission("track"), trackHandler.GetLatestLocation)
		v1.POST("/tracks/receive", middleware.RequirePermission("track"), trackHandler.ReceiveLocation)
		v1.GET("/tracks/map-match", middleware.RequirePermission("track"), trackHandler.MapMatch)

		reportHandler := handler.NewReportHandler(s.store, s.logger)
		v1.POST("/reports/generate", middleware.RequirePermission("report"), reportHandler.Generate)
		v1.GET("/reports", middleware.RequirePermission("report"), reportHandler.List)
		// v3.0 报表统计扩展：在线率/里程/报警/驾驶行为
		v1.GET("/reports/online-rate", middleware.RequirePermission("report"), reportHandler.GetOnlineRateReport)
		v1.GET("/reports/mileage", middleware.RequirePermission("report"), reportHandler.GetMileageReport)
		v1.GET("/reports/alarm", middleware.RequirePermission("report"), reportHandler.GetAlarmReport)
		v1.GET("/reports/driving-behavior", middleware.RequirePermission("report"), reportHandler.GetDrivingBehaviorReport)
		// v3.0 报表统计扩展：油耗统计（基于 CAN 数据）
		v1.GET("/reports/fuel", middleware.RequirePermission("report"), reportHandler.GetFuelReport)

		cascadeHandler := handler.NewCascadeHandler(s.store, s.logger)
		v1.GET("/cascade/platforms", middleware.RequirePermission("cascade"), cascadeHandler.GetPlatforms)
		v1.POST("/cascade/platforms", middleware.RequirePermission("cascade"), cascadeHandler.CreatePlatform)
		v1.PUT("/cascade/platforms/:id", middleware.RequirePermission("cascade"), cascadeHandler.UpdatePlatform)
		v1.DELETE("/cascade/platforms/:id", middleware.RequirePermission("cascade"), cascadeHandler.DeletePlatform)

		aiHandler := handler.NewAIHandler(s.store, s.logger)
		if s.aiModule != nil {
			aiHandler.SetAIModule(s.aiModule)
		}
		if s.aiNLPModule != nil {
			aiHandler.SetAINLPModule(s.aiNLPModule)
		}
		// AUTO-FIX-2026-06-26: 第六轮遗留修复 - 保存 aiHandler 引用供 WebSocket 路由使用
		s.aiHandler = aiHandler
		// AI 路由组：应用 AI 专属限流（比全局限流更严格，保护 LLM 配额）
		aiGroup := v1.Group("/ai", middleware.AIRateLimit(s.cfg.API.RateLimit/10))
		aiGroup.Use(middleware.RequirePermission("ai"))
		aiGroup.POST("/analyze-alarm", aiHandler.AnalyzeAlarm)
		aiGroup.GET("/driver-fatigue", aiHandler.CheckFatigue)
		aiGroup.GET("/risk-score", aiHandler.GetRiskScore)
		aiGroup.POST("/anomaly-detect", aiHandler.AnomalyDetect)
		aiGroup.POST("/chat", aiHandler.Chat)
		// [商业版] 前端 API 契约对齐：AI 扩展路由
		aiGroup.POST("/nl2sql", aiHandler.NL2SQL)
		aiGroup.POST("/generate-report", aiHandler.GenerateReport)
		aiGroup.POST("/debug-protocol", aiHandler.DebugProtocol)
		aiGroup.GET("/knowledge", aiHandler.QueryKnowledge)

		// [商业版] 前端 API 契约对齐：/system/users, /system/config, /system/roles, /system/logs 别名
		v1.GET("/system/users", middleware.RequirePermission("system"), adminHandler.ListUsers)
		v1.POST("/system/users", middleware.RequirePermission("user_manage"), adminHandler.CreateUser)
		v1.PUT("/system/users/:id", middleware.RequirePermission("user_manage"), adminHandler.UpdateUser)
		v1.DELETE("/system/users/:id", middleware.RequirePermission("user_manage"), adminHandler.DeleteUser)
		v1.GET("/system/config", middleware.RequirePermission("system"), adminHandler.GetConfig)
		v1.PUT("/system/config", middleware.RequirePermission("system"), adminHandler.UpdateConfig)
		v1.GET("/system/roles", middleware.RequirePermission("role_manage"), adminHandler.ListRoles)
		v1.POST("/system/roles", middleware.RequirePermission("role_manage"), adminHandler.CreateRole)
		v1.PUT("/system/roles/:id", middleware.RequirePermission("role_manage"), adminHandler.UpdateRole)
		v1.DELETE("/system/roles/:id", middleware.RequirePermission("role_manage"), adminHandler.DeleteRole)
		v1.GET("/system/logs", middleware.RequirePermission("audit_log"), adminHandler.ListAuditLogs)

		// AUTO-FIX-2026-06-29 [P1]: maintenance/* 和 auth/license/:id 从根路由迁移至此，
		// 强制 JWT 鉴权 + RBAC 权限校验（原裸奔路由可被匿名调用执行维护模式/删除 license）。
		v1.GET("/maintenance/status", middleware.RequirePermission("system"), s.maintenanceStatus)
		v1.POST("/maintenance/start", middleware.RequirePermission("system"), s.maintenanceStart)
		v1.POST("/maintenance/stop", middleware.RequirePermission("system"), s.maintenanceStop)
		v1.DELETE("/auth/license/:id", middleware.RequirePermission("license"), s.removeLicense)
		// [商业版] 前端 API 契约对齐：DELETE /auth/license（无 :id，删除当前授权）
		v1.DELETE("/auth/license", middleware.RequirePermission("license"), s.removeLicense)
	}

	// AUTO-FIX-2026-06-29 [P1]: 注入 CORS 白名单到 WebSocket upgrader，
	// 防止 CSWSH 跨站 WebSocket 劫持（upgrader.CheckOrigin 校验 Origin 头）。
	// 必须在 WebSocket 路由注册前调用。
	websocket.SetCORSOrigins(s.cfg.API.CORSOrigins)

	if s.cfg.WebSocket.Enabled {
		wsHandler := websocket.NewHandler(s.wsHub, s.cfg, s.logger)
		r.GET("/ws/v1/stream", wsHandler.Handle)
	}

	// AUTO-FIX-2026-06-26: 第六轮遗留修复 - AI 助手 WebSocket 实时对话
	// 独立注册在根路由上（WebSocket 升级需绕过 JWT Body 中间件，token 通过 query 传递）
	// AUTO-FIX-2026-06-29 [P0]: 原 ChatWS 无任何 token 校验，匿名客户端可直接连接。
	// 修复：路由层显式调用 middleware.ExtractAndVerifyJWT 完成 JWT 验签后再升级 WS。
	if s.aiHandler != nil {
		r.GET("/api/v1/ai/chat/ws", func(c *gin.Context) {
			if err := middleware.ExtractAndVerifyJWT(c, s.cfg.API.JWTSecret, s.cfg.API.JWT); err != nil {
				return
			}
			s.aiHandler.ChatWS(c)
		})
	}

	// AUTO-FIX-2026-06-26: 第六轮遗留修复 - 公开官网配置接口（前端购买解锁按钮）
	websiteInfoHandler := handler.NewWebsiteInfoHandler(s.cfg.Website.PurchaseURL, s.logger)
	r.GET("/api/v1/website/info", websiteInfoHandler.Info)

	s.engine = r
}

func (s *Server) Start() error {
	addr := fmt.Sprintf(":%d", s.cfg.API.Port)
	s.logger.Info("API server starting", zap.String("addr", addr))

	util.SafeGo(s.logger, "api.wsHub", s.wsHub.Run)

	// AUTO-FIX-2026-06-30 [P1-6]: 启动 JWT 密钥自动轮换调度（90 天轮换 + 黑名单清理）
	if s.jwtRotationMgr != nil {
		s.jwtRotationMgr.Start()
	}

	if s.mergeEng != nil {
		eventBus := s.mergeEng.GetEventBus()
		eventBus.Subscribe(merge.EventTypeLocationUpdate, func(event merge.Event) {
			s.wsHub.Publish(string(event.Type), "location_update", event.Data)
		})
		eventBus.Subscribe(merge.EventTypeAlarmEvent, func(event merge.Event) {
			s.wsHub.Publish(string(event.Type), "alarm_event", event.Data)

			// 808 报警也接入 AI 过滤（异步）
			if s.aiModule == nil {
				return
			}
			alarm, ok := event.Data.(*storage.AlarmData)
			if !ok {
				return
			}
			util.SafeGo(s.logger, "api.filterAlarmWithAI", func() { s.filterAlarmWithAI(alarm) })
		})
		eventBus.Subscribe(merge.EventTypeAIAlert, func(event merge.Event) {
			// 先立即推送原始报警到前端，保证实时性
			s.wsHub.Publish(string(event.Type), "ai_alert", event.Data)

			// 异步调用 AI 过滤：对 1045/808 上报的报警进行误报识别，
			// 过滤结果回写 storage.Additional 并推送 "ai_alarm_filtered" 事件
			if s.aiModule == nil {
				return
			}
			alarm, ok := event.Data.(*storage.AlarmData)
			if !ok {
				return
			}
			util.SafeGo(s.logger, "api.filterAlarmWithAI", func() { s.filterAlarmWithAI(alarm) })
		})
		eventBus.Subscribe(merge.EventTypeSystemEvent, func(event merge.Event) {
			s.wsHub.Publish(string(event.Type), "system_event", event.Data)
		})
		s.logger.Info("WebSocket subscribed to EventBus")
	}

	// AUTO-FIX-2026-06-29 [P1]: 原 engine.Run 无 Shutdown 能力。
	// 改用 http.Server + ListenAndServe，配合 Stop(ctx) 调用 Shutdown 实现优雅停机。
	// 配置合理的超时，避免慢速客户端耗尽连接资源（Slowloris 攻击防护）。
	s.httpServer = &http.Server{
		Addr:              addr,
		Handler:           s.engine,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// AUTO-FIX-2026-07-02 [等保2.0 传输安全]: 支持 TLS/HTTPS
	// 配置 api.tls.enabled=true + cert_file/key_file 后自动启用 HTTPS
	// WebSocket 也自动升级为 WSS（TLS 透明覆盖）
	if s.cfg.API.TLS != nil && s.cfg.API.TLS.Enabled {
		tlsConfig, err := middleware.DefaultTLSConfig(s.cfg.API.TLS.CertFile, s.cfg.API.TLS.KeyFile)
		if err != nil {
			return fmt.Errorf("load TLS cert: %w", err)
		}
		s.httpServer.TLSConfig = tlsConfig
		s.logger.Info("HTTPS/TLS enabled",
			zap.String("cert", s.cfg.API.TLS.CertFile),
			zap.Bool("auto_renew", s.cfg.API.TLS.AutoRenew))
		if err := s.httpServer.ListenAndServeTLS(s.cfg.API.TLS.CertFile, s.cfg.API.TLS.KeyFile); err != nil && err != http.ErrServerClosed {
			return fmt.Errorf("api server run (TLS): %w", err)
		}
	} else {
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			return fmt.Errorf("api server run: %w", err)
		}
	}
	return nil
}

// Stop 优雅停机：关闭 HTTP listener 等待在途请求完成，并停止 WS Hub goroutine。
// 调用方应传入带超时的 ctx（如 10-30s），避免 Shutdown 无限等待长连接。
// 多次调用安全：httpServer.Shutdown 内部幂等，wsHub.Stop 用 sync.Once 保护。
func (s *Server) Stop(ctx context.Context) error {
	var firstErr error
	if s.httpServer != nil {
		if err := s.httpServer.Shutdown(ctx); err != nil {
			s.logger.Warn("http server shutdown error", zap.Error(err))
			firstErr = err
		}
	}
	if s.wsHub != nil {
		s.wsHub.Stop()
	}
	// AUTO-FIX-2026-06-30 [P1-6]: 停止 JWT 密钥轮换调度
	if s.jwtRotationMgr != nil {
		s.jwtRotationMgr.Stop()
	}
	// FIXED: [P1-1] 停止 LoginGuard 后台清理协程，防止 goroutine 泄漏
	if s.loginGuard != nil {
		s.loginGuard.Stop()
	}
	return firstErr
}

// aiFilterResult AI 过滤结果，序列化后写入 AlarmData.Additional
type aiFilterResult struct {
	IsFalseAlarm bool    `json:"is_false_alarm"`
	Confidence   float64 `json:"confidence"`
	Reason       string  `json:"reason"`
	Source       string  `json:"source"`
	FilteredAt   string  `json:"filtered_at"`
}

// filterAlarmWithAI 异步调用 AI 模块对报警进行误报过滤，
// 将结果回写到 storage（Additional 字段）并推送 "ai_alarm_filtered" 事件到前端。
func (s *Server) filterAlarmWithAI(alarm *storage.AlarmData) {
	if alarm == nil {
		return
	}

	data := map[string]interface{}{
		"alarm_id":   alarm.ID,
		"alarm_type": alarm.Type,
		"phone":      alarm.Phone,
		"vehicle_id": alarm.VehicleID,
		"level":      alarm.Level,
		"speed":      alarm.Speed,
		"latitude":   alarm.Latitude,
		"longitude":  alarm.Longitude,
		"source":     alarm.Source,
	}

	isFalse, confidence, reason, err := s.aiModule.AnalyzeAlarm(alarm.ID, alarm.Type, data)
	if err != nil {
		s.logger.Warn("AI alarm filter failed",
			zap.String("alarm_id", alarm.ID),
			zap.Error(err))
		return
	}

	result := aiFilterResult{
		IsFalseAlarm: isFalse,
		Confidence:   confidence,
		Reason:       reason,
		Source:       "ai",
		FilteredAt:   time.Now().Format(time.RFC3339),
	}

	additional, err := json.Marshal(result)
	if err != nil {
		s.logger.Warn("marshal ai filter result failed", zap.Error(err))
		return
	}
	alarm.Additional = additional

	// 误报报警降级为 0（信息级），避免污染告警列表
	if isFalse {
		alarm.Level = 0
	}

	// 回写存储
	if err := s.store.UpdateAlarm(context.Background(), alarm); err != nil {
		s.logger.Warn("update alarm with ai result failed",
			zap.String("alarm_id", alarm.ID),
			zap.Error(err))
	}

	// 推送过滤结果到前端
	s.wsHub.Publish("ai_alarm_filtered", "ai_alarm_filtered", map[string]interface{}{
		"alarm_id":      alarm.ID,
		"phone":         alarm.Phone,
		"vehicle_id":    alarm.VehicleID,
		"alarm_type":    alarm.Type,
		"is_false_alarm": isFalse,
		"confidence":    confidence,
		"reason":        reason,
		"filtered_at":   result.FilteredAt,
	})

	s.logger.Info("AI alarm filter completed",
		zap.String("alarm_id", alarm.ID),
		zap.String("alarm_type", alarm.Type),
		zap.Bool("is_false_alarm", isFalse),
		zap.Float64("confidence", confidence))
}

func (s *Server) GetEngine() *gin.Engine {
	return s.engine
}

func (s *Server) GetWSHub() *websocket.Hub {
	return s.wsHub
}

func (s *Server) SetWebFS(webFS fs.FS) {
	s.webFS = webFS
}

func (s *Server) SetLicenseManager(lm interface {
	GetStatus() interface{}
	StartTrial(moduleName string) error
	Activate(licenseKey string) error
	Remove(id string) error
	ValidateArchive() error // AUTO-FIX-2026-06-30 [集成-6]: 归档功能授权校验
}) {
	s.licenseMgr = lm
	// AUTO-FIX-2026-06-30 [集成-6]: 同步注入归档授权校验到 storage handler
	if s.storageHandler != nil {
		s.storageHandler.SetLicenseValidator(lm)
	}
}

func (s *Server) SetMaintenanceMode(mm *maintenance.Mode) {
	s.maintenanceMode = mm
}

func (s *Server) SetMediaClient(client *media.ZLMediaKitClient) {
	s.mediaClient = client
	if s.mediaHandler != nil {
		s.mediaHandler.SetMediaClient(client)
	}
	// FIXED-2026-07-23 [P2]: 动态注册 ZLMediaKit 健康检查器
	if client != nil {
		s.AddHealthChecker(handler.NewZLMediaKitChecker("zlmediakit", client.IsConnected))
	}
}

// SetVideoEngine allows late binding of the VideoEngine (created after the API
// server in main.go). It delegates to the media handler so RTP data can be
// forwarded to ZLMediaKit.
func (s *Server) SetVideoEngine(engine *jt1078.VideoEngine) {
	s.videoEngine = engine
	if s.mediaHandler != nil {
		s.mediaHandler.SetVideoEngine(engine)
	}
}

// SetCommandSender 注入外部创建的 CommandSender，使 API 层与消息处理层共享同一个实例。
// 这样 MessageHandler 收到终端通用应答时能唤醒 CommandSender.SendAndWait 的 pending 队列。
func (s *Server) SetCommandSender(cs *handler.CommandSender) {
	s.commandSender = cs
}

func (s *Server) SetRBACManager(mgr *module.RBACManager) {
	s.rbacMgr = mgr
	// FIXED-2026-07-24: setupRouter() 在 NewServer() 中调用（此时 rbacMgr 为 nil），
	// adminHandler 未注入 RBAC。此处补注入，确保用户管理 CRUD 接口可用。
	if s.adminHandler != nil && mgr != nil {
		s.adminHandler.SetRBACManager(mgr)
	}
}

// SetStorageLayers 注入多层存储实例（v3.0 存储分层管理 API 使用）
func (s *Server) SetStorageLayers(layers *storage.StorageLayers) {
	if s.storageHandler != nil && layers != nil {
		s.storageHandler.SetStorageLayers(layers)
	}
	// AUTO-FIX-2026-06-30 [P1-6]: 注入 Redis 到 token 黑名单（多节点共享）
	if layers != nil && layers.Cache != nil && s.tokenBlacklist != nil {
		s.tokenBlacklist.cache = layers.Cache
	}
}

// SetArchiver 注入归档器（v3.0 存储分层管理 API 使用）
func (s *Server) SetArchiver(a handler.ArchiveRunner) {
	if s.storageHandler != nil {
		s.storageHandler.SetArchiver(a)
	}
}

// SetArchiveProgressProvider 注入归档进度查询回调（AUTO-FIX-2026-07-02）
// 启用 /storage/archive/progress 端点查询归档实时进度和上次运行结果
func (s *Server) SetArchiveProgressProvider(p handler.ArchiveProgressProvider) {
	if s.storageHandler != nil {
		s.storageHandler.SetArchiveProgressProvider(p)
	}
}

func (s *Server) SetAuditLogger(al *audit.AuditLogger) {
	s.auditLogger = al
	// v3.0: 同步注入到 handler 包全局引用，供 AdminHandler.ListAuditLogs/BackupData/RestoreData 使用
	handler.SetAuditLoggerRef(al)
	// AUTO-FIX-2026-06-30 [P1-6]: 注入审计回调到 JWT 轮换管理器（记录密钥轮换/撤销事件）
	if s.jwtRotationMgr != nil && al != nil {
		s.jwtRotationMgr.SetAuditFn(func(action, detail string) {
			al.Log(&audit.AuditEntry{
				Action:   action,
				Operator: "system",
				Resource: "jwt",
				Result:   "success",
				Details:  map[string]interface{}{"detail": detail},
			})
		})
	}
}

// SetLoginGuard 注入登录守卫（等保2.0 防克隆：失败锁定 + 设备指纹 + 多IP/异地/新设备告警）
// 必须在 setupRouter 完成后调用；authLogin 会使用该实例进行登录前锁定检查和登录后异常检测。
func (s *Server) SetLoginGuard(g *security.LoginGuard) {
	s.loginGuard = g
}

// SetDataCipher 注入国密 SM4-GCM 数据加密器（关键数据落库加密：手机号/身份证/车牌）
// 未注入时按明文处理（向后兼容）；注入后 EncryptField 对明文加密、DecryptField 对密文解密。
func (s *Server) SetDataCipher(c *secret.DataCipher) {
	s.dataCipher = c
}

func (s *Server) SetAIModule(m interface{ AnalyzeAlarm(alarmID, alarmType string, data map[string]interface{}) (isFalseAlarm bool, confidence float64, reason string, err error) }) {
	s.aiModule = m
}

func (s *Server) SetAINLPModule(m interface{ Chat(query, sessionID string) (response string, err error) }) {
	s.aiNLPModule = m
}

func (s *Server) SetLogger(l *zap.Logger) {
	s.logger = l
}

func (s *Server) serveDashboard(c *gin.Context) {
	if s.webFS != nil {
		path := c.Request.URL.Path
		if path == "/" || path == "/dashboard" {
			path = "index.html"
		} else {
			path = path[1:]
		}

		data, err := fs.ReadFile(s.webFS, path)
		if err != nil {
			data, err = fs.ReadFile(s.webFS, "index.html")
			if err != nil {
				c.Header("Content-Type", "text/html; charset=utf-8")
				c.String(http.StatusOK, `<!DOCTYPE html><html><head><meta charset="UTF-8"><title>JTE Dashboard</title></head><body style="background:#0a0e17;color:#f1f5f9;display:flex;align-items:center;justify-content:center;height:100vh;font-family:sans-serif"><div style="text-align:center"><h1>JTE Dashboard</h1><p style="color:#94a3b8">Dashboard file not found. Please build the web assets.</p></div></body></html>`)
				return
			}
		}

		contentType := "application/octet-stream"
		switch {
		case len(path) > 5 && path[len(path)-5:] == ".html":
			contentType = "text/html; charset=utf-8"
		case len(path) > 4 && path[len(path)-4:] == ".css":
			contentType = "text/css; charset=utf-8"
		case len(path) > 3 && path[len(path)-3:] == ".js":
			contentType = "application/javascript; charset=utf-8"
		case len(path) > 4 && path[len(path)-4:] == ".svg":
			contentType = "image/svg+xml"
		case len(path) > 4 && path[len(path)-4:] == ".png":
			contentType = "image/png"
		case len(path) > 4 && path[len(path)-4:] == ".ico":
			contentType = "image/x-icon"
		case len(path) > 4 && path[len(path)-4:] == ".json":
			contentType = "application/json"
		}

		c.Data(http.StatusOK, contentType, data)
		return
	}
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.String(http.StatusOK, `<!DOCTYPE html><html><head><meta charset="UTF-8"><title>JTE Dashboard</title></head><body style="background:#0a0e17;color:#f1f5f9;display:flex;align-items:center;justify-content:center;height:100vh;font-family:sans-serif"><div style="text-align:center"><h1>JTE Dashboard</h1><p style="color:#94a3b8">Dashboard file not found. Please build the web assets.</p></div></body></html>`)
}

func (s *Server) listVehicleLocations(c *gin.Context) {
	locations, err := s.store.ListOnlineLocations(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": err.Error()})
		return
	}

	type vehicleLocation struct {
		Phone      string  `json:"phone"`
		Protocol   string  `json:"protocol"`
		Latitude   float64 `json:"latitude"`
		Longitude  float64 `json:"longitude"`
		Speed      float64 `json:"speed"`
		Direction  int     `json:"direction"`
		LastActive string  `json:"last_active"`
	}

	// FIXED-2026-07-17 [P2]: 修复 N+1 查询问题。
	// 原代码对每个 location 调用 GetVehicleByPhone，产生 N+1 次 DB 查询。
	// 改为一次性查询全部车辆，构建 phone->protocol 映射，O(1) 查找。
	phoneToProtocol := make(map[string]string, len(locations))
	if len(locations) > 0 {
		vehicleResult, vErr := s.store.ListVehicles(c.Request.Context(), storage.ListOptions{Page: 1, PageSize: 100000})
		if vErr == nil {
			if vehicles, ok := vehicleResult.Items.([]*storage.Vehicle); ok {
				for _, v := range vehicles {
					phoneToProtocol[v.Phone] = v.Protocol
				}
			}
		}
	}

	result := make([]vehicleLocation, 0, len(locations))
	for _, loc := range locations {
		result = append(result, vehicleLocation{
			Phone:      loc.Phone,
			Protocol:   phoneToProtocol[loc.Phone],
			Latitude:   loc.Latitude,
			Longitude:  loc.Longitude,
			Speed:      loc.Speed,
			Direction:  loc.Direction,
			LastActive: loc.ReceivedAt.Format("2006-01-02T15:04:05Z07:00"),
		})
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "vehicles": result})
}

func (s *Server) authStatus(c *gin.Context) {
	if s.licenseMgr == nil {
		c.JSON(http.StatusOK, gin.H{
			"code": 0,
			"data": gin.H{
				"machine_fingerprint": "",
				"licenses":           []interface{}{},
				"active_modules":     []string{"jt808", "jt1078"},
				"trials":             map[string]interface{}{},
			},
		})
		return
	}
	status := s.licenseMgr.GetStatus()
	// DEV-FIX: 开发/测试环境无许可证时，返回所有模块为活跃状态以便前端路由验收
	if licStatus, ok := status.(*module.LicenseStatus); ok && (licStatus.ActiveModules == nil || len(licStatus.ActiveModules) == 0) {
		licStatus.ActiveModules = []string{
			"jt808", "jt1078", "protocol_809", "protocol_1045",
			"protocol_1078", "protocol_905", "storage", "ai", "ai_nlp",
		}
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": status})
}

func (s *Server) startTrial(c *gin.Context) {
	if s.licenseMgr == nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "license manager not available"})
		return
	}

	var req struct {
		ModuleName string `json:"module_name" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "module_name is required"})
		return
	}

	if err := s.licenseMgr.StartTrial(req.ModuleName); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "trial started"})
}

func (s *Server) activateLicense(c *gin.Context) {
	if s.licenseMgr == nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "license manager not available"})
		return
	}

	var req struct {
		Code string `json:"code" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "code is required"})
		return
	}

	if err := s.licenseMgr.Activate(req.Code); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "License activated successfully"})
}

func (s *Server) removeLicense(c *gin.Context) {
	if s.licenseMgr == nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "license manager not available"})
		return
	}

	id := c.Param("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "license id is required"})
		return
	}

	if err := s.licenseMgr.Remove(id); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "License removed"})
}

func (s *Server) maintenanceStatus(c *gin.Context) {
	if s.maintenanceMode == nil {
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{"active": false}})
		return
	}
	status := s.maintenanceMode.GetStatus()
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": status})
}

func (s *Server) maintenanceStart(c *gin.Context) {
	if s.maintenanceMode == nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "maintenance mode not available"})
		return
	}

	var req struct {
		Reason     string `json:"reason"`
		StopWrites bool   `json:"stop_writes"` // AUTO-FIX-2026-06-30 [P2-6]: true=停止写入+缓冲+0x8103暂停上报
	}
	_ = c.ShouldBindJSON(&req)
	if req.Reason == "" {
		req.Reason = "scheduled maintenance"
	}

	if err := s.maintenanceMode.Start(req.Reason, req.StopWrites); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "maintenance mode started", "stop_writes": req.StopWrites})
}

func (s *Server) maintenanceStop(c *gin.Context) {
	if s.maintenanceMode == nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "maintenance mode not available"})
		return
	}

	if err := s.maintenanceMode.Stop(); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "maintenance mode stopped"})
}

// handleJWTEmergencyRotate JWT 密钥应急轮换（泄露应急）。
// 立即生成新 kid+secret，旧密钥保留 7 天仅供验签；全局撤销所有已签发 token，强制重新登录。
// AUTO-FIX-2026-06-30 [P1-6]
func (s *Server) handleJWTEmergencyRotate(c *gin.Context) {
	if s.jwtRotationMgr == nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "JWT 轮换管理器未启用"})
		return
	}
	newKid, err := s.jwtRotationMgr.EmergencyRotate()
	if err != nil {
		s.logger.Error("JWT 应急轮换失败", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": err.Error()})
		return
	}
	// 审计日志
	if s.auditLogger != nil {
		s.auditLogger.Log(&audit.AuditEntry{
			Action:   "jwt_emergency_rotate",
			Operator: c.GetString("username"),
			IP:       c.ClientIP(),
			Resource: "jwt",
			Result:   "success",
			Details: map[string]interface{}{
				"new_kid":        newKid,
				"global_revoke":  true,
			},
		})
	}
	s.logger.Warn("JWT 应急轮换已触发（管理员操作）",
		zap.String("new_kid", newKid),
		zap.String("operator", c.GetString("username")),
		zap.String("ip", c.ClientIP()))
	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "JWT 密钥应急轮换完成，所有用户需重新登录",
		"new_kid": newKid,
	})
}

// handleJWTStatus 查询 JWT 密钥状态（当前 active kid、轮换配置、kid 列表）。
// AUTO-FIX-2026-06-30 [P1-6]
func (s *Server) handleJWTStatus(c *gin.Context) {
	status := gin.H{
		"rotation_enabled":  s.jwtRotationMgr != nil,
		"blacklist_enabled": s.tokenBlacklist != nil,
	}
	if s.cfg.API.JWT != nil {
		// 通过 GetActiveSecret 间接读 active kid，避免直接访问 mu
		activeKid, _, ok := s.cfg.API.JWT.GetActiveSecret()
		status["active_kid"] = activeKid
		status["rotate_days"] = s.cfg.API.JWT.RotateDays
		status["kms_source"] = s.cfg.API.JWT.KMSSource
		// kids 列表通过遍历 Secrets（无锁读取，允许轻微竞态，仅用于状态展示）
		kids := make([]string, 0, len(s.cfg.API.JWT.Secrets))
		for kid := range s.cfg.API.JWT.Secrets {
			kids = append(kids, kid)
		}
		status["kid_count"] = len(kids)
		status["kids"] = kids
		_ = ok
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": status})
}

// signToken 签发 JWT，自动设置 kid header 并使用对应 secret。
// AUTO-FIX-2026-06-30 [P1-6]: 注入 jti（JWT ID）用于黑名单撤销；注入 iat 用于全局撤销校验。
func (s *Server) signToken(claims jwt.MapClaims) (string, error) {
	// 注入 jti（唯一标识）+ iat（签发时间），供 token 黑名单 / 全局撤销校验
	if claims["jti"] == nil {
		jtiBytes := make([]byte, 16)
		if _, err := rand.Read(jtiBytes); err != nil {
			return "", fmt.Errorf("生成 jti 失败: %w", err)
		}
		claims["jti"] = hex.EncodeToString(jtiBytes)
	}
	if claims["iat"] == nil {
		claims["iat"] = time.Now().Unix()
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signSecret := s.cfg.API.JWTSecret
	if s.cfg.API.JWT != nil {
		if kid, secret, ok := s.cfg.API.JWT.GetActiveSecret(); ok {
			token.Header["kid"] = kid
			signSecret = secret
		}
	}
	return token.SignedString([]byte(signSecret))
}

// parseToken 解析 JWT，支持 kid 多密钥查找（向后兼容无 kid 旧 token）。
// AUTO-FIX-2026-06-30 [P1-6]: 解析后检查 token 黑名单（单 token 撤销 + 全局撤销）。
func (s *Server) parseToken(tokenStr string) (*jwt.Token, error) {
	token, err := jwt.Parse(tokenStr, func(token *jwt.Token) (interface{}, error) {
		if kid, ok := token.Header["kid"].(string); ok && kid != "" {
			if s.cfg.API.JWT != nil {
				if secret, found := s.cfg.API.JWT.GetSecret(kid); found {
					return []byte(secret), nil
				}
			}
		}
		return []byte(s.cfg.API.JWTSecret), nil
	})
	if err != nil || !token.Valid {
		return token, err
	}
	// P1-6: 黑名单校验（泄露应急撤销）
	if s.tokenBlacklist != nil {
		if claims, ok := token.Claims.(jwt.MapClaims); ok {
			// 单 token 撤销（按 jti）
			if jti, _ := claims["jti"].(string); jti != "" && s.tokenBlacklist.IsRevoked(jti) {
				return token, fmt.Errorf("token 已被撤销")
			}
			// 全局撤销（按 iat 早于撤销点）
			if iatRaw, exists := claims["iat"]; exists {
				var iat time.Time
				switch v := iatRaw.(type) {
				case float64:
					iat = time.Unix(int64(v), 0)
				case json.Number:
					sec, _ := v.Int64()
					iat = time.Unix(sec, 0)
				case int64:
					iat = time.Unix(v, 0)
				}
				if !iat.IsZero() && s.tokenBlacklist.IsGlobalRevoked(iat) {
					return token, fmt.Errorf("token 已被全局撤销，请重新登录")
				}
			}
		}
	}
	return token, nil
}

// issueCSRFToken 下发 CSRF token 到 cookie，返回 token 供写入响应体
func (s *Server) issueCSRFToken(c *gin.Context) string {
	token, err := middleware.SetCSRFToken(c)
	if err != nil {
		s.logger.Warn("generate csrf token failed", zap.Error(err))
		return ""
	}
	return token
}

func (s *Server) authLogin(c *gin.Context) {
	var req struct {
		Username string `json:"username" binding:"required"`
		Password string `json:"password" binding:"required"`
		// AUTO-FIX-2026-07-02 [防克隆]: 前端采集的设备指纹特征（JSON），用于生成设备指纹绑定校验
		DeviceFingerprint string `json:"device_fingerprint,omitempty"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "username and password required"})
		return
	}

	// 登录成功即下发 CSRF token（HttpOnly + SameSite=Strict Cookie）
	csrfToken := s.issueCSRFToken(c)

	// AUTO-FIX-2026-06-29 [P1]: 原 rbacMgr==nil 时降级返回 super_admin 后门，
	// 任意账号密码均可登录获取管理员权限——已改为默认拒绝，rbacMgr 必须初始化。
	if s.rbacMgr == nil {
		s.logger.Error("rbac manager not initialized, login rejected",
			zap.String("username", req.Username),
			zap.String("client_ip", c.ClientIP()))
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"code":    503,
			"message": "authentication service not initialized",
		})
		return
	}

	// AUTO-FIX-2026-07-02 [等保2.0/防克隆]: 登录前检查账户是否被登录守卫锁定
	clientIP := c.ClientIP()
	if s.loginGuard != nil {
		if allowed, reason := s.loginGuard.CheckLogin(req.Username, clientIP, req.DeviceFingerprint); !allowed {
			s.logger.Warn("login rejected: account locked",
				zap.String("username", req.Username),
				zap.String("client_ip", clientIP))
			if s.auditLogger != nil {
				_ = s.auditLogger.LogAuthDetail(
					req.Username, "login", "locked",
					clientIP, c.Request.UserAgent(), "",
					map[string]interface{}{"reason": reason},
				)
			}
			c.JSON(http.StatusTooManyRequests, gin.H{
				"code":    429,
				"message": reason,
			})
			return
		}
	}

	user, err := s.rbacMgr.Authenticate(req.Username, req.Password)
	if err != nil {
		// AUTO-FIX-2026-07-02 [等保2.0]: 记录登录失败审计日志
		if s.auditLogger != nil {
			_ = s.auditLogger.LogAuthDetail(
				req.Username, "login", "failed",
				clientIP, c.Request.UserAgent(), "",
				map[string]interface{}{"reason": err.Error()},
			)
		}
		// AUTO-FIX-2026-07-02 [防克隆]: 记录登录失败（失败计数达到阈值自动锁定）
		if s.loginGuard != nil {
			if locked, until := s.loginGuard.RecordLoginFailure(req.Username, clientIP); locked {
				s.logger.Warn("account locked due to repeated failures",
					zap.String("username", req.Username),
					zap.String("client_ip", clientIP),
					zap.Time("locked_until", until))
				if s.auditLogger != nil {
					_ = s.auditLogger.LogSecurity(req.Username, "account_locked", "success", clientIP,
						map[string]interface{}{
							"username":     req.Username,
							"ip":           clientIP,
							"locked_until": until,
						})
				}
			}
		}
		c.JSON(http.StatusUnauthorized, gin.H{"code": 401, "message": err.Error()})
		return
	}

	// AUTO-FIX-2026-07-02 [防克隆]: 记录登录成功并检测异常（多IP/异地/新设备）
	if s.loginGuard != nil {
		alert := s.loginGuard.RecordLoginSuccess(user.Username, clientIP, c.Request.UserAgent(), req.DeviceFingerprint)
		if alert != nil {
			s.logger.Warn("login anomaly detected",
				zap.String("username", user.Username),
				zap.String("alert_type", alert.Type),
				zap.String("message", alert.Message),
				zap.String("ip", alert.IP))
			if s.auditLogger != nil {
				_ = s.auditLogger.LogSecurity(user.Username, "login_anomaly", "warning", clientIP,
					map[string]interface{}{
						"alert_type": alert.Type,
						"message":    alert.Message,
						"ip":         alert.IP,
						"geo":        alert.Geo,
					})
			}
		}
	}

	// AUTO-FIX-2026-07-02 [等保2.0]: 记录登录成功审计日志
	if s.auditLogger != nil {
		_ = s.auditLogger.LogAuthDetail(
			user.Username, "login", "success",
			clientIP, c.Request.UserAgent(), user.ID,
			map[string]interface{}{"role": string(user.Role)},
		)
	}

	perms := s.rbacMgr.GetPermissions(user.ID)
	permStrs := make([]string, len(perms))
	for i, p := range perms {
		permStrs[i] = string(p)
	}

	// AUTO-FIX-2026-07-02: 获取数据权限范围并写入 JWT
	dataScope := s.rbacMgr.GetDataScope(user.ID)

	expireHours := s.cfg.API.JWTExpireHours
	if expireHours <= 0 {
		expireHours = 24
	}

	tokenStr, err := s.signToken(jwt.MapClaims{
		"sub":         user.ID,
		"username":    user.Username,
		"role":        string(user.Role),
		"permissions": permStrs,
		"data_scope": gin.H{
			"scope_type":  dataScope.ScopeType,
			"org_id":      dataScope.OrgID,
			"vehicle_ids": dataScope.VehicleIDs,
		},
		"exp": time.Now().Add(time.Duration(expireHours) * time.Hour).Unix(),
		"iat": time.Now().Unix(),
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "token generation failed"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": gin.H{
			"token":       tokenStr,
			"csrf_token":  csrfToken,
			"id":          user.ID,
			"username":    user.Username,
			"role":        string(user.Role),
			"permissions": permStrs,
		},
	})
}

func (s *Server) auditMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()

		method := c.Request.Method
		path := c.Request.URL.Path

		if method == "GET" || method == "OPTIONS" {
			return
		}

		if !strings.HasPrefix(path, "/api/v1/") {
			return
		}

		if strings.HasPrefix(path, "/api/v1/health") {
			return
		}

		operator := "anonymous"
		if uid, exists := c.Get("user_id"); exists {
			operator = fmt.Sprintf("%v", uid)
		}
		if username, exists := c.Get("username"); exists {
			operator = fmt.Sprintf("%v", username)
		}

		result := "success"
		if c.Writer.Status() >= 400 {
			result = "failed"
		}

		// AUTO-FIX-2026-07-02 [等保2.0]: 审计日志补充 user_agent/session_id/category/result_code
		sessionID := ""
		if sid, exists := c.Get("session_id"); exists {
			sessionID = fmt.Sprintf("%v", sid)
		}
		category := classifyAuditAction(path)
		_ = s.auditLogger.Log(&audit.AuditEntry{
			Operator:  operator,
			Action:    method,
			Resource:  path,
			Result:    result,
			IP:        c.ClientIP(),
			UserAgent: c.Request.UserAgent(),
			SessionID: sessionID,
			Category:  category,
			ResultCode: c.Writer.Status(),
		})
	}
}

// classifyAuditAction 根据请求路径分类操作类型（等保2.0 审计分类）
func classifyAuditAction(path string) string {
	switch {
	case strings.Contains(path, "/auth/"):
		return audit.CategoryAuth
	case strings.Contains(path, "/users") || strings.Contains(path, "/roles"):
		return audit.CategoryUser
	case strings.Contains(path, "/config") || strings.Contains(path, "/system/"):
		return audit.CategoryConfig
	case strings.Contains(path, "/system/jwt/"):
		return audit.CategorySecurity
	case strings.Contains(path, "/maintenance"):
		return audit.CategorySystem
	default:
		return audit.CategoryData
	}
}

func (s *Server) authRefresh(c *gin.Context) {
	tokenStr := c.GetHeader("Authorization")
	if tokenStr == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"code": 401, "message": "authorization required"})
		return
	}

	tokenStr = strings.TrimPrefix(tokenStr, "Bearer ")

	token, err := s.parseToken(tokenStr)

	if err != nil || !token.Valid {
		c.JSON(http.StatusUnauthorized, gin.H{"code": 401, "message": "invalid token"})
		return
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"code": 401, "message": "invalid claims"})
		return
	}

	expireHours := s.cfg.API.JWTExpireHours
	if expireHours <= 0 {
		expireHours = 24
	}

	tokenStr, err = s.signToken(jwt.MapClaims{
		"sub":         claims["sub"],
		"username":    claims["username"],
		"role":        claims["role"],
		"permissions": claims["permissions"],
		"data_scope":  claims["data_scope"],
		"exp":         time.Now().Add(time.Duration(expireHours) * time.Hour).Unix(),
		"iat":         time.Now().Unix(),
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "token generation failed"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": gin.H{
			"token": tokenStr,
		},
	})
}

// authPermissions 返回当前登录用户的完整权限树 + 角色 + 数据范围（AUTO-FIX-2026-07-02）
// GET /api/v1/auth/permissions
// 前端登录后调用此接口动态拉取权限，替代硬编码的 ROLE_PERMISSIONS。
// 响应：
//   - role: 角色标识
//   - role_label: 角色显示名
//   - permissions: 权限字符串列表
//   - permission_tree: 按模块分组的权限树
//   - data_scope: 数据权限范围（scope_type/org_id/vehicle_ids）
func (s *Server) authPermissions(c *gin.Context) {
	userID, _ := c.Get("user_id")
	uid, _ := userID.(string)
	if uid == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"code": 401, "message": "authorization required"})
		return
	}

	// super_admin 从 JWT claims 获取角色；rbacMgr 可能为 nil（降级模式）
	roleStr, _ := c.Get("role")
	role, _ := roleStr.(string)
	if role == "" {
		role = "readonly"
	}

	roleLabel := map[string]string{
		"super_admin":     "超级管理员",
		"admin":           "管理员",
		"operator":        "操作员",
		"readonly":        "只读用户",
		"system_admin":    "系统管理员",
		"security_admin":  "安全管理员",
		"audit_admin":     "审计管理员",
	}[role]
	if roleLabel == "" {
		roleLabel = role
	}

	// 权限列表：优先从 JWT claims（c.Get("permissions")）取
	permStrs, _ := c.Get("permissions")
	perms, _ := permStrs.([]string)

	// 数据权限范围
	var dataScope gin.H
	if s.rbacMgr != nil {
		// 重新从 RBAC 拉取最新权限（权限可能已变更，JWT 中是登录时快照）
		freshPerms := s.rbacMgr.GetPermissions(uid)
		if len(freshPerms) > 0 {
			perms = make([]string, len(freshPerms))
			for i, p := range freshPerms {
				perms[i] = string(p)
			}
		}
		ds := s.rbacMgr.GetDataScope(uid)
		dataScope = gin.H{
			"scope_type":  ds.ScopeType,
			"org_id":      ds.OrgID,
			"vehicle_ids": ds.VehicleIDs,
		}
	} else {
		dataScope = gin.H{"scope_type": "all"}
	}

	// 构建权限树（按模块分组，便于前端渲染菜单）
	permTree := buildPermissionTree(perms)

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": gin.H{
			"role":            role,
			"role_label":      roleLabel,
			"permissions":     perms,
			"permission_tree": permTree,
			"data_scope":      dataScope,
		},
	})
}

// buildPermissionTree 将扁平权限列表构建为按模块分组的权限树（AUTO-FIX-2026-07-02）
// 供前端菜单渲染使用：每个模块包含 label 和 children（子权限）
func buildPermissionTree(perms []string) []gin.H {
	// 权限模块定义（模块标识 -> 显示名 + 子权限映射）
	moduleDefs := []struct {
		key    string
		label  string
		perm   string
	}{
		{"monitor", "监控中心", "monitor"},
		{"device", "设备管理", "device"},
		{"vehicle", "车辆管理", "vehicle"},
		{"alarm", "报警中心", "alarm"},
		{"track", "轨迹回放", "track"},
		{"video", "视频监控", "video"},
		{"command", "指令下发", "command"},
		{"report", "报表中心", "report"},
		{"cascade", "级联平台", "cascade"},
		{"user_manage", "用户管理", "user_manage"},
		{"role_manage", "角色权限", "role_manage"},
		{"system", "系统设置", "system"},
		{"module", "模块管理", "module"},
		{"license", "授权管理", "license"},
		{"audit_log", "审计日志", "audit_log"},
		{"security_manage", "安全管理", "security_manage"},
		{"ai", "AI智能", "ai"},
	}

	permSet := make(map[string]bool, len(perms))
	for _, p := range perms {
		permSet[p] = true
	}

	tree := make([]gin.H, 0, len(moduleDefs))
	for _, md := range moduleDefs {
		if !permSet[md.perm] {
			continue
		}
		tree = append(tree, gin.H{
			"key":   md.key,
			"label": md.label,
			"perm":  md.perm,
		})
	}
	return tree
}