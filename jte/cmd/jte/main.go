package main

import (
	"bufio"
	"context"
	"embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/suoten/jt-engine/internal/api"
	apiHandler "github.com/suoten/jt-engine/internal/api/handler"
	"github.com/suoten/jt-engine/internal/audit"
	"github.com/suoten/jt-engine/internal/config"
	"github.com/suoten/jt-engine/internal/gateway"
	"github.com/suoten/jt-engine/internal/media"
	"github.com/suoten/jt-engine/pkg/handler"
	"github.com/suoten/jt-engine/internal/maintenance"
	"github.com/suoten/jt-engine/pkg/merge"
	"github.com/suoten/jt-engine/internal/migration"
	"github.com/suoten/jt-engine/internal/module"
	"github.com/suoten/jt-engine/pkg/protocol"
	jt808 "github.com/suoten/jt-engine/pkg/protocol/jt808"
	jt1078 "github.com/suoten/jt-engine/pkg/protocol/jt1078"
	"github.com/suoten/jt-engine/internal/registry"
	"github.com/suoten/jt-engine/internal/security"
	"github.com/suoten/jt-engine/internal/simulator"
	"github.com/suoten/jt-engine/internal/trace"
	"github.com/suoten/jt-engine/internal/util"
	"github.com/suoten/jt-engine/pkg/crypto/gmsm"
	"github.com/suoten/jt-engine/pkg/crypto/secret"
	"github.com/suoten/jt-engine/pkg/storage"
	"github.com/suoten/jt-engine/pkg/storage/memory"
	"github.com/suoten/jt-engine/pkg/storage/sqlite"
	"go.uber.org/zap"
)

//go:embed all:web
var webFS embed.FS

var Version = "1.0.0"

// AIAnalyzer 定义AI报警分析接口（供 module-ai 实现并注册）
type AIAnalyzer interface {
	AnalyzeAlarm(alarmID, alarmType string, data map[string]interface{}) (isFalseAlarm bool, confidence float64, reason string, err error)
}

// AIChatter 定义AI对话接口（供 module-ai-nlp 实现并注册）
type AIChatter interface {
	Chat(query, sessionID string) (response string, err error)
}

type App struct {
	Config          *config.Config
	Logger          *zap.Logger
	Storage         storage.Interface
	// v2.0 百亿级轨迹数据存储方?- 多层存储容器
	// 关系?= Storage（v1.x 兼容），时序/缓存/对象层按需注入
	StorageLayers   *storage.StorageLayers
	Gateway         *gateway.TCPServer
	JT809Server     *gateway.JT809Server
	JT809Clients    []*gateway.JT809Client
	Registry        *registry.FeatureRegistry
	Protocol        *protocol.Hub
	Sessions        *gateway.SessionManager
	Merge           *merge.Engine
	APIServer       *api.Server
	ModuleLoader    *module.Loader
	AuthManager     *module.AuthManager
	LicenseMgr      *module.LicenseManager
	RBACManager     *module.RBACManager
	AuditLogger     *audit.AuditLogger
	HandlerRegistry *handler.HandlerRegistry
	AIModule        AIAnalyzer
	AINLPModule     AIChatter
	// AUTO-FIX-2026-06-29 [P1]: ?ctx 被丢弃（_ = ctx），组件无法感知进程停机?
	// Ctx 保存?context，供需?context 的组件使用；Cancel 取消后触发停机?
	Ctx             context.Context
	Cancel          context.CancelFunc
	// AUTO-FIX-2026-06-30 [P1-5]: 心跳超时资源清理回调，由 NewApp 设置、Start 注入?HeartbeatChecker
	onSessionTimeout func(*gateway.Session)
	// AUTO-FIX-2026-06-30 [P1-2/P1-5]: 持有 msgHandler 引用，便?SetStorageLayers
	// 延迟注入 Redis 缓存层（鉴权结果缓存 24h / 在线状态清理）?
	// StorageLayers ?module-storage ?Init 后注入，晚于 NewApp，因此需保存引用?
	msgHandler *MessageHandler
}

func (a *App) ProtocolHub() *protocol.Hub {
	return a.Protocol
}

func (a *App) GetHandlerRegistry() *handler.HandlerRegistry {
	return a.HandlerRegistry
}

func (a *App) GetStorage() storage.Interface {
	return a.Storage
}

func (a *App) GetMergeEngine() *merge.Engine {
	return a.Merge
}

// GetAuditLogger 返回审计日志记录器，?module-security-audit 等模块生成合规报告?
func (a *App) GetAuditLogger() *audit.AuditLogger {
	return a.AuditLogger
}

// GetSessions 返回会话管理器，?module-tts 等模块查找在线设备会话并下发指令?
func (a *App) GetSessions() *gateway.SessionManager {
	return a.Sessions
}

// GetGateway 返回 808 协议网关服务器，?module-loadtest 等模块获取监听地址?
func (a *App) GetGateway() *gateway.TCPServer {
	return a.Gateway
}

func (a *App) GetLogger() *zap.Logger {
	return a.Logger
}


func (a *App) GetConfig() *config.Config {
	return a.Config
}

func (a *App) GetRegistry() *registry.FeatureRegistry {
	return a.Registry
}

func (a *App) GetNodeID() string {
	if a.Config != nil && a.Config.Cluster.NodeID != "" {
		return a.Config.Cluster.NodeID
	}
	return "node-1"
}

// GetClusterSeeds 实现 module-cluster 的 ClusterConfigProvider（结构化接口，无需导入模块）。
// AUTO-FIX-2026-08-29 [P0-1]: 将集群种子节点暴露给 module-cluster，
// 原实现 seeds 硬编码为 nil，集群永远无法互相发现。
func (a *App) GetClusterSeeds() []string {
	if a.Config != nil {
		return a.Config.Cluster.Seeds
	}
	return nil
}

// GetClusterBindAddr 实现 module-cluster 的 ClusterConfigProvider（结构化接口）。
// 返回空字符串时模块使用默认监听地址 0.0.0.0:7946。
func (a *App) GetClusterBindAddr() string {
	if a.Config != nil {
		return a.Config.Cluster.BindAddr
	}
	return ""
}

// MigrateSessions 实现 module-cluster 的 SessionMigrationHook（结构化接口）。
// AUTO-FIX-2026-08-29 [P0-2]: 故障转移会话迁移的最小可行语义：
//
//  1. 故障节点持有的终端连接不在本节点，无法直接接管；
//  2. 本方法清理本节点可能残留的该终端失效登记（RemoveIfStale 仅清理
//     非活跃会话，不影响已在本节点重连的终端），避免重连被误判重复登录；
//  3. 终端重连后由 gossip 成员发现 + 负载均衡自然分配到目标节点。
func (a *App) MigrateSessions(deviceIDs []string, targetNodeID string) {
	if len(deviceIDs) == 0 || a.Sessions == nil {
		return
	}
	cleaned := 0
	for _, phone := range deviceIDs {
		if removed, status := a.Sessions.RemoveIfStale(phone); removed {
			cleaned++
		} else if status == "authenticated" {
			// 终端已在本节点活跃（重连先于故障通知到达），无需处理
			a.Logger.Debug("failover migrate: device already active locally, skip",
				zap.String("phone", phone))
		}
	}
	a.Logger.Warn("failover session migration executed",
		zap.Int("devices", len(deviceIDs)),
		zap.Int("stale_local_sessions_removed", cleaned),
		zap.String("target_node", targetNodeID))
}

// GetRouter returns the gin engine for modules that need to register API routes
// (module-monitor, module-adapter, module-crypto, module-ai, module-ai-nlp).
// Delegates to APIServer.GetEngine(). Returns nil if APIServer is not yet
// initialized (modules should handle nil gracefully ?routes simply won't register).
func (a *App) GetRouter() *gin.Engine {
	if a.APIServer == nil {
		return nil
	}
	return a.APIServer.GetEngine()
}

// GetConfigProvider returns a ConfigProvider interface for modules that cannot
// import the internal config package (module-ai, module-ai-nlp).
// *config.Config satisfies registry.ConfigProvider via its GetString method.
func (a *App) GetConfigProvider() registry.ConfigProvider {
	return a.Config
}

func (a *App) SetStore(store storage.Interface) {
	a.Storage = store
	// 同步初始?StorageLayers 容器（关系层至少存在?
	if a.StorageLayers == nil {
		a.StorageLayers = &storage.StorageLayers{}
	}
	a.StorageLayers.Relational = store
}

// SetStorageLayers v2.0 - ?module-storage 注入完整的多层存储容?
// 包含：关系层（DBStore? 时序层（TDengineStorage? 缓存层（RedisStorage? 对象层（MinIOStorage?
// 未启用的层为 nil，调用方需判空
// AUTO-FIX-2026-06-28: P0 修复 - 同步桥接?APIServer，否则存储分层管?API 运行时形同虚?
func (a *App) SetStorageLayers(layers *storage.StorageLayers) {
	a.StorageLayers = layers
	// 关系层同步到 v1.x ?Storage 字段，保持向后兼?
	if layers != nil && layers.Relational != nil {
		a.Storage = layers.Relational
	}
	// v3.0 - 桥接?API Server，启?/storage/stats?storage/ttl?storage/archive/* 等端?
	if a.APIServer != nil {
		a.APIServer.SetStorageLayers(layers)
	}
	// AUTO-FIX-2026-06-30 [P1-2/P1-5]: 注入 Redis 缓存层到 MessageHandler?
	// 鉴权结果缓存 24h（P1-2 鉴权风暴防护）、心跳超时清理在线状态（P1-5）?
	if layers != nil && layers.Cache != nil && a.msgHandler != nil {
		a.msgHandler.SetCacheStorage(layers.Cache)
	}
}

// SetArchiver v3.0 - ?module-storage 在归档器启动后注?
// 启用 /storage/archive/trigger 端点（手动触发一次离线归档）
// AUTO-FIX-2026-06-28: P0 修复 - 之前 Archiver 仅在 module-storage 内部启动，API 层无法触?
func (a *App) SetArchiver(archiver apiHandler.ArchiveRunner) {
	if a.APIServer != nil {
		a.APIServer.SetArchiver(archiver)
	}
}

// SetArchiveProgressProvider v3.0 - ?module-storage 注入归档进度查询回调
// 启用 /storage/archive/progress 端点查询归档实时进度和上次运行结?
// AUTO-FIX-2026-07-02: 新增方法以支持归档进度实时查?API
func (a *App) SetArchiveProgressProvider(p func() (any, any, bool)) {
	if a.APIServer != nil {
		a.APIServer.SetArchiveProgressProvider(apiHandler.ArchiveProgressProvider(p))
	}
}

// GetStorageLayers v2.0 - 获取多层存储容器
func (a *App) GetStorageLayers() *storage.StorageLayers {
	return a.StorageLayers
}

// GetAIModule 返回已注册的AI分析模块
func (a *App) GetAIModule() AIAnalyzer {
	return a.AIModule
}

// GetAINLPModule 返回已注册的AI对话模块
func (a *App) GetAINLPModule() AIChatter {
	return a.AINLPModule
}

// SetAIModule ?module-ai ?Init() 中调用以注册自身
func (a *App) SetAIModule(m interface{}) {
	if ai, ok := m.(AIAnalyzer); ok {
		a.AIModule = ai
		if a.APIServer != nil {
			a.APIServer.SetAIModule(ai)
		}
	}
}

// SetAINLPModule ?module-ai-nlp ?Init() 中调用以注册自身
func (a *App) SetAINLPModule(m interface{}) {
	if nlp, ok := m.(AIChatter); ok {
		a.AINLPModule = nlp
		if a.APIServer != nil {
			a.APIServer.SetAINLPModule(nlp)
		}
	}
}

func NewApp() (*App, error) {
	cfg, err := config.Load("")
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	logger, err := config.InitLogger(&cfg.Logging)
	if err != nil {
		return nil, fmt.Errorf("init logger: %w", err)
	}

	logger.Info("initializing JTE engine...", zap.String("version", Version))

	// [P1-可靠性] 生产环境强制安全校验
	// JTE_ENV=production 时，校验所有安全基线（密码强度、JWT 密钥、TLS 配置、CORS 等）
	// 任一校验失败则拒绝启动，防止以不安全配置运行生产环境
	if os.Getenv("JTE_ENV") == "production" {
		if err := cfg.ValidateForProduction(); err != nil {
			logger.Error("production config validation failed, refusing to start",
				zap.Error(err))
			return nil, fmt.Errorf("production validation: %w", err)
		}
		logger.Info("production config validation passed")
	}

	// AUTO-FIX-2026-06-30 [P1-6]: ?KMS 加载 JWT 密钥（env/file），禁止主配置明文存储?
	// kms_source=config 或空时回退到主配置（向后兼容）?
	if cfg.API.JWT != nil && cfg.API.JWT.KMSSource != "" && cfg.API.JWT.KMSSource != "config" {
		if err := api.InitJWTFromKMS(cfg.API.JWT, cfg.API.JWT.KMSSource, cfg.API.JWT.KMSFilePath, logger); err != nil {
			return nil, fmt.Errorf("init jwt from kms: %w", err)
		}
	}

	var store storage.Interface
	switch cfg.Storage.Type {
	case "sqlite":
		dbPath := cfg.Storage.DSN
		if dbPath == "" {
			dbPath = "./data/jte.db"
		}
		sqliteStore, err := sqlite.NewSQLiteStore(dbPath, logger)
		if err != nil {
			return nil, fmt.Errorf("init sqlite store: %w", err)
		}
		store = sqliteStore
		logger.Info("using SQLite storage", zap.String("path", dbPath))
	case "memory":
		// AUTO-FIX-2026-07-15 [ConvergeLoop-语义一致性]: 显式 case "memory"
		// 原先 default 分支?mysql/postgres/tdengine 等未实现的类型静默降级到 memory?
		// 导致 docker-compose.yml 配置 JTE_STORAGE_TYPE=mysql 时容器重启数据全丢?
		// 现在只有显式配置 memory 才使用内存存储；其他类型?default 显式报错?
		store = memory.NewMemoryStore(cfg.Gateway.MaxDevices)
		logger.Info("using in-memory storage")
	default:
		// AUTO-FIX-2026-07-15 [ConvergeLoop-语义一致性]: 显式拒绝不支持的存储类型
		// 避免静默降级?memory 导致数据丢失；mysql/postgres 待后续实?
		return nil, fmt.Errorf("unsupported storage type %q: only sqlite/memory supported, mysql/postgres not implemented",
			cfg.Storage.Type)
	}
	reg := registry.NewFeatureRegistry()
	protocolHub := protocol.NewHub(logger)
	sessions := gateway.NewSessionManager(logger)

	jt808Codec := jt808.NewCodec()
	protocolHub.RegisterCodec(jt808Codec)

	jt1078Codec := jt1078.NewCodec()
	protocolHub.RegisterCodec(jt1078Codec)

	mergeEngine := merge.NewEngine(store, logger, reg)
	// 启用存储批量写入：聚合到 1000 条或 100ms 超时后批量写入，
	// 替代默认单条写入，大幅提升高并发位置/报警写入吞吐?
	mergeEngine.EnableBatchWriters(1000, 100*time.Millisecond)

	limiter := gateway.NewLimiter(cfg.Gateway.MaxDevices, reg)

	handlerRegistry := handler.NewHandlerRegistry()

	tcpServer := gateway.NewTCPServer(
		&cfg.Gateway,
		logger,
		sessions,
		protocolHub,
		store,
		reg,
	)

	msgHandler := NewMessageHandler(store, sessions, mergeEngine, limiter, logger, protocolHub, handlerRegistry, nil)
	// AUTO-FIX-2026-06-30 [P0-1]: 注入鉴权码管理器（强随机鉴权?+ 防伪?防克隆）
	authCodeMgr := gateway.NewAuthCodeManager(logger)
	msgHandler.SetAuthCodeManager(authCodeMgr)
	tcpServer.SetMessageHandler(msgHandler.Handle)

	// 创建 809 服务端，用于接收下级平台?809 连接?
	// 通过 SetMessageHandler 将收到的 809 消息分发?HandlerRegistry 中的 Handler809?
	// 从而利?module-protocol-809 的完整消息处理能力（车辆增删改、报警、音视频、统计、路线等）?
	jt809Server := gateway.NewJT809Server(&cfg.Gateway, logger, mergeEngine, store)
	jt809Server.SetProtocolHub(protocolHub)
	// AUTO-FIX-2026-06-26: 注入下级平台接入鉴权账号列表（按第一?txt要求）[2026-06-26]
	jt809Server.SetDownstreamPlatforms(cfg.JT809.DownstreamPlatforms)
	jt809Server.SetMessageHandler(func(session handler.Session, msg *protocol.Message) error {
		h, ok := handlerRegistry.Get(protocol.ProtocolJT809)
		if !ok {
			return fmt.Errorf("809 handler not registered")
		}
		return h.HandleMessage(session, msg, protocolHub)
	})

	// 创建 809 客户端，连接上级平台并按转发规则自动转发 808 位置/报警数据?
	jt809Clients := make([]*gateway.JT809Client, 0, len(cfg.JT809.Platforms))
	for i := range cfg.JT809.Platforms {
		pcfg := &cfg.JT809.Platforms[i]
		client := gateway.NewJT809Client(pcfg, logger, mergeEngine, store)
		client.SetForwardRules(pcfg.ForwardRules)
		// FIXED-2026-07-23 [P2]: 从配置注入熔断器参数和缓冲区溢出告警开关
		client.SetCircuitBreakerConfig(
			cfg.JT809.CircuitBreaker.FailThreshold,
			cfg.JT809.CircuitBreaker.ResetTimeout,
			cfg.JT809.PendingBufferOverflowAlert,
		)

		if err := client.Connect(); err != nil {
			logger.Error("809 client connect failed, auto-forward disabled",
				zap.String("platform_id", pcfg.ID),
				zap.String("host", pcfg.Host),
				zap.Int("port", pcfg.Port),
				zap.Error(err))
		} else {
			if err := client.Login(); err != nil {
				logger.Error("809 client login failed, auto-forward disabled",
					zap.String("platform_id", pcfg.ID),
					zap.Error(err))
			} else {
				logger.Info("809 client connected and logged in",
					zap.String("platform_id", pcfg.ID))
			}
		}

		// 无论是否连接成功，都注册自动转发订阅?
		// StartAutoForward 内部会检查连接状态，未连接时跳过，重连成功后即可转发?
		client.StartAutoForward(mergeEngine.GetEventBus())
		jt809Clients = append(jt809Clients, client)
	}

	// 创建共享?CommandSender，使 API 层和消息处理层使用同一实例?
	// MessageHandler 收到终端 0x0001 通用应答时，通过回调唤醒 SendAndWait ?pending 队列?
	commandSender := apiHandler.NewCommandSender(sessions, logger)
	msgHandler.SetCommandRespCallback(commandSender.HandleGeneralResp)

	var apiServer *api.Server
	websiteClient := module.NewWebsiteClient(cfg.Website.APIURL)
	authManager := module.NewAuthManager(logger, websiteClient)

	configDir := cfg.Modules.Dir
	if configDir == "" {
		configDir = "./config"
	}
	licenseMgr := module.NewLicenseManager(logger, websiteClient, configDir, []byte(cfg.Auth.OfflineUnbindSecret))
	licenseMgr.SetEventBus(mergeEngine.GetEventBus())
	// AUTO-FIX-2026-06-30 [集成-6]: 注入授权配额校验器到消息处理器（设备注册时校验车辆数 ?max_vehicles?
	msgHandler.SetLicenseValidator(licenseMgr)
	rbacManager := module.NewRBACManager(configDir, logger)
	maintenanceMode := maintenance.NewMode(configDir, logger)
	// AUTO-FIX-2026-06-30 [P2-6]: 注入维护模式启停通知回调?
	// stopWrites 维护开始时广播 0x8103 通知终端"暂停上报"，维护结束时广播恢复?
	maintenanceMode.SetNotifyCallbacks(
		func(reason string) { broadcastReportingControl(sessions, commandSender, logger, false, reason) },
		func() { broadcastReportingControl(sessions, commandSender, logger, true, "") },
	)

	var auditLogger *audit.AuditLogger
	auditPath := configDir + "/audit.log"
	// AUTO-FIX-2026-07-02 [等保2.0]: 启用 HMAC-SM3 链式防篡改审计日?
	// HMAC 密钥优先使用 crypto.sm4_key ?SM3 摘要（复用现有密钥，避免新增配置）；
	// 未启用国密时使用固定密钥（开发环境，生产环境必须启用国密?
	var auditHMACKey string
	if cfg.Crypto.Enabled && cfg.Crypto.SM4Key != "" {
		// 使用 SM4 主密钥的 SM3 摘要作为 HMAC 密钥?2 字节 = 64 hex 字符?
		auditHMACKey = gmsm.SM3HashHex([]byte(cfg.Crypto.SM4Key))
		logger.Info("audit log HMAC-SM3 anti-tamper enabled (国密模式)")
	}
	al, err := audit.NewAuditLogger(auditPath, 100, logger, auditHMACKey)
	if err != nil {
		logger.Warn("audit logger init failed, audit logging disabled", zap.Error(err))
	} else {
		auditLogger = al
	}

	if cfg.API.Enabled {
		apiServer = api.NewServer(cfg, logger, store, sessions, mergeEngine, reg)
		distFS, err := fs.Sub(webFS, "web/dist")
		if err == nil {
			apiServer.SetWebFS(distFS)
		}
		apiServer.SetLicenseManager(licenseMgr)
		apiServer.SetMaintenanceMode(maintenanceMode)
		apiServer.SetRBACManager(rbacManager)
		apiServer.SetAuditLogger(auditLogger)
		apiServer.SetCommandSender(commandSender)

		// AUTO-FIX-2026-07-02 [等保2.0/防克隆]: 注入登录守卫
		// 失败 5 次锁?15 分钟；记录设备指?IP/地理位置，检测多IP/异地/新设备登?
		loginGuard := security.NewLoginGuard(security.DefaultLoginGuardConfig(), logger)
		apiServer.SetLoginGuard(loginGuard)
		logger.Info("login guard enabled (等保2.0 防克?",
			zap.Int("max_failures", 5),
			zap.Duration("lockout", 15*time.Minute))

		// AUTO-FIX-2026-07-02 [国密]: 注入 SM4-GCM 数据加密?
		// 启用国密时对手机?身份?车牌等敏感字段落库加密；未启用时透传明文（向后兼容）
		dataCipher, err := secret.NewDataCipher(cfg.Crypto.SM4Key, cfg.Crypto.Enabled)
		if err != nil {
			logger.Warn("data cipher init failed, sensitive data will be stored in plaintext",
				zap.Error(err))
		} else {
			apiServer.SetDataCipher(dataCipher)
			if cfg.Crypto.Enabled {
				logger.Info("SM4-GCM data cipher enabled (国密关键数据加密)")
			} else {
				logger.Info("data cipher disabled (plaintext mode, crypto.enabled=false)")
			}
		}

		if cfg.ZLMediaKit.URL != "" {
			mediaClient := media.NewZLMediaKitClient(&media.ZLMediaKitConfig{
				APIURL:      cfg.ZLMediaKit.URL,
				Secret:      cfg.ZLMediaKit.Secret,
				RTSPPort:    cfg.ZLMediaKit.RTSPPort,
				RTPPort:     cfg.ZLMediaKit.RTPPort,
				HTTPPort:    cfg.ZLMediaKit.HTTPPort,
				StreamIdle:  cfg.ZLMediaKit.StreamIdle,
				TcpMode:     cfg.ZLMediaKit.TcpMode,
			}, logger)
			apiServer.SetMediaClient(mediaClient)
			logger.Info("ZLMediaKit client configured", zap.String("url", cfg.ZLMediaKit.URL))

			// Create and start the 1078 video engine for RTP forwarding to ZLMediaKit.
			zlmHost := "127.0.0.1"
			if u, err := url.Parse(cfg.ZLMediaKit.URL); err == nil && u.Hostname() != "" {
				zlmHost = u.Hostname()
			}
			videoEngine := jt1078.NewVideoEngine(logger, zlmHost)
			if err := videoEngine.Start(); err != nil {
				logger.Warn("failed to start video engine", zap.Error(err))
			}
			msgHandler.SetVideoEngine(videoEngine)
			apiServer.SetVideoEngine(videoEngine)
			// AUTO-FIX-2026-06-26: 启动TCP-RTP监听?按配置设置流模式（按第一?txt要求）[2026-06-26]
			if cfg.Video.StreamPort > 0 {
				if err := videoEngine.StartStreamListener(cfg.Video.StreamPort); err != nil {
					logger.Warn("failed to start TCP stream listener", zap.Error(err))
				} else {
					logger.Info("TCP-RTP stream listener started", zap.Int("port", cfg.Video.StreamPort))
				}
			}
			if cfg.Video.RTPMode == "tcp" || cfg.Video.RTPMode == "auto" {
				videoEngine.SetDefaultStreamMode(cfg.Video.RTPMode)
				logger.Info("video default RTP stream mode applied", zap.String("mode", cfg.Video.RTPMode))
			}
			logger.Info("1078 video engine started", zap.String("zlm_host", zlmHost))
		}
	}

	moduleLoader := module.NewLoader(
		cfg.Modules.Dir,
		reg,
		logger,
		cfg.Modules.SignatureVerify,
	)
	// AUTO-FIX-2026-06-30 [集成-5]: 配置模块加载模式（plugin/process/auto?
	modeCfg := module.LoadModeConfig{
		Mode:            module.ParseLoadMode(cfg.Modules.LoadMode),
		ModuleBinDir:    cfg.Modules.BinDir,
		SocketDir:       cfg.Modules.SocketDir,
		StartTimeoutSec: cfg.Modules.StartTimeoutSec,
		StopTimeoutSec:  cfg.Modules.StopTimeoutSec,
	}
	if modeCfg.ModuleBinDir == "" {
		modeCfg.ModuleBinDir = filepath.Join(cfg.Modules.Dir, "bin")
	}
	if modeCfg.StartTimeoutSec == 0 {
		modeCfg.StartTimeoutSec = 10
	}
	if modeCfg.StopTimeoutSec == 0 {
		modeCfg.StopTimeoutSec = 5
	}
	moduleLoader.SetLoadMode(modeCfg.Mode, modeCfg)
	logger.Info("module load mode configured",
		zap.String("mode", module.SelectLoadMode(modeCfg.Mode).String()),
		zap.String("dir", cfg.Modules.Dir),
		zap.String("bin_dir", modeCfg.ModuleBinDir))
	// 联动：授权移除时自动停止并卸载对应模?
	licenseMgr.SetLoader(moduleLoader)

	// AUTO-FIX-2026-06-29 [P1-6]: ?JT809Client 注册为转发规则热更新回调?
	// 每个 JT809Client 实现?ReloadForwardRules()，ForwardRuleHandler ?API 变更?
	// 调用对应 platformID ?reloader 通知客户端重新加载内存快照?
	if apiServer != nil && len(jt809Clients) > 0 {
		reloaders := make(map[string]apiHandler.ForwardRuleReloader, len(jt809Clients))
		for _, c := range jt809Clients {
			reloaders[c.GetPlatformID()] = c
		}
		apiServer.SetForwardRuleReloaders(reloaders)

		// FIXED-2026-07-23 [P2]: 注入 809 客户端状态供健康检查端点使用
		statuses := make([]apiHandler.JT809ClientStatus, len(jt809Clients))
		for i, c := range jt809Clients {
			statuses[i] = c
		}
		apiServer.SetJT809ClientStatuses(statuses)
	}

	// AUTO-FIX-2026-06-29 [P1]: ?ctx 被丢弃（_ = ctx），组件无法感知进程停机?
	// Ctx 保存?App，供需?context 的组件使用；Stop() ?Cancel() 取消触发停机?
	ctx, cancel := context.WithCancel(context.Background())

	return &App{
		Config:          cfg,
		Logger:          logger,
		Storage:         store,
		Gateway:         tcpServer,
		JT809Server:     jt809Server,
		JT809Clients:    jt809Clients,
		Registry:        reg,
		Protocol:        protocolHub,
		Sessions:        sessions,
		Merge:           mergeEngine,
		APIServer:       apiServer,
		ModuleLoader:    moduleLoader,
		AuthManager:     authManager,
		LicenseMgr:      licenseMgr,
		RBACManager:     rbacManager,
		AuditLogger:     auditLogger,
		HandlerRegistry: handlerRegistry,
		Ctx:             ctx,
		Cancel:          cancel,
		// AUTO-FIX-2026-06-30 [P1-5]: 心跳超时资源清理回调，供 Start() 注入 HeartbeatChecker
		onSessionTimeout: msgHandler.HandleSessionTimeout,
		// AUTO-FIX-2026-06-30 [P1-2/P1-5]: 保存 msgHandler 引用，供 SetStorageLayers 延迟注入缓存
		msgHandler: msgHandler,
	}, nil
}

func (a *App) Start() error {
	a.Logger.Info("starting JTE engine...")

	// AUTO-FIX-2026-06-30 [集成-7]: OpenTelemetry 链路追踪初始化（可选，需 -tags otel 编译）?
	// 默认构建（无 otel tag）为 no-op，仅使用自研 trace_id 机制?
	// 配置?telemetry.otel_endpoint（如 "localhost:4318"）为空则跳过初始化?
	otelEndpoint := a.Config.GetString("telemetry.otel_endpoint")
	if otelEndpoint != "" {
		otelService := a.Config.GetString("telemetry.otel_service_name")
		if otelService == "" {
			otelService = "jte"
		}
		otelSample := 1.0
		if v := a.Config.GetString("telemetry.otel_sample_rate"); v != "" {
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				otelSample = f
			}
		}
		if err := trace.InitOTel(otelEndpoint, otelService, otelSample); err != nil {
			if errors.Is(err, trace.ErrOTelNotEnabled) {
				a.Logger.Info("OpenTelemetry endpoint configured but SDK not compiled in; using lightweight trace_id",
					zap.String("otel_endpoint", otelEndpoint))
			} else {
				a.Logger.Warn("OpenTelemetry initialization failed; falling back to lightweight trace_id",
					zap.Error(err))
			}
		} else {
			a.Logger.Info("OpenTelemetry SDK initialized",
				zap.String("endpoint", otelEndpoint),
				zap.String("service", otelService),
				zap.Float64("sample_rate", otelSample))
		}
	}

	// AUTO-FIX-2026-06-30 [集成-6]: TDengine 建库前校?vgroups/replica 配额
	if a.LicenseMgr != nil && a.Config.Storage.TimeSeries.VGroups > 0 {
		if err := a.LicenseMgr.ValidateVGroups(a.Config.Storage.TimeSeries.VGroups); err != nil {
			a.Logger.Error("TDengine vgroups exceeds license limit, aborting startup",
				zap.Int("configured_vgroups", a.Config.Storage.TimeSeries.VGroups),
				zap.Error(err))
			return fmt.Errorf("license validation failed: %w", err)
		}
	}
	if a.LicenseMgr != nil && a.Config.Storage.TimeSeries.Replica > 0 {
		if err := a.LicenseMgr.ValidateReplica(a.Config.Storage.TimeSeries.Replica); err != nil {
			a.Logger.Error("TDengine replica exceeds license limit, aborting startup",
				zap.Int("configured_replica", a.Config.Storage.TimeSeries.Replica),
				zap.Error(err))
			return fmt.Errorf("license validation failed: %w", err)
		}
	}

	// 809 等试用支持模块：首次加载时自动开?30 天试用（绑定机器指纹，防重置?
	a.LicenseMgr.AutoStartTrials()

	// FIXED-2026-07-24: 注册所有已授权/试用的模块到 feature registry
	// 在模块二进制未编译（如 Windows 开发环境）时，已授权模块仍显示为"已启用"
	if status, ok := a.LicenseMgr.GetStatus().(*module.LicenseStatus); ok && len(status.ActiveModules) > 0 {
		moduleFeatureMap := map[string]registry.Feature{
			"jt808":          registry.FeatureJT808,
			"jt1078":         registry.FeatureJT1078,
			"protocol_809":   registry.FeatureProtocol809,
			"protocol_1045":  registry.FeatureProtocol1045,
			"protocol_905":   registry.FeatureProtocol905,
			"storage":        registry.FeatureDBStorage,
			"ai":             registry.FeatureAI,
			"ai_nlp":         registry.FeatureAINLP,
		}
		for _, modName := range status.ActiveModules {
			if feat, exists := moduleFeatureMap[modName]; exists {
				a.Registry.Register(feat)
				a.Logger.Info("registered licensed module in feature registry",
					zap.String("module", modName))
			}
		}
	}
	// FIXED-2026-07-24: 基础设施模块（无独立授权，随核心引擎启用）也注册到 registry
	// 这些模块的二进制未单独编译，但核心功能已内置在主程序中
	infraModules := []registry.Feature{
		registry.FeatureProtocol1253,  // JT/T 1253 道路运输
		registry.FeatureProtocol32960, // GB/T 32960 新能源车
		registry.FeatureCrypto,        // 国密加密
		registry.FeatureAdapter,       // 协议适配器
		registry.FeatureCluster,       // 集群管理
		registry.FeatureMonitor,       // 监控告警
		registry.FeatureLegacy,        // 兼容旧版协议
	}
	for _, feat := range infraModules {
		a.Registry.Register(feat)
	}
	a.Logger.Info("infrastructure modules registered in feature registry",
		zap.Int("count", len(infraModules)))

	if err := a.ModuleLoader.LoadAll(); err != nil {
		a.Logger.Warn("some modules failed to load", zap.Error(err))
	}

	if err := a.ModuleLoader.InitAll(a); err != nil {
		a.Logger.Warn("some modules failed to init", zap.Error(err))
	}

	if err := a.ModuleLoader.StartAll(); err != nil {
		a.Logger.Warn("some modules failed to start", zap.Error(err))
	}

	// AUTO-FIX-2026-06-30 [集成-4]: 启动模块崩溃自动重启 supervisor
	// 后台周期性探活（HealthModule.Health()），崩溃模块自动重启（最?3 ?小时，指数退避）?
	a.ModuleLoader.StartSupervisor()

	// AUTO-FIX-2026-06-26: 所有模块启动后注入 AI 报警过滤器到支持的协?Handler
	// ?StartAll 之后执行，确?module-ai 已注?AIModule，避免依?map 遍历顺序的不确定性?
	// 仅当 AIModule 实现?handler.AlarmFilter 接口时注入（module-ai ?AIModule 自动满足）?
	if a.AIModule != nil {
		if af, ok := a.AIModule.(handler.AlarmFilter); ok {
			for _, pt := range a.HandlerRegistry.ListHandlers() {
				if h, ok := a.HandlerRegistry.Get(pt); ok {
					if setter, ok := h.(handler.AlarmFilterSetter); ok {
						setter.SetAlarmFilter(af)
						a.Logger.Info("AI alarm filter injected into protocol handler",
							zap.String("protocol", string(pt)))
					}
				}
			}
		} else {
			a.Logger.Debug("AIModule does not implement handler.AlarmFilter, skipping injection")
		}
	}

	// AUTO-FIX-2026-07-02 [P1]: ?809 协议 Handler 注入转发规则检查器?
	// 模块已在上一?InitAll 中注?Handler809，此处通过类型断言注入 ForwardChecker 适配器?
	// 适配器聚合所有上级平?JT809Client?09 Handler 在发布音视频转发事件?
	// 调用 ShouldForward 检查持久化转发规则（按车辆/消息类型/时间?源平台过滤）?
	// 未配置上级平台（jt809Clients 为空）时跳过注入?09 Handler 回退到旧行为?
	if len(a.JT809Clients) > 0 {
		fc := newForwardCheckerAdapter(a.JT809Clients)
		for _, pt := range a.HandlerRegistry.ListHandlers() {
			if h, ok := a.HandlerRegistry.Get(pt); ok {
				if setter, ok := h.(handler.ForwardCheckerSetter); ok {
					setter.SetForwardChecker(fc)
					a.Logger.Info("forward checker injected into protocol handler",
						zap.String("protocol", string(pt)),
						zap.Int("upstream_clients", len(a.JT809Clients)))
				}
			}
		}
	}

	if err := a.Gateway.Start(); err != nil {
		return fmt.Errorf("start tcp server: %w", err)
	}

	// 启动 809 服务端（接收下级平台连接），仅在配置?server_port 时启动?
	// 模块已在上一步加载完毕，809 codec ?Handler809 已注册到 Hub ?HandlerRegistry?
	if a.JT809Server != nil && a.Config.JT809.ServerPort > 0 {
		if err := a.JT809Server.Start(a.Config.JT809.ServerPort); err != nil {
			a.Logger.Error("failed to start 809 server", zap.Int("port", a.Config.JT809.ServerPort), zap.Error(err))
		}
	}

	heartbeatChecker := gateway.NewHeartbeatChecker(
		time.Duration(a.Config.Gateway.HeartbeatInterval)*time.Second,
		time.Duration(a.Config.Gateway.HeartbeatTimeout)*time.Second,
		a.Sessions,
		a.Logger,
	)
	// AUTO-FIX-2026-06-30 [P1-5]: 注入心跳超时资源清理回调
	heartbeatChecker.SetTimeoutHook(a.onSessionTimeout)
	heartbeatChecker.Start()

	a.LicenseMgr.StartValidation()

	if a.APIServer != nil {
		util.SafeGo(a.Logger, "main.apiServer", func() {
			if err := a.APIServer.Start(); err != nil {
				a.Logger.Error("API server error", zap.Error(err))
			}
		})
	}

		protocols := make([]string, 0, len(a.Protocol.ListCodecs()))
		for _, p := range a.Protocol.ListCodecs() {
			protocols = append(protocols, string(p))
		}
		a.Logger.Info("JTE engine started",
		zap.Int("tcp_port", a.Config.Gateway.TCPPort),
		zap.Int("api_port", a.Config.API.Port),
		zap.Int("max_devices", a.Config.Gateway.MaxDevices),
		zap.Strings("protocols", protocols),
		zap.Int("modules_loaded", len(a.ModuleLoader.List())))

	return nil
}

func (a *App) Stop() {
	a.Logger.Info("stopping JTE engine...")

	// AUTO-FIX-2026-06-30 [集成-4]: 先停?supervisor，避免重启正在停止的模块
	a.ModuleLoader.StopSupervisor()
	a.ModuleLoader.StopAll()
	a.LicenseMgr.StopValidation()

	// AUTO-FIX-2026-06-30 [集成-7]: 优雅关闭 OpenTelemetry SDK，flush 残留 span?
	if err := trace.ShutdownOTel(); err != nil {
		a.Logger.Warn("OpenTelemetry shutdown error", zap.Error(err))
	}

	// AUTO-FIX-2026-06-29 [P1]: 优雅停机 API server——关?HTTP listener + WS Hub?
	// ?Publish shutdown 事件通知前端（best-effort，WS Hub 即将停止），
	// 再调?APIServer.Stop(ctx) 关闭 listener 等待在途请?+ 停止 WS Hub goroutine?
	if a.APIServer != nil {
		a.APIServer.GetWSHub().Publish(string(merge.EventTypeSystemEvent), "shutdown", nil)
		// AUTO-FIX-2026-06-30 [P1-2]: API 停机超时?config.Shutdown 读取（默?10s）?
		apiTimeout := a.Config.Shutdown.APIShutdownTimeout()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), apiTimeout)
		if err := a.APIServer.Stop(shutdownCtx); err != nil {
			a.Logger.Warn("API server shutdown error", zap.Error(err))
		}
		cancel()
	}

	a.Gateway.Stop()
	if a.JT809Server != nil {
		a.JT809Server.Stop()
	}
	for _, c := range a.JT809Clients {
		c.Disconnect()
	}
	if a.AuditLogger != nil {
		a.AuditLogger.Close()
	}
	if a.Merge != nil {
		a.Merge.Stop() // 刷新批量写入器剩余数据并停止
	}
	// 关闭多层存储：TDengine/Redis/MinIO（v2.0 StorageLayers?
	if a.StorageLayers != nil {
		if a.StorageLayers.TimeSeries != nil {
			if err := a.StorageLayers.TimeSeries.Close(); err != nil {
				a.Logger.Warn("close time series storage failed", zap.Error(err))
			}
		}
		if a.StorageLayers.Cache != nil {
			if err := a.StorageLayers.Cache.Close(); err != nil {
				a.Logger.Warn("close cache storage failed", zap.Error(err))
			}
		}
		if a.StorageLayers.Object != nil {
			if err := a.StorageLayers.Object.Close(); err != nil {
				a.Logger.Warn("close object storage failed", zap.Error(err))
			}
		}
	}
	a.Storage.Close()
	a.Cancel()
	a.Logger.Info("JTE engine stopped")
}

// GracefulShutdown 优雅停机（对?jte-plan-final-v3.0.md ?A.6.1 节）
// 流程：拒绝新连接 ?下发重连退??等待连接排空（最?DrainTimeout）→ 强制关闭 ?关闭存储
// AUTO-FIX-2026-06-30 [P1-2]: 超时与退避参数从 config.Shutdown 读取，替换硬编码值?
func (a *App) GracefulShutdown() {
	shutdownCfg := a.Config.Shutdown
	drainTimeout := shutdownCfg.DrainTimeout()
	checkInterval := shutdownCfg.DrainCheckInterval()

	a.Logger.Info("graceful shutdown started",
		zap.Duration("drain_timeout", drainTimeout),
		zap.Duration("check_interval", checkInterval),
		zap.Int("reconnect_backoff_max", shutdownCfg.ReconnectBackoffMax()))

	// 1. 拒绝新连接（关闭 listener，保留现有连接继续服务）
	if a.Gateway != nil {
		a.Gateway.StopAccept()
	}

	// 2. 向所有已连接设备下发 0x8001 重连退避时间（0~ReconnectBackoffMax 随机?
	a.broadcastReconnectBackoff()

	// 3. 等待现有连接完成，最?drainTimeout
	deadlineCh := time.After(drainTimeout)
	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			remaining := len(a.Sessions.List())
			if remaining == 0 {
				a.Logger.Info("all connections drained")
				a.Stop()
				return
			}
			a.Logger.Info("waiting for connections to drain",
				zap.Int("remaining", remaining))
		case <-deadlineCh:
			remaining := len(a.Sessions.List())
			a.Logger.Warn("graceful shutdown deadline reached, force closing connections",
				zap.Int("remaining", remaining),
				zap.Duration("drain_timeout", drainTimeout))
			// 4. 超时后强制关闭剩余连?+ 5. 关闭存储 + 6. 进程退出（由调用方 os.Exit?
			a.Stop()
			return
		}
	}
}

// broadcastReconnectBackoff 向所有已注册设备下发 0x8001，RespResult 携带随机退避时间?
// AUTO-FIX-2026-06-30 [P1-2]: 退避上限从 config.Shutdown.ReconnectBackoffMaxSeconds 读取
// （默?300s 安全场景；蓝绿部署可配置 60s 加速重连）?
// 用于服务端停?重启时让设备分散重连，避免重启后所有设备同时鉴权压垮服务端?
func (a *App) broadcastReconnectBackoff() {
	if a.Sessions == nil {
		return
	}
	backoffMax := a.Config.Shutdown.ReconnectBackoffMax()
	if backoffMax <= 0 {
		backoffMax = 300
	}
	cs := apiHandler.NewCommandSender(a.Sessions, a.Logger)
	sessions := a.Sessions.List()
	sent := 0
	for _, sess := range sessions {
		phone := sess.GetPhone()
		if phone == "" {
			continue
		}
		// 随机退?0~backoffMax 秒，放入 RespResult 字节
		backoff := byte(rand.Intn(backoffMax + 1))
		msg := &jt808.TerminalGeneralRespMessage{
			RespSeqNum: 0,
			RespMsgID:  0,
			RespResult: backoff,
		}
		if err := cs.SendToDevice(phone, jt808.MsgIDGeneralResp, msg); err != nil {
			a.Logger.Debug("send reconnect backoff failed",
				zap.String("phone", phone), zap.Error(err))
			continue
		}
		sent++
	}
	a.Logger.Info("reconnect backoff broadcasted",
		zap.Int("total_sessions", len(sessions)),
		zap.Int("sent", sent),
		zap.Int("backoff_max_seconds", backoffMax))
}

// broadcastReportingControl 广播 0x8103 通知所有终端暂?恢复上报?
// AUTO-FIX-2026-06-30 [P2-6]: 维护模式 stopWrites=true 时调用，
// 让终端暂停位?报警上报，避免维护期间数据丢失在写入失败重试中?
// resume=true 恢复上报，resume=false 暂停上报?
func broadcastReportingControl(sessions *gateway.SessionManager, cs *apiHandler.CommandSender, logger *zap.Logger, resume bool, reason string) {
	if sessions == nil || cs == nil {
		return
	}
	action := "pause"
	if resume {
		action = "resume"
	}

	// 0x8103 CommandMessage：设置自定义参数 0x0090（上报控制）
	// 0x0090 = 0x01 暂停上报?x0090 = 0x00 恢复上报
	// 使用厂商扩展参数 ID，标准终端可忽略不支持的参数
	paramVal := byte(0x01) // 暂停
	if resume {
		paramVal = byte(0x00) // 恢复
	}

	list := sessions.List()
	sent := 0
	for _, sess := range list {
		phone := sess.GetPhone()
		if phone == "" {
			continue
		}
		msg := &jt808.CommandMessage{
			Params: map[uint32][]byte{
				0x0090: {paramVal}, // 自定义参数：上报控制
			},
		}
		if err := cs.SendToDevice(phone, jt808.MsgIDCommand, msg); err != nil {
			logger.Debug("send reporting control failed",
				zap.String("phone", phone),
				zap.String("action", action),
				zap.Error(err))
			continue
		}
		sent++
	}
	logger.Info("reporting control broadcasted",
		zap.String("action", action),
		zap.String("reason", reason),
		zap.Int("total_sessions", len(list)),
		zap.Int("sent", sent))
}

func (a *App) WaitForSignal() {
	sigCh := make(chan os.Signal, 1)
	// P2-FIX: Windows 不支持 SIGTERM/SIGHUP，仅注册 SIGINT + os.Interrupt
	if runtime.GOOS == "windows" {
		signal.Notify(sigCh, os.Interrupt)
	} else {
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	}
	for {
		sig := <-sigCh
		if sig == syscall.SIGHUP && runtime.GOOS != "windows" {
			a.Logger.Info("received SIGHUP, reloading configuration...")
			a.reloadConfig()
			continue
		}
		a.Logger.Info("received signal", zap.String("signal", sig.String()))
		return
	}
}

func (a *App) reloadConfig() {
	newCfg, err := config.Load("")
	if err != nil {
		a.Logger.Error("failed to reload config", zap.Error(err))
		return
	}

	applied := a.Config.ApplyHotReload(newCfg)
	for _, key := range applied {
		a.Logger.Info("config hot-reloaded", zap.String("key", key))
	}

	nonReloadable := a.Config.GetNonReloadableChanges(newCfg)
	for _, change := range nonReloadable {
		a.Logger.Warn("config change requires restart", zap.String("change", change))
	}

	if len(applied) > 0 && (applied[0] == "logging.level" || applied[0] == "logging.format") {
			newLogger, err := config.InitLogger(&a.Config.Logging)
			if err == nil {
				a.Logger.Info("logger reconfiguring")
				oldLogger := a.Logger
				a.Logger = newLogger
				if a.APIServer != nil {
					a.APIServer.SetLogger(newLogger)
				}
				if a.ModuleLoader != nil {
					a.ModuleLoader.SetLogger(newLogger)
				}
				if a.Gateway != nil {
					a.Gateway.SetLogger(newLogger)
				}
				a.Logger.Info("logger reconfigured successfully")
				_ = oldLogger.Sync()
			}
		}
}

func main() {
	// 顶层 panic 兜底：确保任何未捕获的 panic 不会静默崩溃，
	// 记录堆栈后退出码 1，便于运维定位根因。
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "FATAL: unrecovered panic: %v\n", r)
			debug.PrintStack()
			os.Exit(1)
		}
	}()

	if len(os.Args) > 1 {
		if err := runCLI(os.Args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	app, err := NewApp()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to initialize: %v\n", err)
		os.Exit(1)
	}

	// 确保退出前刷写日志缓冲区并关闭文件句柄，防止日志丢失
	defer func() {
		_ = app.Logger.Sync()
		config.CloseLogger()
	}()

	if err := app.Start(); err != nil {
		app.Logger.Fatal("failed to start", zap.Error(err))
	}

	app.WaitForSignal()
	// 优雅停机：摘流 -> 下发退避 -> 排空 -> 强制关闭 -> 关闭存储
	app.GracefulShutdown()
}

func runCLI(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("no command specified")
	}

	switch args[0] {
	case "auth":
		return runAuthCLI(args[1:])
	case "module":
		return runModuleCLI(args[1:])
	case "chat":
		return runChatCLI(args[1:])
	case "simulate":
		return runSimulateCLI(args[1:])
	case "migrate":
		return runMigrateCLI(args[1:])
	case "maintenance":
		return runMaintenanceCLI(args[1:])
	case "version":
		fmt.Printf("JTE %s\n", Version)
		return nil
	case "help", "--help", "-h":
		printHelp()
		return nil
	default:
		return fmt.Errorf("unknown command: %s", args[0])
	}
}

func runAuthCLI(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: jte auth <activate|unbind|remove|status> [args]")
	}

	cfg, _ := config.Load("")
	logger, _ := config.InitLogger(&cfg.Logging)
	client := module.NewWebsiteClient(cfg.Website.APIURL)
	configDir := cfg.Modules.Dir
	if configDir == "" {
		configDir = "./config"
	}
	licenseMgr := module.NewLicenseManager(logger, client, configDir, []byte(cfg.Auth.OfflineUnbindSecret))

	switch args[0] {
	case "activate":
		if len(args) < 2 {
			return fmt.Errorf("usage: jte auth activate <license_key>")
		}
		if err := licenseMgr.Activate(args[1]); err != nil {
			return fmt.Errorf("activation failed: %w", err)
		}
		fmt.Println("License activated successfully")
	case "unbind":
		if len(args) < 2 {
			return fmt.Errorf("usage: jte auth unbind <license_id> [--offline]")
		}
		licenseID := args[1]
		offline := false
		for _, a := range args[2:] {
			if a == "--offline" {
				offline = true
			}
		}
		cert, err := licenseMgr.Unbind(licenseID, offline)
		if err != nil {
			return fmt.Errorf("unbind failed: %w", err)
		}
		if cert != "" {
			fmt.Println("License unbound successfully (offline mode).")
			fmt.Println("Please send the following unbind certificate to the official website to complete unbinding:")
			fmt.Println(cert)
		} else {
			fmt.Println("License unbound successfully")
		}
	case "remove":
		if len(args) < 2 {
			return fmt.Errorf("usage: jte auth remove <license_id>")
		}
		if err := licenseMgr.Remove(args[1]); err != nil {
			return fmt.Errorf("remove failed: %w", err)
		}
		fmt.Println("License removed successfully")
	case "status":
		status := licenseMgr.GetStatus()
		if ls, ok := status.(*module.LicenseStatus); ok {
			fmt.Printf("Machine Fingerprint: %s\n", ls.MachineFingerprint)
			fmt.Printf("Active Modules: %v\n", ls.ActiveModules)
			if len(ls.Licenses) == 0 {
				fmt.Println("No licenses activated")
			} else {
				fmt.Println("\nLicenses:")
				for _, lic := range ls.Licenses {
					expired := ""
					if lic.Expired {
						expired = " [EXPIRED]"
					}
					fmt.Printf("  ID: %s  Modules: %v  Expires: %s%s\n",
						lic.ID, lic.Modules, lic.ExpiresAt.Format("2006-01-02"), expired)
				}
			}
			if len(ls.Trials) > 0 {
				fmt.Println("\nTrials:")
				for mod, trial := range ls.Trials {
					tStatus := "Active"
					if trial.IsExpired() {
						tStatus = "Expired"
					}
					fmt.Printf("  %s: %s (expires %s, %d days remaining)\n",
						mod, tStatus, trial.ExpiresAt.Format("2006-01-02"), trial.RemainingDays())
				}
			}
		} else {
			fmt.Println("License status unavailable")
		}
	case "trial":
		if len(args) < 2 {
			return fmt.Errorf("usage: jte auth trial <module_name>")
		}
		if err := licenseMgr.StartTrial(args[1]); err != nil {
			return fmt.Errorf("start trial failed: %w", err)
		}
		fmt.Printf("Trial for %s started successfully (30 days)\n", args[1])
	default:
		return fmt.Errorf("unknown auth command: %s", args[0])
	}
	return nil
}

func runModuleCLI(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: jte module <pull|install|list> [args]")
	}

	cfg, _ := config.Load("")
	logger, _ := config.InitLogger(&cfg.Logging)

	switch args[0] {
	case "list":
		loader := module.NewLoader(cfg.Modules.Dir, registry.NewFeatureRegistry(), logger, false)
		loader.LoadAll()
		modules := loader.List()
		if len(modules) == 0 {
			fmt.Println("No modules installed")
			return nil
		}
		for _, m := range modules {
			fmt.Printf("  %-25s v%-8s %s\n", m.Name, m.Version, m.Status)
		}
	case "pull":
		if len(args) < 2 {
			return fmt.Errorf("usage: jte module pull <module-name> [version]")
		}
		moduleName := args[1]
		version := "latest"
		if len(args) >= 3 {
			version = args[2]
		}
		return pullModule(cfg, logger, moduleName, version)
	case "install":
		if len(args) < 2 {
			return fmt.Errorf("usage: jte module install <module-name>")
		}
		return installModule(cfg, logger, args[1])
	default:
		return fmt.Errorf("unknown module command: %s", args[0])
	}
	return nil
}

func runMigrateCLI(args []string) error {
	cfg, _ := config.Load("")
	logger, _ := config.InitLogger(&cfg.Logging)

	srcDriver := "mysql"
	srcDSN := ""
	tgtDriver := "postgres"
	tgtDSN := ""
	batchSize := 1000
	dryRun := false
	verify := false
	configDir := cfg.Modules.Dir
	if configDir == "" {
		configDir = "./config"
	}

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--src-driver":
			if i+1 < len(args) { srcDriver = args[i+1]; i++ }
		case "--src-dsn":
			if i+1 < len(args) { srcDSN = args[i+1]; i++ }
		case "--tgt-driver":
			if i+1 < len(args) { tgtDriver = args[i+1]; i++ }
		case "--tgt-dsn":
			if i+1 < len(args) { tgtDSN = args[i+1]; i++ }
		case "--batch":
			if i+1 < len(args) { fmt.Sscanf(args[i+1], "%d", &batchSize); i++ }
		case "--dry-run":
			dryRun = true
		case "--verify":
			verify = true
		case "--help", "-h":
			fmt.Println("Usage: jte migrate [options]")
			fmt.Println()
			fmt.Println("Options:")
			fmt.Println("  --src-driver <driver>  Source database driver (default: mysql)")
			fmt.Println("  --src-dsn <dsn>        Source database DSN")
			fmt.Println("  --tgt-driver <driver>  Target database driver (default: postgres)")
			fmt.Println("  --tgt-dsn <dsn>        Target database DSN")
			fmt.Println("  --batch <size>         Batch size (default: 1000)")
			fmt.Println("  --dry-run              Simulate migration without writing")
			fmt.Println("  --verify               Verify migration results")
			return nil
		}
	}

	if srcDSN == "" {
		return fmt.Errorf("source DSN is required (--src-dsn)")
	}
	if tgtDSN == "" {
		return fmt.Errorf("target DSN is required (--tgt-dsn)")
	}

	migrator := migration.NewMigrator(&migration.MigratorConfig{
		SourceDriver: srcDriver,
		SourceDSN:    srcDSN,
		TargetDriver: tgtDriver,
		TargetDSN:    tgtDSN,
		BatchSize:    batchSize,
		DryRun:       dryRun,
		ConfigDir:    configDir,
	}, logger)

	if err := migrator.Connect(); err != nil {
		return fmt.Errorf("connect databases: %w", err)
	}
	defer migrator.Close()

	if verify {
		fmt.Println("Verifying migration...")
		if err := migrator.Verify(); err != nil {
			return fmt.Errorf("verification failed: %w", err)
		}
		return nil
	}

	fmt.Printf("Migrating from %s to %s (batch: %d, dry-run: %v)\n", srcDriver, tgtDriver, batchSize, dryRun)
	if err := migrator.Migrate(); err != nil {
		return fmt.Errorf("migration failed: %w", err)
	}

	fmt.Println("Migration completed successfully!")
	return nil
}

func runMaintenanceCLI(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: jte maintenance <start|stop|status> [reason]")
	}

	cfg, _ := config.Load("")
	logger, _ := config.InitLogger(&cfg.Logging)
	configDir := cfg.Modules.Dir
	if configDir == "" {
		configDir = "./config"
	}

	mgr := maintenance.NewMode(configDir, logger)

	switch args[0] {
	case "start":
		reason := "scheduled maintenance"
		stopWrites := false
		// 解析参数?-stop-writes 标志 + reason 文本
		// 用法：jte maintenance start [--stop-writes] [reason...]
		var reasonParts []string
		for _, a := range args[1:] {
			if a == "--stop-writes" || a == "--stop-writes=true" {
				stopWrites = true
			} else {
				reasonParts = append(reasonParts, a)
			}
		}
		if len(reasonParts) > 0 {
			reason = strings.Join(reasonParts, " ")
		}
		if err := mgr.Start(reason, stopWrites); err != nil {
			return err
		}
		fmt.Printf("Maintenance mode started: %s (stop_writes=%v)\n", reason, stopWrites)
	case "stop":
		if err := mgr.Stop(); err != nil {
			return err
		}
		fmt.Println("Maintenance mode stopped")
	case "status":
		status := mgr.GetStatus()
		if status.Active {
			fmt.Printf("Maintenance: ACTIVE\n")
			fmt.Printf("Reason: %s\n", status.Reason)
			fmt.Printf("Started: %s\n", status.StartedAt.Format("2006-01-02 15:04:05"))
		} else {
			fmt.Println("Maintenance: INACTIVE")
		}
	default:
		return fmt.Errorf("unknown maintenance command: %s", args[0])
	}
	return nil
}

func printHelp() {
	fmt.Println("JTE - JT Engine v" + Version)
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  jte              Start JTE engine")
	fmt.Println("  jte auth activate <license_key> Activate license key")
	fmt.Println("  jte auth unbind <license_id> [--offline]  Unbind license key (offline generates a cert)")
	fmt.Println("  jte auth remove <license_id>     Remove license key")
	fmt.Println("  jte auth trial <module>          Start module trial (e.g. protocol_809)")
	fmt.Println("  jte auth status                  Check auth status")
	fmt.Println("  jte module list                List installed modules")
	fmt.Println("  jte module pull                Pull modules from server")
	fmt.Println("  jte module install             Install pulled modules")
	fmt.Println("  jte simulate 808 [options]     Simulate JT808 terminals")
	fmt.Println("  jte simulate 1078 [options]    Simulate JT1078 terminals")
	fmt.Println("  jte migrate [options]          Migrate data between databases")
	fmt.Println("  jte maintenance start [reason] Enter maintenance mode")
	fmt.Println("  jte maintenance stop           Exit maintenance mode")
	fmt.Println("  jte maintenance status         Check maintenance status")
	fmt.Println("  jte chat                       Start interactive chat session")
	fmt.Println("  jte version                    Show version")
	fmt.Println("  jte help                       Show this help")
}

func runChatCLI(args []string) error {
	fmt.Println("JTE Chat v" + Version)
	fmt.Println("Type 'exit' or 'quit' to end the session.")
	fmt.Println("Type 'help' for available commands.")
	fmt.Println()

	scanner := bufio.NewScanner(os.Stdin)

	for {
		fmt.Print("jte> ")
		if !scanner.Scan() {
			break
		}
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}
		if input == "exit" || input == "quit" {
			fmt.Println("Goodbye!")
			break
		}
		if input == "help" {
			printChatHelp()
			continue
		}

		resp := fallbackChat(input)
		if resp != "" {
			fmt.Println(resp)
		}
		fmt.Println()
	}

	return nil
}

func fallbackChat(query string) string {
	lower := strings.ToLower(query)
	if strings.Contains(lower, "在线") || strings.Contains(lower, "设备") {
		return "当前在线设备：请启动JTE引擎后查看实时数据"
	}
	if strings.Contains(lower, "报警") {
		return "今日报警：请启动JTE引擎后查看实时数据"
	}
	if strings.Contains(lower, "协议") {
		return "JTE支持以下协议: JT/T 808, JT/T 809, JT/T 1078, JT/T 1045, JT/T 905, JT/T 1253, GB/T 32960"
	}
	return "我是JTE智能助手。请先启动JTE引擎(jte)以启用完整功能，或输入'help'查看可用命令。"
}


func printChatHelp() {
	fmt.Println("Available commands:")
	fmt.Println("  在线设备  - 查看在线设备列表")
	fmt.Println("  报警统计  - 查看报警统计")
	fmt.Println("  协议支持  - 查看支持的协议")
	fmt.Println("  help      - 显示帮助")
	fmt.Println("  exit/quit - 退出聊天")
}

func pullModule(cfg *config.Config, logger *zap.Logger, moduleName, version string) error {
	moduleDir := cfg.Modules.Dir
	if moduleDir == "" {
		moduleDir = "./modules"
	}

	cacheDir := moduleDir + "/.cache"
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return fmt.Errorf("create cache dir: %w", err)
	}

	repoURL := cfg.Modules.Registry
	if repoURL == "" {
		repoURL = "https://modules.jte.dev"
	}

	fmt.Printf("Pulling module %s@%s from %s...\n", moduleName, version, repoURL)

	licenseKey := cfg.Auth.LicenseKey
	if licenseKey == "" {
		return fmt.Errorf("authorization required. Run 'jte auth activate' first")
	}

	downloadURL := fmt.Sprintf("%s/api/v1/modules/%s/download?version=%s&license=%s", repoURL, moduleName, version, licenseKey)

	resp, err := http.Get(downloadURL)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("download failed: status %d, body: %s", resp.StatusCode, string(body))
	}

	archivePath := fmt.Sprintf("%s/%s_%s.tar.gz", cacheDir, moduleName, version)
	f, err := os.Create(archivePath)
	if err != nil {
		return fmt.Errorf("create archive file: %w", err)
	}
	defer f.Close()

	written, err := io.Copy(f, resp.Body)
	if err != nil {
		os.Remove(archivePath)
		return fmt.Errorf("write archive: %w", err)
	}

	fmt.Printf("Downloaded %d bytes to %s\n", written, archivePath)

	sigURL := downloadURL + "&type=sig"
	sigResp, sigErr := http.Get(sigURL)
	if sigErr == nil && sigResp.StatusCode == http.StatusOK {
		sigPath := fmt.Sprintf("%s/%s_%s.sig", cacheDir, moduleName, version)
		sigFile, sigFileErr := os.Create(sigPath)
		if sigFileErr == nil {
			sigWritten, _ := io.Copy(sigFile, sigResp.Body)
			sigFile.Close()
			fmt.Printf("Downloaded signature %d bytes to %s\n", sigWritten, sigPath)
		}
		sigResp.Body.Close()
	} else {
		fmt.Printf("Warning: signature file not available for %s\n", moduleName)
		if sigResp != nil {
			sigResp.Body.Close()
		}
	}

	fmt.Printf("Module %s@%s pulled successfully.\n", moduleName, version)
	fmt.Printf("Run 'jte module install %s' to install.\n", moduleName)
	return nil
}

func installModule(cfg *config.Config, logger *zap.Logger, moduleName string) error {
	moduleDir := cfg.Modules.Dir
	if moduleDir == "" {
		moduleDir = "./modules"
	}

	cacheDir := moduleDir + "/.cache"
	archivePattern := fmt.Sprintf("%s/%s_*.tar.gz", cacheDir, moduleName)

	matches, err := filepath.Glob(archivePattern)
	if err != nil || len(matches) == 0 {
		return fmt.Errorf("module %s not found in cache. Run 'jte module pull %s' first", moduleName, moduleName)
	}

	archivePath := matches[len(matches)-1]
	installPath := fmt.Sprintf("%s/%s.so", moduleDir, moduleName)

	fmt.Printf("Installing module %s from %s...\n", moduleName, archivePath)

	if err := os.MkdirAll(moduleDir, 0755); err != nil {
		return fmt.Errorf("create module dir: %w", err)
	}

	src, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open archive: %w", err)
	}
	defer src.Close()

	dst, err := os.Create(installPath)
	if err != nil {
		return fmt.Errorf("create module file: %w", err)
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return fmt.Errorf("copy module: %w", err)
	}

	fmt.Printf("Module %s installed successfully to %s\n", moduleName, installPath)
	fmt.Println("Restart JTE engine to load the new module.")
	return nil
}

func runSimulateCLI(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: jte simulate <808|1078> [options]")
	}

	cfg, _ := config.Load("")
	logger, _ := config.InitLogger(&cfg.Logging)

	protocol := args[0]
	serverAddr := fmt.Sprintf("127.0.0.1:%d", cfg.Gateway.TCPPort)
	count := 1
	freq := 5
	phone := "013912345678"

	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--server":
			if i+1 < len(args) { serverAddr = args[i+1]; i++ }
		case "--count":
			if i+1 < len(args) { fmt.Sscanf(args[i+1], "%d", &count); i++ }
		case "--freq":
			if i+1 < len(args) { fmt.Sscanf(args[i+1], "%d", &freq); i++ }
		case "--phone":
			if i+1 < len(args) { phone = args[i+1]; i++ }
		}
	}

	switch protocol {
	case "808":
		sim := simulator.NewSimulator808(&simulator.Simulator808Config{
			ServerAddr: serverAddr,
			Phone:      phone,
			Count:      count,
			Freq:       time.Duration(freq) * time.Second,
		}, logger)
		if err := sim.Start(); err != nil {
			return fmt.Errorf("start simulator: %w", err)
		}
		fmt.Printf("Simulating %d JT808 terminals to %s (freq: %ds)\n", count, serverAddr, freq)
		fmt.Println("Press Ctrl+C to stop")

		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		sim.Stop()

	case "1078":
		sim := simulator.NewSimulator1078(&simulator.Simulator1078Config{
			ServerAddr: serverAddr,
			Phone:      phone,
			Count:      count,
		}, logger)
		if err := sim.Start(); err != nil {
			return fmt.Errorf("start simulator: %w", err)
		}
		fmt.Printf("Simulating %d JT1078 terminals to %s\n", count, serverAddr)
		fmt.Println("Press Ctrl+C to stop")

		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		sim.Stop()

	default:
		return fmt.Errorf("unknown protocol: %s (supported: 808, 1078)", protocol)
	}

	return nil
}
