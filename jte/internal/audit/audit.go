// Package audit 提供等保2.0 三级合规审计日志服务。
//
// 等保2.0 三级要求：
//   - 记录所有管理操作的 who/when/what/result
//   - 审计日志防篡改（HMAC-SM3 链式签名）
//   - 审计日志独立存储，仅审计管理员可查看
//   - 审计日志保留期限 >= 6 个月
package audit

import (
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/suoten/jt-engine/pkg/crypto/gmsm"
)

// AuditEntry 等保2.0 三级合规审计日志条目。
// 包含完整的 who/when/what/result + 上下文信息（IP/UA/会话/变更前后值）。
type AuditEntry struct {
	ID        string                 `json:"id"`
	Timestamp time.Time              `json:"timestamp"`
	Operator  string                 `json:"operator"`        // who: 操作者用户名/ID
	Action    string                 `json:"action"`          // what: 操作类型（HTTP方法/动作名）
	Resource  string                 `json:"resource"`        // what: 操作资源（路径/对象ID）
	Result    string                 `json:"result"`          // result: success/failed
	Details   map[string]interface{} `json:"details,omitempty"`
	IP        string                 `json:"ip,omitempty"`         // 来源 IP
	UserAgent string                 `json:"user_agent,omitempty"` // 客户端 UA（等保2.0 要求）
	SessionID string                 `json:"session_id,omitempty"` // 会话 ID（关联追踪）

	// AUTO-FIX-2026-07-02 [等保2.0]: 操作变更前后值（数据完整性审计）
	Before map[string]interface{} `json:"before,omitempty"` // 变更前状态
	After  map[string]interface{} `json:"after,omitempty"`  // 变更后状态

	// AUTO-FIX-2026-07-02 [等保2.0]: 操作分类（auth/config/data/user/security/system）
	Category string `json:"category,omitempty"`

	// AUTO-FIX-2026-07-02 [等保2.0]: 结果码（HTTP状态码或自定义错误码）
	ResultCode int `json:"result_code,omitempty"`

	// AUTO-FIX-2026-07-02 [等保2.0]: HMAC-SM3 链式防篡改签名
	// 签名内容 = HMAC-SM3(key, prev_hash || current_entry_canonical_json)
	// 第一条日志的 prev_hash = SM3("jte-audit-genesis")
	PrevHash string `json:"prev_hash,omitempty"`
	Hash     string `json:"hash,omitempty"` // 当前条目的链式哈希
}

// AuditCategory 操作分类常量
const (
	CategoryAuth     = "auth"     // 登录/登出/鉴权
	CategoryConfig   = "config"   // 系统配置变更
	CategoryData     = "data"     // 数据增删改
	CategoryUser     = "user"     // 用户/角色管理
	CategorySecurity = "security" // 安全策略/密钥/证书
	CategorySystem   = "system"   // 系统维护/模块管理
)

// genesisHash 创世哈希（链式日志的起点）
const genesisHash = "jte-audit-genesis"

type AuditLogger struct {
	mu         sync.Mutex
	file       *os.File
	logger     *zap.Logger
	maxSize    int64
	filePath   string
	hmacKey    []byte // HMAC-SM3 防篡改密钥（为空则不签名）
	prevHash   string // 上一条日志的哈希（链式签名）
	enabled    bool
}

// NewAuditLogger 创建审计日志记录器。
// filePath: 日志文件路径
// maxSizeMB: 单文件最大大小（MB），超过自动轮转
// hmacKeyHex: HMAC-SM3 防篡改密钥（hex 编码，32 字节=64 hex 字符），为空则不启用防篡改
func NewAuditLogger(filePath string, maxSizeMB int, logger *zap.Logger, hmacKeyHex ...string) (*AuditLogger, error) {
	if maxSizeMB <= 0 {
		maxSizeMB = 100
	}

	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return nil, err
	}

	file, err := os.OpenFile(filePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0640)
	if err != nil {
		return nil, err
	}

	a := &AuditLogger{
		file:     file,
		logger:   logger,
		maxSize:  int64(maxSizeMB) * 1024 * 1024,
		filePath: filePath,
		enabled:  true,
	}

	// 可选：启用 HMAC-SM3 链式防篡改
	if len(hmacKeyHex) > 0 && hmacKeyHex[0] != "" {
		key, err := hex.DecodeString(hmacKeyHex[0])
		if err != nil {
			file.Close()
			return nil, fmt.Errorf("HMAC 密钥 hex 解码失败: %w", err)
		}
		a.hmacKey = key
		// 初始化链式哈希起点
		a.prevHash = gmsm.SM3HashHex([]byte(genesisHash))
		// 尝试从现有日志文件恢复链式状态
		a.recoverChainState()
	}

	return a, nil
}

// SetHMACKey 设置/更新 HMAC-SM3 防篡改密钥
func (a *AuditLogger) SetHMACKey(keyHex string) error {
	key, err := hex.DecodeString(keyHex)
	if err != nil {
		return fmt.Errorf("HMAC 密钥 hex 解码失败: %w", err)
	}
	a.mu.Lock()
	a.hmacKey = key
	if a.prevHash == "" {
		a.prevHash = gmsm.SM3HashHex([]byte(genesisHash))
	}
	a.mu.Unlock()
	return nil
}

// recoverChainState 从现有日志文件恢复链式哈希状态（重启后继续链式签名）
// AUTO-FIX-2026-07-14 [ConvergeLoop-P1]: 修复死代码 + 从后向前查找有效日志。
// strings.Split 永远返回 >=1 长度，原 len(lines)==0 检查永远为 false。
func (a *AuditLogger) recoverChainState() {
	data, err := os.ReadFile(a.filePath)
	if err != nil {
		return
	}
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return
	}
	lines := strings.Split(trimmed, "\n")
	// 从后向前查找最后一条有效日志（跳过可能的空行/损坏行）
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		var entry AuditEntry
		if err := json.Unmarshal([]byte(line), &entry); err == nil && entry.Hash != "" {
			a.prevHash = entry.Hash
			return
		}
		a.logger.Warn("audit log chain may be broken: failed to parse last valid entry",
			zap.String("file", a.filePath),
			zap.Int("line", i+1))
	}
}

// computeHash 计算当前条目的链式哈希
// hash = HMAC-SM3(key, prevHash || canonical_json(entry_without_hash_fields))
// 无 HMAC 密钥时退化为 SM3(prevHash || canonical_json(...))
func (a *AuditLogger) computeHash(entry *AuditEntry) string {
	// 构造待签名内容（排除 hash/prev_hash 字段）
	type signPayload struct {
		ID        string                 `json:"id"`
		Timestamp time.Time              `json:"timestamp"`
		Operator  string                 `json:"operator"`
		Action    string                 `json:"action"`
		Resource  string                 `json:"resource"`
		Result    string                 `json:"result"`
		Details   map[string]interface{} `json:"details,omitempty"`
		IP        string                 `json:"ip,omitempty"`
		UserAgent string                 `json:"user_agent,omitempty"`
		SessionID string                 `json:"session_id,omitempty"`
		Before    map[string]interface{} `json:"before,omitempty"`
		After     map[string]interface{} `json:"after,omitempty"`
		Category  string                 `json:"category,omitempty"`
		ResultCode int                   `json:"result_code,omitempty"`
	}
	payload := signPayload{
		ID:        entry.ID,
		Timestamp: entry.Timestamp,
		Operator:  entry.Operator,
		Action:    entry.Action,
		Resource:  entry.Resource,
		Result:    entry.Result,
		Details:   entry.Details,
		IP:        entry.IP,
		UserAgent: entry.UserAgent,
		SessionID: entry.SessionID,
		Before:    entry.Before,
		After:     entry.After,
		Category:  entry.Category,
		ResultCode: entry.ResultCode,
	}
	data, _ := json.Marshal(payload)
	signContent := append([]byte(a.prevHash), data...)

	if len(a.hmacKey) > 0 {
		return hex.EncodeToString(gmsm.SM3HMAC(a.hmacKey, signContent))
	}
	return gmsm.SM3HashHex(signContent)
}

func (a *AuditLogger) Log(entry *AuditEntry) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if !a.enabled {
		return nil
	}

	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now()
	}

	// 链式签名
	entry.PrevHash = a.prevHash
	entry.Hash = a.computeHash(entry)
	// 更新链式状态
	a.prevHash = entry.Hash

	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}

	data = append(data, '\n')

	if _, err := a.file.Write(data); err != nil {
		return err
	}

	a.logger.Info("audit log",
		zap.String("action", entry.Action),
		zap.String("operator", entry.Operator),
		zap.String("resource", entry.Resource),
		zap.String("result", entry.Result),
		zap.String("category", entry.Category))

	if err := a.rotateIfNeeded(); err != nil {
		a.logger.Warn("audit log rotation failed", zap.Error(err))
	}

	return nil
}

func (a *AuditLogger) rotateIfNeeded() error {
	info, err := a.file.Stat()
	if err != nil {
		return err
	}

	if info.Size() < a.maxSize {
		return nil
	}

	a.file.Close()

	backupPath := a.filePath + "." + time.Now().Format("20060102150405")
	if err := os.Rename(a.filePath, backupPath); err != nil {
		return err
	}

	a.file, err = os.OpenFile(a.filePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0640)
	if err != nil {
		return err
	}

	return nil
}

func (a *AuditLogger) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.file != nil {
		return a.file.Close()
	}
	return nil
}

// SetEnabled 启用/禁用审计日志（维护期间可临时禁用）
func (a *AuditLogger) SetEnabled(enabled bool) {
	a.mu.Lock()
	a.enabled = enabled
	a.mu.Unlock()
}

// LogAuth 记录认证操作（登录/登出/鉴权失败等）
func (a *AuditLogger) LogAuth(operator, action, result, ip string) error {
	return a.Log(&AuditEntry{
		Operator: operator,
		Action:   action,
		Resource: "auth",
		Result:   result,
		IP:       ip,
		Category: CategoryAuth,
	})
}

// LogAuthDetail 记录认证操作（带完整上下文）
func (a *AuditLogger) LogAuthDetail(operator, action, result, ip, userAgent, sessionID string, details map[string]interface{}) error {
	return a.Log(&AuditEntry{
		Operator:  operator,
		Action:    action,
		Resource:  "auth",
		Result:    result,
		IP:        ip,
		UserAgent: userAgent,
		SessionID: sessionID,
		Details:   details,
		Category:  CategoryAuth,
	})
}

func (a *AuditLogger) LogModule(operator, action, module, result string) error {
	return a.Log(&AuditEntry{
		Operator: operator,
		Action:   action,
		Resource: "module:" + module,
		Result:   result,
		Category: CategorySystem,
	})
}

func (a *AuditLogger) LogConfig(operator, action, result string) error {
	return a.Log(&AuditEntry{
		Operator: operator,
		Action:   action,
		Resource: "config",
		Result:   result,
		Category: CategoryConfig,
	})
}

// LogDataChange 记录数据变更操作（带变更前后值）
func (a *AuditLogger) LogDataChange(operator, action, resource, result, ip, category string, before, after map[string]interface{}) error {
	return a.Log(&AuditEntry{
		Operator:  operator,
		Action:    action,
		Resource:  resource,
		Result:    result,
		IP:        ip,
		Category:  category,
		Before:    before,
		After:     after,
	})
}

// LogSecurity 记录安全相关操作（密钥轮换/证书更新/安全策略变更）
func (a *AuditLogger) LogSecurity(operator, action, result, ip string, details map[string]interface{}) error {
	return a.Log(&AuditEntry{
		Operator: operator,
		Action:   action,
		Resource: "security",
		Result:   result,
		IP:       ip,
		Details:  details,
		Category: CategorySecurity,
	})
}

// VerifyChain 验证审计日志链完整性（防篡改检测）。
// 返回第一个被篡改的条目行号（0-based），-1 表示完整无篡改。
func (a *AuditLogger) VerifyChain() (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	data, err := os.ReadFile(a.filePath)
	if err != nil {
		return -1, err
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	expectedPrev := gmsm.SM3HashHex([]byte(genesisHash))

	for i, line := range lines {
		if line == "" {
			continue
		}
		var entry AuditEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			return i, fmt.Errorf("行 %d JSON 解析失败: %w", i, err)
		}
		// 验证 prevHash 链接
		if entry.PrevHash != expectedPrev {
			return i, fmt.Errorf("行 %d prevHash 不匹配（链断裂）", i)
		}
		// 重新计算 hash 验证（常量时间比较防时序攻击）
		// 临时保存并清除 hash 字段以重算
		savedHash := entry.Hash
		savedPrev := a.prevHash
		a.prevHash = entry.PrevHash
		computedHash := a.computeHash(&entry)
		a.prevHash = savedPrev
		// FIXED [2026-07-17]: 使用 subtle.ConstantTimeCompare 替代字符串比较，
		// 防止攻击者通过时序差异逐字节猜测哈希值。
		if subtle.ConstantTimeCompare([]byte(computedHash), []byte(savedHash)) != 1 {
			return i, fmt.Errorf("行 %d hash 不匹配（内容被篡改）", i)
		}
		expectedPrev = savedHash
	}
	return -1, nil
}

// GetFilePath 返回审计日志文件路径（供外部读取日志使用）
func (a *AuditLogger) GetFilePath() string {
	return a.filePath
}

// ReadEntries 读取审计日志最近 N 条（倒序，最新在前）。
// AUTO-FIX-2026-07-02: 修复审计日志路径不一致 bug——
// 原写入路径为 configDir/audit.log，读取路径为 os.TempDir()/jte/audit.log，
// 导致 ListAuditLogs 永远读到空文件。现统一通过 AuditLogger.GetFilePath() 读取。
func (a *AuditLogger) ReadEntries(limit int) ([]*AuditEntry, error) {
	if limit <= 0 {
		limit = 100
	}

	data, err := os.ReadFile(a.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return []*AuditEntry{}, nil
		}
		return nil, err
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) == 0 {
		return []*AuditEntry{}, nil
	}

	// 取最后 limit 行
	start := 0
	if len(lines) > limit {
		start = len(lines) - limit
	}
	result := make([]*AuditEntry, 0, limit)
	for i := len(lines) - 1; i >= start; i-- {
		var entry AuditEntry
		if err := json.Unmarshal([]byte(lines[i]), &entry); err == nil {
			result = append(result, &entry)
		}
	}
	return result, nil
}
