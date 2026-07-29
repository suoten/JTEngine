package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/suoten/jt-engine/internal/config"
	"github.com/suoten/jt-engine/pkg/storage"
	"go.uber.org/zap"
)

// ===================================================================
// P1-6: JWT 密钥轮换 + KMS 集成 + token 黑名单（泄露应急）
// ===================================================================
//
// 设计要点：
//  1. KMS 抽象：密钥可从环境变量 / 独立文件加载，禁止主配置文件明文存储
//  2. 自动轮换：每 90 天生成新 kid+secret，旧 kid 保留 7 天仅供验签
//  3. Token 黑名单：泄露应急时撤销指定 token 或全部 token，强制重新登录
//  4. kid 标识：token header 携带 kid，验签按 kid 路由密钥（已由 JWTConfig 实现）
//
// 与现有 JWTConfig 的关系：
//   - JWTConfig 提供 RotateJWTKey / CleanupExpiredKids / GetActiveSecret / GetSecret 原语
//   - 本文件提供调度器（何时触发轮换）+ 黑名单（应急撤销）+ KMS 加载器

// TokenBlacklist JWT token 黑名单（泄露应急撤销）。
// 双后端：内存 map（默认）+ Redis（可选，支持多节点共享）。
// 黑名单记录 jti -> 过期时间，过期后自动清理，避免无限增长。
type TokenBlacklist struct {
	mu      sync.RWMutex
	revoked map[string]time.Time // jti -> 过期时间（过期后可清理）
	cache   storage.CacheStorage // 可选 Redis 后端（多节点共享黑名单）
	logger  *zap.Logger
}

// NewTokenBlacklist 创建 token 黑名单。cache 为 nil 时仅用内存后端。
func NewTokenBlacklist(cache storage.CacheStorage, logger *zap.Logger) *TokenBlacklist {
	return &TokenBlacklist{
		revoked: make(map[string]time.Time),
		cache:   cache,
		logger:  logger,
	}
}

// Revoke 撤销指定 token（按 jti）。expiry 为该 token 的原始过期时间，过期后自动清理。
func (b *TokenBlacklist) Revoke(jti string, expiry time.Time) error {
	if jti == "" {
		return nil
	}
	b.mu.Lock()
	b.revoked[jti] = expiry
	b.mu.Unlock()
	// 同步到 Redis（多节点共享）
	if b.cache != nil {
		ttl := time.Until(expiry)
		if ttl > 0 {
			ctx := context.Background()
			if err := b.cache.CacheSet(ctx, "jwt:blacklist:"+jti, true, ttl); err != nil {
				b.logger.Warn("同步 token 黑名单到 Redis 失败", zap.String("jti", jti), zap.Error(err))
			}
		}
	}
	return nil
}

// IsRevoked 检查 token 是否已被撤销（黑名单命中）。
func (b *TokenBlacklist) IsRevoked(jti string) bool {
	if jti == "" {
		return false
	}
	b.mu.RLock()
	_, ok := b.revoked[jti]
	b.mu.RUnlock()
	if ok {
		return true
	}
	// 查 Redis（多节点共享黑名单）
	if b.cache != nil {
		ctx := context.Background()
		var revoked bool
		if err := b.cache.CacheGet(ctx, "jwt:blacklist:"+jti, &revoked); err == nil && revoked {
			return true
		}
	}
	return false
}

// RevokeAll 撤销所有已签发 token（泄露应急）。
// 通过生成一个 "全局撤销时间戳" 实现：所有签发时间早于该时间戳的 token 均视为无效。
// 配合单 token 撤销，实现细粒度 + 全局两种应急模式。
func (b *TokenBlacklist) RevokeAll() error {
	b.mu.Lock()
	// 全局撤销点：当前时间。所有 iat < 此时间的 token 在 IsGlobalRevoked 中被拒绝。
	b.revoked["__global_revoke__"] = time.Now()
	b.mu.Unlock()
	if b.cache != nil {
		ctx := context.Background()
		// TTL 设为 token 最大有效期（24h * 7 = 7天），覆盖所有可能的有效 token
		if err := b.cache.CacheSet(ctx, "jwt:global_revoke", time.Now().Unix(), 7*24*time.Hour); err != nil {
			b.logger.Warn("同步全局撤销点到 Redis 失败", zap.Error(err))
		}
	}
	b.logger.Warn("JWT 全局撤销已触发，所有已签发 token 即时失效")
	return nil
}

// IsGlobalRevoked 检查 token 签发时间是否早于全局撤销点（泄露应急）。
// issuedAt 为 token 的 iat 声明。
func (b *TokenBlacklist) IsGlobalRevoked(issuedAt time.Time) bool {
	b.mu.RLock()
	globalT, ok := b.revoked["__global_revoke__"]
	b.mu.RUnlock()
	if ok && issuedAt.Before(globalT) {
		return true
	}
	if b.cache != nil {
		ctx := context.Background()
		var ts int64
		if err := b.cache.CacheGet(ctx, "jwt:global_revoke", &ts); err == nil && ts > 0 {
			globalTime := time.Unix(ts, 0)
			if issuedAt.Before(globalTime) {
				return true
			}
		}
	}
	return false
}

// Cleanup 清理已过期的黑名单记录，避免内存无限增长。
func (b *TokenBlacklist) Cleanup() {
	now := time.Now()
	b.mu.Lock()
	for jti, expiry := range b.revoked {
		if jti == "__global_revoke__" {
			// 全局撤销点保留 7 天后清理
			if now.Sub(expiry) > 7*24*time.Hour {
				delete(b.revoked, jti)
			}
			continue
		}
		if now.After(expiry) {
			delete(b.revoked, jti)
		}
	}
	b.mu.Unlock()
}

// JWTRotationManager JWT 密钥自动轮换调度器。
// 每 90 天自动生成新 kid+secret，旧 kid 保留 7 天仅供验签（JWTConfig.CleanupExpiredKids）。
// 泄露应急时调用 EmergencyRotate 立即轮换 + 全局撤销。
type JWTRotationManager struct {
	jwt       *config.JWTConfig
	blacklist *TokenBlacklist
	logger    *zap.Logger
	stopCh    chan struct{}
	stopOnce  sync.Once
	auditFn   func(action, detail string) // 可选审计回调
}

// NewJWTRotationManager 创建轮换管理器。
func NewJWTRotationManager(jwt *config.JWTConfig, blacklist *TokenBlacklist, logger *zap.Logger) *JWTRotationManager {
	return &JWTRotationManager{
		jwt:       jwt,
		blacklist: blacklist,
		logger:    logger,
		stopCh:    make(chan struct{}),
	}
}

// SetAuditFn 注入审计回调（记录密钥轮换/撤销事件）。
func (m *JWTRotationManager) SetAuditFn(fn func(action, detail string)) {
	m.auditFn = fn
}

// Start 启动后台轮换调度 goroutine。
// 每天检查一次：active kid 创建时间超过 RotateDays 则自动轮换。
// FIXED: [P1] loop goroutine 缺少 recover()，panic 会扩散至整个进程 [2026-07-17]
func (m *JWTRotationManager) Start() {
	if m.jwt == nil {
		m.logger.Warn("JWT 轮换管理器未启用：JWTConfig 为 nil")
		return
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				m.logger.Error("JWT rotation loop panic recovered",
					zap.Any("panic", r),
					zap.Stack("stack"))
			}
		}()
		m.loop()
	}()
	m.logger.Info("JWT 密钥轮换管理器已启动",
		zap.Int("rotate_days", m.jwt.RotateDays),
		zap.String("active_kid", m.jwt.ActiveKid))
}

// Stop 停止轮换调度。
func (m *JWTRotationManager) Stop() {
	m.stopOnce.Do(func() {
		close(m.stopCh)
	})
}

func (m *JWTRotationManager) loop() {
	ticker := time.NewTicker(24 * time.Hour) // 每天检查一次
	defer ticker.Stop()
	blacklistTicker := time.NewTicker(1 * time.Hour) // 每小时清理黑名单
	defer blacklistTicker.Stop()
	for {
		select {
		case <-m.stopCh:
			return
		case <-ticker.C:
			// R62-FIX [P2]: 将 recover 移到循环内部，确保单次 checkAndRotate panic
			// 不会导致轮换协程退出。原实现 recover 在 goroutine 级别，
			// panic 后协程退出，JWT 密钥永不被轮换，旧密钥泄漏风险。
			func() {
				defer func() {
					if r := recover(); r != nil {
						if m.logger != nil {
							m.logger.Error("JWT rotation checkAndRotate panic recovered",
								zap.Any("panic", r))
						}
					}
				}()
				m.checkAndRotate()
			}()
		case <-blacklistTicker.C:
			// R62-FIX [P2]: 同上，确保黑名单清理 panic 不杀协程
			func() {
				defer func() {
					if r := recover(); r != nil {
						if m.logger != nil {
							m.logger.Error("JWT blacklist cleanup panic recovered",
								zap.Any("panic", r))
						}
					}
				}()
				if m.blacklist != nil {
					m.blacklist.Cleanup()
				}
				if m.jwt != nil {
					m.jwt.CleanupExpiredKids()
				}
			}()
		}
	}
}

// checkAndRotate 检查 active kid 是否到期，到期则自动轮换。
func (m *JWTRotationManager) checkAndRotate() {
	if m.jwt == nil || m.jwt.RotateDays <= 0 {
		return
	}
	activeKid := m.jwt.ActiveKid
	createdAt, ok := m.jwt.GetActiveKidCreatedAt()
	if !ok {
		// active kid 无创建记录（可能是配置文件加载的初始 kid），补记
		m.jwt.EnsureKidCreatedAt()
		return
	}
	if time.Since(createdAt) < time.Duration(m.jwt.RotateDays)*24*time.Hour {
		return // 未到期
	}
	// 到期，执行自动轮换
	newKid := generateKid()
	newSecret, err := generateSecret(48)
	if err != nil {
		m.logger.Error("自动轮换生成密钥失败", zap.Error(err))
		return
	}
	m.jwt.RotateJWTKey(newKid, newSecret)
	m.logger.Info("JWT 密钥自动轮换完成",
		zap.String("old_kid", activeKid),
		zap.String("new_kid", newKid),
		zap.Int("rotate_days", m.jwt.RotateDays))
	if m.auditFn != nil {
		m.auditFn("jwt_auto_rotate", fmt.Sprintf("old_kid=%s new_kid=%s", activeKid, newKid))
	}
}

// EmergencyRotate 泄露应急：立即生成新密钥 + 全局撤销所有现有 token。
// 旧密钥保留 7 天仅供验签（已签发的 token 在全局撤销后立即失效，用户需重新登录）。
func (m *JWTRotationManager) EmergencyRotate() (newKid string, err error) {
	if m.jwt == nil {
		return "", fmt.Errorf("JWTConfig 未配置")
	}
	newKid = generateKid()
	newSecret, err := generateSecret(48)
	if err != nil {
		return "", fmt.Errorf("生成应急密钥失败: %w", err)
	}
	oldKid := m.jwt.ActiveKid
	m.jwt.RotateJWTKey(newKid, newSecret)
	// 全局撤销：所有已签发 token 立即失效
	if m.blacklist != nil {
		m.blacklist.RevokeAll()
	}
	m.logger.Warn("JWT 密钥应急轮换已执行",
		zap.String("old_kid", oldKid),
		zap.String("new_kid", newKid))
	if m.auditFn != nil {
		m.auditFn("jwt_emergency_rotate", fmt.Sprintf("old_kid=%s new_kid=%s global_revoke=true", oldKid, newKid))
	}
	return newKid, nil
}

// generateKid 生成 8 字节 hex kid（16 字符）。
func generateKid() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("kid-%d", time.Now().UnixNano())
	}
	return "k" + hex.EncodeToString(b) // k 前缀避免纯数字
}

// generateSecret 生成 n 字节随机 secret（base64 编码）。
func generateSecret(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil // 96 字符 hex，远超 32 字节要求
}

// ===================================================================
// KMS 集成：从环境变量 / 独立文件加载 JWT 密钥，禁止主配置明文
// ===================================================================

// LoadJWTSecretsFromEnv 从环境变量加载 JWT 密钥（KMS 替代方案）。
// 环境变量格式：
//   JTE_JWT_SECRET_<KID>=<secret>   — 每个 kid 一个环境变量
//   JTE_JWT_ACTIVE_KID=<kid>        — 当前签发用的 kid
//
// 返回 secrets map 和 activeKid。无配置时返回 nil（回退到配置文件）。
func LoadJWTSecretsFromEnv() (secrets map[string]string, activeKid string) {
	activeKid = os.Getenv("JTE_JWT_ACTIVE_KID")
	for _, env := range os.Environ() {
		if !strings.HasPrefix(env, "JTE_JWT_SECRET_") {
			continue
		}
		parts := strings.SplitN(env, "=", 2)
		if len(parts) != 2 {
			continue
		}
		kid := strings.TrimPrefix(parts[0], "JTE_JWT_SECRET_")
		if kid == "" {
			continue
		}
		if secrets == nil {
			secrets = make(map[string]string)
		}
		secrets[kid] = parts[1]
	}
	return secrets, activeKid
}

// jwtKeyFile KMS 文件格式（独立于主配置，可由 secret manager 挂载）
type jwtKeyFile struct {
	Secrets   map[string]string `json:"secrets"`
	ActiveKid string            `json:"active_kid"`
}

// LoadJWTSecretsFromFile 从独立文件加载 JWT 密钥（KMS 替代方案）。
// 文件格式 JSON: {"secrets": {"kid1": "secret1"}, "active_kid": "kid1"}
// 文件权限应为 0600，由 secret manager（Vault/KMS）挂载或生成。
func LoadJWTSecretsFromFile(path string) (secrets map[string]string, activeKid string, err error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, "", fmt.Errorf("读取 JWT 密钥文件失败: %w", err)
	}
	var kf jwtKeyFile
	if err := json.Unmarshal(data, &kf); err != nil {
		return nil, "", fmt.Errorf("解析 JWT 密钥文件失败: %w", err)
	}
	if len(kf.Secrets) == 0 {
		return nil, "", fmt.Errorf("JWT 密钥文件中 secrets 为空")
	}
	return kf.Secrets, kf.ActiveKid, nil
}

// InitJWTFromKMS 根据配置的 kms_source 从 KMS 加载密钥到 JWTConfig。
//   - "env": 从环境变量加载（JTE_JWT_SECRET_*）
//   - "file": 从独立文件加载（jwt.keys.json）
//   - "config" 或 "": 回退到主配置文件（向后兼容，生产环境不推荐）
//
// 加载后校验密钥强度（≥32 字节），弱密钥返回错误。
func InitJWTFromKMS(jwt *config.JWTConfig, kmsSource, kmsFilePath string, logger *zap.Logger) error {
	if jwt == nil {
		return nil
	}
	switch strings.ToLower(kmsSource) {
	case "env":
		secrets, activeKid := LoadJWTSecretsFromEnv()
		if len(secrets) == 0 {
			return fmt.Errorf("kms_source=env 但未找到 JTE_JWT_SECRET_* 环境变量")
		}
		if err := validateSecretsStrength(secrets); err != nil {
			return err
		}
		jwt.SetSecretsFromKMS(secrets, activeKid)
		logger.Info("JWT 密钥已从环境变量(KMS)加载", zap.Int("kid_count", len(secrets)), zap.String("active_kid", jwt.ActiveKid))
	case "file":
		if kmsFilePath == "" {
			return fmt.Errorf("kms_source=file 但未配置 jwt.kms_file_path")
		}
		secrets, activeKid, err := LoadJWTSecretsFromFile(kmsFilePath)
		if err != nil {
			return err
		}
		if err := validateSecretsStrength(secrets); err != nil {
			return err
		}
		jwt.SetSecretsFromKMS(secrets, activeKid)
		logger.Info("JWT 密钥已从文件(KMS)加载", zap.String("path", kmsFilePath), zap.Int("kid_count", len(secrets)), zap.String("active_kid", jwt.ActiveKid))
	case "", "config":
		// 回退到主配置文件（向后兼容）
		if len(jwt.Secrets) == 0 {
			logger.Warn("JWT 密钥未配置 KMS 来源，且无 secrets 配置，将回退到 jwt_secret（不推荐生产使用）")
		}
	default:
		return fmt.Errorf("不支持的 kms_source: %s（可选: env/file/config）", kmsSource)
	}
	return nil
}

// validateSecretsStrength 校验所有密钥强度（≥32 字节），防止弱密钥。
func validateSecretsStrength(secrets map[string]string) error {
	for kid, secret := range secrets {
		if len(secret) < 32 {
			return fmt.Errorf("JWT 密钥 kid=%s 长度不足 32 字节（当前 %d），请使用 openssl rand -base64 48 生成", kid, len(secret))
		}
	}
	return nil
}
