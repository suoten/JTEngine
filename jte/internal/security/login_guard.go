// Package security 提供防克隆与登录安全加固（等保2.0 三级 + 防克隆要求）。
//
// 功能：
//   - 设备指纹采集与校验（鉴权码绑定手机号 + 设备指纹）
//   - 多 IP 登录告警
//   - 异常登录行为检测（异地登录、非常用设备）
//   - 登录失败锁定（防暴力破解）
package security

import (
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/suoten/jt-engine/pkg/crypto/gmsm"
)

// LoginGuard 登录安全守卫
type LoginGuard struct {
	mu sync.RWMutex
	// 登录失败计数: username -> failureCount
	loginFailures map[string]*loginFailureState
	// 用户登录历史: username -> []*LoginRecord
	loginHistory map[string][]*LoginRecord
	// 用户已知设备: username -> map[fingerprint]bool
	knownDevices map[string]map[string]bool
	// 用户常用 IP: username -> map[ip]bool
	knownIPs map[string]map[string]bool

	logger *zap.Logger

	// 配置
	maxFailures     int           // 最大失败次数（超过锁定）
	lockoutDuration time.Duration // 锁定时长
	historyLimit    int           // 登录历史保留条数
	geoIPProvider   GeoIPProvider // IP 地理位置查询（可选）

	// FIXED: [P1-1] 四张 Map 无限增长内存泄漏——增加后台清理协程
	stopCh   chan struct{}
	stopOnce sync.Once
}

// loginFailureState 登录失败状态
type loginFailureState struct {
	count       int
	lastFailure time.Time
	lockedUntil time.Time
}

// LoginRecord 登录记录
type LoginRecord struct {
	Username   string    `json:"username"`
	IP         string    `json:"ip"`
	UserAgent  string    `json:"user_agent"`
	Fingerprint string   `json:"fingerprint"` // 设备指纹
	Timestamp  time.Time `json:"timestamp"`
	Success    bool      `json:"success"`
	Geo        string    `json:"geo,omitempty"` // 地理位置
}

// GeoIPProvider IP 地理位置查询接口（实现可对接 MaxMind GeoIP2 / 在线 API）
type GeoIPProvider interface {
	Lookup(ip string) (country, province, city string)
}

// LoginGuardConfig 登录守卫配置
type LoginGuardConfig struct {
	MaxFailures     int           // 最大失败次数（默认 5）
	LockoutDuration time.Duration // 锁定时长（默认 15 分钟）
	HistoryLimit    int           // 历史保留条数（默认 20）
	// 异地登录告警阈值（公里，默认 500km）
	RemoteLoginThresholdKm float64
}

// DefaultLoginGuardConfig 默认配置
func DefaultLoginGuardConfig() LoginGuardConfig {
	return LoginGuardConfig{
		MaxFailures:            5,
		LockoutDuration:        15 * time.Minute,
		HistoryLimit:           20,
		RemoteLoginThresholdKm: 500,
	}
}

// NewLoginGuard 创建登录守卫
// FIXED: [P1-1] 自动启动后台清理协程，定期清除过期的失败记录、历史记录和冷用户数据
func NewLoginGuard(cfg LoginGuardConfig, logger *zap.Logger) *LoginGuard {
	if cfg.MaxFailures <= 0 {
		cfg = DefaultLoginGuardConfig()
	}
	g := &LoginGuard{
		loginFailures:   make(map[string]*loginFailureState),
		loginHistory:    make(map[string][]*LoginRecord),
		knownDevices:    make(map[string]map[string]bool),
		knownIPs:        make(map[string]map[string]bool),
		logger:          logger,
		maxFailures:     cfg.MaxFailures,
		lockoutDuration: cfg.LockoutDuration,
		historyLimit:    cfg.HistoryLimit,
		stopCh:          make(chan struct{}),
	}
	// R57-FIX [P1]: 添加 recover 防止 cleanupLoop panic 导致登录安全守卫静默死亡，
	// 四张 Map 将无限增长导致内存泄漏
	go func() {
		defer func() {
			if r := recover(); r != nil {
				if g.logger != nil {
					g.logger.Error("login_guard cleanupLoop panic recovered",
						zap.Any("panic", r), zap.Stack("stack"))
				}
			}
		}()
		g.cleanupLoop()
	}()
	return g
}

// Stop 停止后台清理协程（幂等，供 api.Server.Stop 调用）
func (g *LoginGuard) Stop() {
	g.stopOnce.Do(func() { close(g.stopCh) })
}

// cleanupLoop 后台定期清理过期的登录数据，防止四张 Map 无限增长
// - loginFailures: 清理锁定已过期的条目（lockedUntil 已过且后续未再登录）
// - loginHistory: 清理超过 30 天的记录
// - knownDevices/knownIPs: 清理超过 90 天未活跃的用户
const (
	loginGuardCleanupInterval = 10 * time.Minute
	loginHistoryTTL           = 30 * 24 * time.Hour // 30 天
	knownDeviceTTL            = 90 * 24 * time.Hour // 90 天
	// [P0-3] 分批清理上限：每次 cleanupExpired 最多处理 cleanupBatchSize 条，
	// 剩余下次 cleanupLoop tick 继续处理，避免长时间持锁阻塞登录。
	cleanupBatchSize = 1000
)

func (g *LoginGuard) cleanupLoop() {
	ticker := time.NewTicker(loginGuardCleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-g.stopCh:
			return
		case <-ticker.C:
			// R62-FIX [P2]: 将 recover 移到循环内部，确保单次 cleanupExpired panic
			// 不会导致清理协程退出。原实现 recover 在 goroutine 级别，
			// panic 后协程退出，四张 Map 永不被清理，内存泄漏。
			func() {
				defer func() {
					if r := recover(); r != nil {
						if g.logger != nil {
							g.logger.Error("loginGuard cleanupExpired panic recovered",
								zap.Any("panic", r),
								zap.Stack("stack"))
						}
					}
				}()
				g.cleanupExpired()
			}()
		}
	}
}

// cleanupExpired [P0-3] 两阶段清理，避免长时间持写锁阻塞登录请求：
//
//	阶段 1：RLock 快照所有待清理 keys 列表（读锁，不阻塞 CheckLogin 等读操作）
//	阶段 2：逐条 Lock 删除单个条目（写锁仅持有极短时间，每次删除后立即释放）
//
// 同时引入分批上限 cleanupBatchSize，超大批量分多次 cleanupLoop tick 处理。
func (g *LoginGuard) cleanupExpired() {
	now := time.Now()

	// ── 阶段 1：RLock 快照待清理 keys ──

	// 1a. 快照 loginFailures 待删除 keys
	type failKey struct {
		username string
	}
	var failToDelete []failKey
	g.mu.RLock()
	for username, state := range g.loginFailures {
		if now.After(state.lockedUntil) && now.Sub(state.lastFailure) > time.Hour {
			failToDelete = append(failToDelete, failKey{username})
			if len(failToDelete) >= cleanupBatchSize {
				break
			}
		}
	}
	g.mu.RUnlock()

	// 1b. 快照 loginHistory 待清理条目（过期的 username + 需要裁剪的历史）
	type historyKey struct {
		username   string
		cutoffIdx  int // 需要裁剪的起始索引（>=0 表示需要处理）
		deleteAll  bool
	}
	var historyToDelete []historyKey
	historyCutoff := now.Add(-loginHistoryTTL)
	g.mu.RLock()
	for username, history := range g.loginHistory {
		if len(history) == 0 {
			historyToDelete = append(historyToDelete, historyKey{username: username, deleteAll: true})
			continue
		}
		cutoffIdx := 0
		for i, rec := range history {
			if rec.Timestamp.After(historyCutoff) {
				cutoffIdx = i
				break
			}
			cutoffIdx = i + 1
		}
		if cutoffIdx >= len(history) {
			historyToDelete = append(historyToDelete, historyKey{username: username, deleteAll: true})
		} else if cutoffIdx > 0 {
			historyToDelete = append(historyToDelete, historyKey{username: username, cutoffIdx: cutoffIdx})
		}
	}
	g.mu.RUnlock()

	// 1c. 快照 knownDevices/knownIPs 待清理 keys
	type deviceKey struct {
		username string
	}
	var deviceToDelete []deviceKey
	deviceCutoff := now.Add(-knownDeviceTTL)
	g.mu.RLock()
	for username := range g.knownDevices {
		history := g.loginHistory[username]
		if len(history) == 0 {
			deviceToDelete = append(deviceToDelete, deviceKey{username})
			continue
		}
		lastActive := history[len(history)-1].Timestamp
		if lastActive.Before(deviceCutoff) {
			deviceToDelete = append(deviceToDelete, deviceKey{username})
		}
	}
	// knownIPs 中不存在于 knownDevices 的残留
	var ipResidueToDelete []string
	for username := range g.knownIPs {
		if _, exists := g.knownDevices[username]; !exists {
			ipResidueToDelete = append(ipResidueToDelete, username)
		}
	}
	g.mu.RUnlock()

	// ── 阶段 2：逐条 Lock 删除（每次仅持锁极短时间） ──

	// 2a. 删除 loginFailures
	for _, fk := range failToDelete {
		g.mu.Lock()
		delete(g.loginFailures, fk.username)
		g.mu.Unlock()
	}

	// 2b. 删除/裁剪 loginHistory
	for _, hk := range historyToDelete {
		g.mu.Lock()
		if hk.deleteAll {
			delete(g.loginHistory, hk.username)
		} else if history, ok := g.loginHistory[hk.username]; ok && hk.cutoffIdx < len(history) {
			g.loginHistory[hk.username] = history[hk.cutoffIdx:]
		}
		g.mu.Unlock()
	}

	// 2c. 删除 knownDevices/knownIPs
	for _, dk := range deviceToDelete {
		g.mu.Lock()
		delete(g.knownDevices, dk.username)
		delete(g.knownIPs, dk.username)
		g.mu.Unlock()
	}

	// 2d. 删除 knownIPs 残留
	for _, username := range ipResidueToDelete {
		g.mu.Lock()
		delete(g.knownIPs, username)
		g.mu.Unlock()
	}
}

// SetGeoIPProvider 注入 IP 地理位置查询服务
func (g *LoginGuard) SetGeoIPProvider(provider GeoIPProvider) {
	g.mu.Lock()
	g.geoIPProvider = provider
	g.mu.Unlock()
}

// CheckLogin 检查登录请求是否允许（未锁定）
// 返回 (allowed, reason)
func (g *LoginGuard) CheckLogin(username, ip, fingerprint string) (bool, string) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	state, exists := g.loginFailures[username]
	if !exists {
		return true, ""
	}
	if time.Now().Before(state.lockedUntil) {
		remaining := state.lockedUntil.Sub(time.Now()).Round(time.Second)
		return false, fmt.Sprintf("账户已锁定，请 %v 后重试", remaining)
	}
	return true, ""
}

// RecordLoginSuccess 记录登录成功
// 返回告警信息（异地登录/新设备/多IP）
func (g *LoginGuard) RecordLoginSuccess(username, ip, userAgent, fingerprint string) *LoginAlert {
	g.mu.Lock()
	defer g.mu.Unlock()

	// 清除失败计数
	delete(g.loginFailures, username)

	// 记录登录历史
	record := &LoginRecord{
		Username:    username,
		IP:          ip,
		UserAgent:   userAgent,
		Fingerprint: fingerprint,
		Timestamp:   time.Now(),
		Success:     true,
	}

	// 查询地理位置
	if g.geoIPProvider != nil {
		country, province, city := g.geoIPProvider.Lookup(ip)
		record.Geo = fmt.Sprintf("%s %s %s", country, province, city)
	}

	history := g.loginHistory[username]
	history = append(history, record)
	if len(history) > g.historyLimit {
		history = history[len(history)-g.historyLimit:]
	}
	g.loginHistory[username] = history

	// 检测异常登录
	alert := g.detectAnomalyLocked(username, ip, fingerprint, record)

	// 记录已知设备和 IP
	if g.knownDevices[username] == nil {
		g.knownDevices[username] = make(map[string]bool)
	}
	g.knownDevices[username][fingerprint] = true

	if g.knownIPs[username] == nil {
		g.knownIPs[username] = make(map[string]bool)
	}
	g.knownIPs[username][ip] = true

	return alert
}

// RecordLoginFailure 记录登录失败
// 返回是否触发锁定
func (g *LoginGuard) RecordLoginFailure(username, ip string) (locked bool, lockoutUntil time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()

	state, exists := g.loginFailures[username]
	if !exists {
		state = &loginFailureState{}
		g.loginFailures[username] = state
	}

	state.count++
	state.lastFailure = time.Now()

	if state.count >= g.maxFailures {
		state.lockedUntil = time.Now().Add(g.lockoutDuration)
		g.logger.Warn("账户因多次登录失败被锁定",
			zap.String("username", username),
			zap.String("ip", ip),
			zap.Int("failures", state.count),
			zap.Time("locked_until", state.lockedUntil))
		return true, state.lockedUntil
	}

	g.logger.Warn("登录失败",
		zap.String("username", username),
		zap.String("ip", ip),
		zap.Int("failures", state.count),
		zap.Int("max", g.maxFailures))
	return false, time.Time{}
}

// LoginAlert 登录告警信息
type LoginAlert struct {
	Type    string `json:"type"`     // remote_login / new_device / multi_ip
	Message string `json:"message"`
	IP      string `json:"ip"`
	Geo     string `json:"geo,omitempty"`
}

// detectAnomalyLocked 检测异常登录（调用方需持锁）
func (g *LoginGuard) detectAnomalyLocked(username, ip, fingerprint string, record *LoginRecord) *LoginAlert {
	// 1. 新设备检测
	if g.knownDevices[username] != nil && len(g.knownDevices[username]) > 0 {
		if !g.knownDevices[username][fingerprint] {
			return &LoginAlert{
				Type:    "new_device",
				Message: fmt.Sprintf("用户 %s 从新设备登录（指纹: %s...）", username, fingerprint[:min(8, len(fingerprint))]),
				IP:      ip,
				Geo:     record.Geo,
			}
		}
	}

	// 2. 多 IP 检测（同用户短时间多 IP）
	// 注意：record 已被追加到 history 末尾，因此 history[len-1] 是当前记录，
	// 上一条记录在 history[len-2]。
	if g.knownIPs[username] != nil && len(g.knownIPs[username]) > 0 {
		if !g.knownIPs[username][ip] {
			history := g.loginHistory[username]
			if len(history) >= 2 {
				prev := history[len(history)-2]
				if prev.IP != ip && time.Since(prev.Timestamp) < 10*time.Minute {
					return &LoginAlert{
						Type:    "multi_ip",
						Message: fmt.Sprintf("用户 %s 短时间内从不同 IP 登录（%s → %s）", username, prev.IP, ip),
						IP:      ip,
						Geo:     record.Geo,
					}
				}
			}
		}
	}

	// 3. 异地登录检测（与上一次登录的地理位置对比）
	// 同理使用 history[len-2] 作为上一条记录。
	if g.geoIPProvider != nil {
		history := g.loginHistory[username]
		if len(history) >= 2 {
			prev := history[len(history)-2]
			if prev.Geo != "" && record.Geo != "" && prev.Geo != record.Geo {
				return &LoginAlert{
					Type:    "remote_login",
					Message: fmt.Sprintf("用户 %s 异地登录（上次: %s，本次: %s）", username, prev.Geo, record.Geo),
					IP:      ip,
					Geo:     record.Geo,
				}
			}
		}
	}

	return nil
}

// GetLoginHistory 获取用户登录历史
// INDUSTRIAL-FIX-2026-07-25-R12 [P2]: 返回深拷贝而非内部指针，防止外部修改破坏内部状态。
func (g *LoginGuard) GetLoginHistory(username string, limit int) []*LoginRecord {
	g.mu.RLock()
	defer g.mu.RUnlock()

	history := g.loginHistory[username]
	if len(history) == 0 {
		return []*LoginRecord{}
	}
	if limit <= 0 || limit > len(history) {
		limit = len(history)
	}
	// 返回最近 limit 条（倒序），深拷贝避免外部修改内部记录
	result := make([]*LoginRecord, 0, limit)
	for i := len(history) - 1; i >= 0 && len(result) < limit; i-- {
		cp := *history[i]
		result = append(result, &cp)
	}
	return result
}

// IsKnownDevice 检查是否为已知设备
func (g *LoginGuard) IsKnownDevice(username, fingerprint string) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	devices := g.knownDevices[username]
	return devices != nil && devices[fingerprint]
}

// ClearFailures 清除用户失败计数（管理员解锁）
func (g *LoginGuard) ClearFailures(username string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.loginFailures, username)
}

// ============================================================================
// 设备指纹生成（基于客户端特征）
// ============================================================================

// DeviceFingerprint 设备指纹特征
type DeviceFingerprint struct {
	UserAgent       string `json:"user_agent"`        // 浏览器 UA
	AcceptLanguage  string `json:"accept_language"`   // 语言偏好
	AcceptEncoding  string `json:"accept_encoding"`   // 编码偏好
	Screen          string `json:"screen"`            // 屏幕分辨率（前端采集）
	Timezone        string `json:"timezone"`          // 时区
	Platform        string `json:"platform"`          // 操作系统平台
	CanvasHash      string `json:"canvas_hash"`       // Canvas 指纹哈希（前端采集）
	WebGLHash       string `json:"webgl_hash"`        // WebGL 指纹哈希（前端采集）
	FontsHash       string `json:"fonts_hash"`        // 字体列表哈希（前端采集）
}

// GenerateFingerprint 根据设备特征生成指纹哈希（SM3 国密摘要）
// 前端采集设备特征后传入，后端生成统一指纹用于绑定校验
func GenerateFingerprint(fp DeviceFingerprint) string {
	// 拼接特征字符串（顺序固定确保稳定）
	raw := strings.Join([]string{
		fp.UserAgent,
		fp.AcceptLanguage,
		fp.AcceptEncoding,
		fp.Screen,
		fp.Timezone,
		fp.Platform,
		fp.CanvasHash,
		fp.WebGLHash,
		fp.FontsHash,
	}, "|")
	return gmsm.SM3HashHex([]byte(raw))[:32] // 取前 32 字符作为指纹
}

// ============================================================================
// IP 工具函数
// ============================================================================

// IsPrivateIP 判断是否为内网 IP
func IsPrivateIP(ipStr string) bool {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}
	// IPv4 私有地址段
	if ip4 := ip.To4(); ip4 != nil {
		switch {
		case ip4[0] == 10:
			return true
		case ip4[0] == 172 && ip4[1] >= 16 && ip4[1] <= 31:
			return true
		case ip4[0] == 192 && ip4[1] == 168:
			return true
		case ip4[0] == 127:
			return true // loopback
		}
	}
	// IPv6 loopback
	if ip.IsLoopback() {
		return true
	}
	return false
}

// GetClientIP 从请求中获取真实客户端 IP（穿透代理）
func GetClientIP(forwardedFor, realIP, remoteAddr string) string {
	// 优先 X-Forwarded-For（取第一个非内网 IP）
	if forwardedFor != "" {
		ips := strings.Split(forwardedFor, ",")
		for _, ip := range ips {
			ip = strings.TrimSpace(ip)
			if ip != "" && !IsPrivateIP(ip) {
				return ip
			}
		}
		// 全是内网 IP，取第一个
		if len(ips) > 0 {
			return strings.TrimSpace(ips[0])
		}
	}
	// X-Real-IP
	if realIP != "" {
		return strings.TrimSpace(realIP)
	}
	// RemoteAddr
	if remoteAddr != "" {
		host, _, err := net.SplitHostPort(remoteAddr)
		if err == nil {
			return host
		}
		return remoteAddr
	}
	return ""
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
