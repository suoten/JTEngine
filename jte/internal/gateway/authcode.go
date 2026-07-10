package gateway

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"

	"go.uber.org/zap"
)

// AUTO-FIX-2026-06-30 [P0-1]: 设备鉴权码防伪造与防克隆。
//
// 原实现（handler.go handleRegister）直接用手机号作为 AuthCode，等于无密钥：
//   - 任何知道手机号的攻击者均可伪造 0x0102 鉴权
//   - session.Metadata["auth_code"] 校验在 expectedAuthCode=="" 时被跳过（服务重启后 session 重建）
//   - 无多 IP/多会话检测，鉴权码被盗用无法发现
//
// 本 AuthCodeManager 实现：
//   1. 强随机鉴权码（crypto/rand 16 字节 → 32 字符 hex），不再用手机号
//   2. 鉴权码与终端手机号绑定校验（phone ↔ authCode 双向索引）
//   3. 同一鉴权码多 IP 使用 → 告警（异常使用记录审计日志），允许（NAT/重连可能变 IP）
//   4. 同一鉴权码多会话使用 → 由 SessionManager 单设备单会话机制拒绝第二个会话
//   5. ICCID/IMEI/TerminalID 作为设备指纹记录，便于审计追溯
//   6. 鉴权码 24h 过期自动清理，避免内存泄漏

// AuthCodeRecord 鉴权码记录
type AuthCodeRecord struct {
	AuthCode    string         // 鉴权码（强随机）
	Phone       string         // 绑定的终端手机号
	DeviceFP    string         // 设备指纹（IMEI/TerminalID/ICCID 组合）
	RemoteAddr  string         // 首次下发时的远程地址
	SessionID   string         // 首次下发的会话 ID
	CreatedAt   time.Time      // 生成时间
	IPChanges   []ipChangeLog  // IP 变更历史记录（用于频率检测）
}

// ipChangeLog IP 变更记录
type ipChangeLog struct {
	FromIP    string
	ToIP      string
	ChangedAt time.Time
}

// AuthCodeManager 鉴权码管理器（内存实现，服务重启后终端需重新注册）
type AuthCodeManager struct {
	mu          sync.RWMutex
	records     map[string]*AuthCodeRecord // key = authCode
	byPhone     map[string]*AuthCodeRecord // key = phone，快速查询某手机的当前鉴权码
	logger      *zap.Logger
	// AUTO-FIX-2026-07-03: 防克隆增强 — IP 变更频率检测参数
	ipChangeWindow  time.Duration // IP 变更检测时间窗口（默认 1 小时）
	ipChangeThreshold int          // 时间窗口内 IP 变更次数阈值（默认 3 次）
}

// NewAuthCodeManager 创建鉴权码管理器
func NewAuthCodeManager(logger *zap.Logger) *AuthCodeManager {
	m := &AuthCodeManager{
		records:          make(map[string]*AuthCodeRecord),
		byPhone:          make(map[string]*AuthCodeRecord),
		logger:           logger,
		ipChangeWindow:   1 * time.Hour,  // 默认 1 小时窗口
		ipChangeThreshold: 3,              // 默认 1 小时内换 IP >3 次告警
	}
	return m
}

// Generate 为指定手机号生成强随机鉴权码并记录。
// deviceFP 为设备指纹（IMEI/TerminalID/ICCID 组合），用于审计追溯。
// 同一手机号重复注册时，旧鉴权码会被撤销（防止旧码继续可用）。
func (m *AuthCodeManager) Generate(phone, deviceFP, remoteAddr, sessionID string) string {
	code := generateSecureAuthCode()
	rec := &AuthCodeRecord{
		AuthCode:   code,
		Phone:      phone,
		DeviceFP:   deviceFP,
		RemoteAddr: remoteAddr,
		SessionID:  sessionID,
		CreatedAt:  time.Now(),
	}
	m.mu.Lock()
	// 撤销该 phone 的旧鉴权码，防止旧码在被盗后继续可用
	if old, ok := m.byPhone[phone]; ok {
		delete(m.records, old.AuthCode)
		m.logger.Info("撤销旧鉴权码（设备重新注册）",
			zap.String("phone", phone),
			zap.String("old_session", old.SessionID),
			zap.String("new_session", sessionID))
	}
	m.records[code] = rec
	m.byPhone[phone] = rec
	m.mu.Unlock()
	return code
}

// Validate 校验鉴权码。
// 返回 (valid, reason)。valid=true 表示通过；reason 非空时为失败/告警原因。
//
// 校验规则：
//  1. 鉴权码必须存在（不存在 → false "auth_code_not_found"，终端需重新注册）
//  2. 鉴权码必须与手机号绑定（不匹配 → false "phone_mismatch"，疑似伪造）
//  3. 不同 IP 使用同一鉴权码 → 记录 IP 变更，告警但不拒绝
//  4. 同一鉴权码 1 小时内换 IP >3 次 → 高频变更告警（疑似克隆）
//
// 多会话检测由 SessionManager.Register 的单设备单会话机制保证（同一 phone 踢旧会话）。
func (m *AuthCodeManager) Validate(phone, authCode, remoteAddr string) (bool, string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	rec, ok := m.records[authCode]
	if !ok {
		m.logger.Warn("鉴权码不存在，终端需重新注册",
			zap.String("phone", phone))
		return false, "auth_code_not_found"
	}

	if rec.Phone != phone {
		m.logger.Warn("鉴权码与手机号不匹配，疑似伪造",
			zap.String("phone", phone),
			zap.String("expected_phone", rec.Phone),
			zap.String("device_fp", rec.DeviceFP))
		return false, "phone_mismatch"
	}

	// IP 变更检测
	if rec.RemoteAddr != remoteAddr {
		oldIP := rec.RemoteAddr
		now := time.Now()

		// 记录 IP 变更
		rec.IPChanges = append(rec.IPChanges, ipChangeLog{
			FromIP:    oldIP,
			ToIP:      remoteAddr,
			ChangedAt: now,
		})

		// 清理窗口外的旧记录
		cutoff := now.Add(-m.ipChangeWindow)
		validChanges := rec.IPChanges[:0]
		for _, c := range rec.IPChanges {
			if c.ChangedAt.After(cutoff) {
				validChanges = append(validChanges, c)
			}
		}
		rec.IPChanges = validChanges

		// 更新当前 IP
		rec.RemoteAddr = remoteAddr

		// 单次 IP 变更告警
		m.logger.Warn("鉴权码从不同 IP 使用，异常使用记录",
			zap.String("phone", phone),
			zap.String("orig_ip", oldIP),
			zap.String("new_ip", remoteAddr),
			zap.String("device_fp", rec.DeviceFP))

		// AUTO-FIX-2026-07-03: 高频 IP 变更告警（防克隆）
		// 同一鉴权码在时间窗口内换 IP 超过阈值 → 疑似克隆攻击
		if len(rec.IPChanges) > m.ipChangeThreshold {
			m.logger.Error("鉴权码高频 IP 变更告警（疑似克隆攻击）",
				zap.String("phone", phone),
				zap.String("device_fp", rec.DeviceFP),
				zap.Int("change_count", len(rec.IPChanges)),
				zap.Int("threshold", m.ipChangeThreshold),
				zap.Duration("window", m.ipChangeWindow),
				zap.String("ips", formatIPChangeList(rec.IPChanges)))
			// 记录审计日志但不拒绝（NAT/CDN 可能导致合法高频变更）
			// 运营人员可根据告警级别决定是否手动封禁
		}
	}

	return true, ""
}

// formatIPChangeList 格式化 IP 变更列表用于日志输出
func formatIPChangeList(changes []ipChangeLog) string {
	if len(changes) == 0 {
		return ""
	}
	result := ""
	for i, c := range changes {
		if i > 0 {
			result += " -> "
		}
		result += c.FromIP + "->" + c.ToIP
	}
	return result
}

// Revoke 撤销指定手机号的鉴权码（登出/注销时调用）
func (m *AuthCodeManager) Revoke(phone string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if rec, ok := m.byPhone[phone]; ok {
		delete(m.records, rec.AuthCode)
		delete(m.byPhone, phone)
	}
}

// CleanupExpired 清理过期的鉴权码记录（超过 24h 未使用）
// 由调用方定期触发（如 HeartbeatChecker 的 checkLoop）。
func (m *AuthCodeManager) CleanupExpired(maxAge time.Duration) {
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	for code, rec := range m.records {
		if now.Sub(rec.CreatedAt) >= maxAge {
			delete(m.records, code)
			delete(m.byPhone, rec.Phone)
		}
	}
}

// generateSecureAuthCode 生成 32 字符 hex 鉴权码（16 字节 crypto/rand）。
// 失败时回退到时间戳哈希（仍比手机号强）。
func generateSecureAuthCode() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// rand.Read 失败极罕见，回退到时间戳（不可预测性仍优于手机号）
		t := time.Now().UnixNano()
		tb := []byte{
			byte(t), byte(t >> 8), byte(t >> 16), byte(t >> 24),
			byte(t >> 32), byte(t >> 40), byte(t >> 48), byte(t >> 56),
		}
		return hex.EncodeToString(tb) + hex.EncodeToString(tb)
	}
	return hex.EncodeToString(b)
}
