package module

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/hmac"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jte-engine/jte/internal/metrics"
	"github.com/jte-engine/jte/internal/util"
	"github.com/jte-engine/jte/pkg/merge"
	"go.uber.org/zap"
)

type License struct {
	ID                 string    `json:"id"`
	Modules            []string  `json:"modules"`
	ExpiresAt          time.Time `json:"expires_at"`
	CustomerID         string    `json:"customer_id"`
	IssuedAt           time.Time `json:"issued_at"`
	MachineFingerprint string    `json:"machine_fingerprint"`
	Signature          string    `json:"signature"`
	ActivatedAt        time.Time `json:"activated_at"`

	// AUTO-FIX-2026-06-30 [集成-6]: 存储分级定价 + 永久授权版本锁定
	// Tier 授权等级：free / standard / professional / enterprise
	Tier string `json:"tier,omitempty"`
	// MaxVehicles 最大车辆/设备数（0 表示使用配置默认值）
	MaxVehicles int `json:"max_vehicles,omitempty"`
	// MaxVGroups TDengine 最大 vgroups（0 表示使用配置默认值）
	MaxVGroups int `json:"max_vgroups,omitempty"`
	// MaxReplica TDengine 最大副本数（0 表示使用配置默认值）
	MaxReplica int `json:"max_replica,omitempty"`
	// Features 授权功能列表（如 "archive", "ai", "cluster", "video"）
	Features []string `json:"features,omitempty"`
	// MajorVersion 永久授权版本锁定（购买时的主版本号，如 3）
	// 永久授权仅含购买时主版本，大版本升级需付费（永久授权价 × 50%）
	MajorVersion int `json:"major_version,omitempty"`
	// LicenseType 授权类型：permanent（永久）/ subscription（订阅）
	LicenseType string `json:"license_type,omitempty"`
	// SupportTier 付费支持等级：none / standard / premium（单独购买）
	SupportTier string `json:"support_tier,omitempty"`
}

func (l *License) IsExpired() bool {
	return time.Now().After(l.ExpiresAt)
}

func (l *License) HasModule(moduleName string) bool {
	for _, m := range l.Modules {
		if m == moduleName {
			return true
		}
	}
	return false
}

type TrialInfo struct {
	ModuleName         string    `json:"module_name"`
	StartedAt          time.Time `json:"started_at"`
	ExpiresAt          time.Time `json:"expires_at"`
	MachineFingerprint string    `json:"machine_fingerprint"`
	FirstBootTime      time.Time `json:"first_boot_time"`
	Signature          string    `json:"signature"` // RSA-SHA256 签名（Base64），防篡改
}

func (t *TrialInfo) IsExpired() bool {
	return time.Now().After(t.ExpiresAt)
}

func (t *TrialInfo) RemainingDays() int {
	d := time.Until(t.ExpiresAt).Hours() / 24
	if d < 0 {
		return 0
	}
	return int(d)
}

// trialSigningPayload 构造试用记录的签名原文：module_name|started_at|expires_at|fingerprint|first_boot_time
func (t *TrialInfo) trialSigningPayload() string {
	return fmt.Sprintf("%s|%s|%s|%s|%s",
		t.ModuleName,
		t.StartedAt.Format(time.RFC3339Nano),
		t.ExpiresAt.Format(time.RFC3339Nano),
		t.MachineFingerprint,
		t.FirstBootTime.Format(time.RFC3339Nano),
	)
}

// signTrial 使用本机 RSA 私钥对试用记录签名。
func (m *LicenseManager) signTrial(t *TrialInfo) {
	if m.localKeys == nil {
		return
	}
	sig, err := m.localKeys.SignBase64([]byte(t.trialSigningPayload()))
	if err != nil {
		m.logger.Warn("failed to sign trial record", zap.Error(err))
		return
	}
	t.Signature = sig
}

// verifyTrialSignature 校验试用记录签名。签名缺失或校验失败返回 false。
func (m *LicenseManager) verifyTrialSignature(t *TrialInfo) bool {
	if t.Signature == "" {
		return false
	}
	if m.localKeys == nil {
		// 无本机密钥时跳过校验（兼容旧数据）
		return true
	}
	sigBytes, err := base64.StdEncoding.DecodeString(t.Signature)
	if err != nil {
		return false
	}
	return VerifyLocalSignature(&m.localKeys.PrivateKey.PublicKey, []byte(t.trialSigningPayload()), sigBytes) == nil
}

type LicenseManager struct {
	mu                 sync.RWMutex
	licenses           []*License
	trials             map[string]*TrialInfo
	fingerprint        string
	logger             *zap.Logger
	client             *WebsiteClient
	configDir          string
	dailyTick          *time.Ticker
	weeklyTick         *time.Ticker
	stopCh             chan struct{}
	stopOnce           sync.Once
	offlineCache       map[string]*offlineCacheEntry
	offlineCacheMaxDays int
	expiredDataRetention map[string]*expiredDataEntry
	eventBus             *merge.EventBus
	loader               *Loader
	localKeys            *LocalKeyPair
	// offlineUnbindSecret 是注入的离线解绑 HMAC 应用密钥（来自 config.Auth.OfflineUnbindSecret）。
	// AUTO-FIX-2026-06-29 [P2]: 替换原硬编码包级 secret，使各部署可用独立密钥。
	// 为 nil 时回退到 defaultOfflineUnbindHMACSecret（向后兼容已签发的旧凭证）。
	offlineUnbindSecret  []byte
	// offlineFailureSince 记录连续联网验证失败的起始时间。
	// AUTO-FIX-2026-06-30 [P2-9]: 连续 7 天无法联网验证 → 停用所有授权模块，
	// 防止攻击者通过断网绕过在线吊销检查。联网恢复后重置。
	offlineFailureSince  time.Time
	offlineDeactivateDays int
}

type offlineCacheEntry struct {
	LicenseID  string    `json:"license_id"`
	Modules    []string  `json:"modules"`
	ExpiresAt  time.Time `json:"expires_at"`
	CachedAt   time.Time `json:"cached_at"`
	ValidUntil time.Time `json:"valid_until"`
}

type expiredDataEntry struct {
	ModuleName   string    `json:"module_name"`
	ExpiredAt    time.Time `json:"expired_at"`
	DataDeleteAt time.Time `json:"data_delete_at"`
	ReadOnly     bool      `json:"read_only"`
}

var TrialModules = map[string]time.Duration{
	"protocol_809": 30 * 24 * time.Hour,
}

var (
	ErrInvalidLicense      = fmt.Errorf("invalid license key")
	ErrAlreadyBound        = fmt.Errorf("license already bound to another machine")
	ErrLicenseNotFound     = fmt.Errorf("license not found")
	ErrLicenseExpired      = fmt.Errorf("license expired")
	ErrUnbindFailed        = fmt.Errorf("unbind failed, please unbind on website")
)

func NewLicenseManager(logger *zap.Logger, client *WebsiteClient, configDir string, offlineUnbindSecret []byte) *LicenseManager {
	fp, err := GetMachineFingerprint()
	if err != nil {
		logger.Warn("failed to get machine fingerprint", zap.Error(err))
		fp = "unknown"
	}

	lm := &LicenseManager{
		fingerprint:           fp,
		logger:                logger,
		client:                client,
		configDir:             configDir,
		trials:                make(map[string]*TrialInfo),
		offlineCache:          make(map[string]*offlineCacheEntry),
		offlineCacheMaxDays:   7,
		expiredDataRetention:  make(map[string]*expiredDataEntry),
		stopCh:                make(chan struct{}),
		offlineUnbindSecret:   offlineUnbindSecret,
		offlineDeactivateDays: 7, // AUTO-FIX-2026-06-30 [P2-9]: 连续 7 天断网 → 停用模块
	}

	// 加载或生成本机 RSA 密钥对，用于试用记录签名和离线解绑凭证签名
	if keys, err := GenerateOrLoadLocalKeys(configDir); err != nil {
		logger.Warn("failed to load local RSA keys, signing disabled", zap.Error(err))
	} else {
		lm.localKeys = keys
		logger.Info("local RSA key pair loaded for trial/offline-unbind signing")
	}

	if err := lm.load(); err != nil {
		logger.Warn("failed to load licenses, starting fresh", zap.Error(err))
	}

	return lm
}

// SetEventBus 注入 EventBus，用于在 License 到期前主动推送事件通知前端。
func (m *LicenseManager) SetEventBus(eb *merge.EventBus) {
	m.eventBus = eb
}

// SetLoader 注入模块加载器，用于在授权移除/到期时联动停止并卸载对应模块。
func (m *LicenseManager) SetLoader(loader *Loader) {
	m.loader = loader
}

func (m *LicenseManager) Activate(licenseKey string) error {
	lic, err := parseAndVerifyLicense(licenseKey)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidLicense, err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, existing := range m.licenses {
		if existing.ID == lic.ID {
			return fmt.Errorf("license %s already activated", lic.ID)
		}
	}

	if lic.MachineFingerprint != "" {
		if lic.MachineFingerprint != m.fingerprint {
			return ErrAlreadyBound
		}
	} else {
		lic.MachineFingerprint = m.fingerprint
	}

	// AUTO-FIX-2026-06-30 [集成-6]: 永久授权版本锁定校验
	// 永久授权仅含购买时主版本，大版本升级需付费（原价 × 50%）。
	if lic.IsPermanent() && lic.MajorVersion > 0 {
		hostMajor, _, _, ok := parseSemVer(HostCoreVersion)
		if ok && hostMajor > lic.MajorVersion {
			return fmt.Errorf("permanent license locked to major version %d, current host is %d "+
				"(major version upgrade requires payment: 50%% of permanent license price)",
				lic.MajorVersion, hostMajor)
		}
	}

	lic.ActivatedAt = time.Now()

	m.licenses = append(m.licenses, lic)

	if m.client != nil {
		util.SafeGo(m.logger, "licenseManager.goroutine1", func() {
			if _, err := m.client.BindLicense(lic.ID, m.fingerprint); err != nil {
				m.logger.Warn("online bind failed", zap.Error(err))
			}
		})
	}

	if err := m.save(); err != nil {
		m.licenses = m.licenses[:len(m.licenses)-1]
		return fmt.Errorf("save license: %w", err)
	}

	m.logger.Info("license activated",
		zap.String("id", lic.ID),
		zap.Strings("modules", lic.Modules),
		zap.Time("expires_at", lic.ExpiresAt))

	// AUTO-FIX-2026-06-30 [集成-7]: 更新授权等级指标
	metrics.LicenseTier.Set(licenseTierRank(lic.Tier))

	return nil
}

// licenseTierRank 将授权等级字符串映射为数值（Prometheus gauge 友好）。
// 0=free, 1=standard, 2=professional, 3=enterprise
func licenseTierRank(tier string) float64 {
	switch tier {
	case TierFree:
		return 0
	case TierStandard:
		return 1
	case TierProfessional:
		return 2
	case TierEnterprise:
		return 3
	default:
		return 0
	}
}

// defaultOfflineUnbindHMACSecret is the built-in fallback secret mixed into
// the machine-fingerprint-derived HMAC key. It is ONLY used when the operator
// has not configured auth.offline_unbind_secret, preserving backward
// compatibility for offline unbind certificates signed before this fix.
//
// AUTO-FIX-2026-06-29 [P2]: 此前名为 offlineUnbindHMACSecret 且为唯一密钥来源，
// 源码公开后任何人都可伪造合法凭证。现已支持通过 config.Auth.OfflineUnbindSecret
// 注入部署专属密钥；该字段仅作为未配置时的兜底，不应在新部署中依赖。
var defaultOfflineUnbindHMACSecret = []byte("jte-offline-unbind-v1-secret")

// deriveOfflineUnbindKey derives an HMAC-SHA256 key from the machine
// fingerprint combined with the application secret. 使用注入的 secret；
// 若未注入（nil 或空）则回退到 defaultOfflineUnbindHMACSecret。
func (m *LicenseManager) deriveOfflineUnbindKey(fingerprint string) []byte {
	secret := m.offlineUnbindSecret
	if len(secret) == 0 {
		secret = defaultOfflineUnbindHMACSecret
	}
	h := sha256.Sum256(append([]byte(fingerprint), secret...))
	return h[:]
}

// deriveOfflineUnbindKeyWithSecret 是包级辅助函数，供 VerifyOfflineUnbindCert
// 在无 LicenseManager 实例时使用。secret 为 nil 或空时回退到默认值。
func deriveOfflineUnbindKeyWithSecret(fingerprint string, secret []byte) []byte {
	if len(secret) == 0 {
		secret = defaultOfflineUnbindHMACSecret
	}
	h := sha256.Sum256(append([]byte(fingerprint), secret...))
	return h[:]
}

// generateOfflineUnbindCert builds an offline unbind certificate for the given
// license id. 优先使用本机 RSA 私钥签名（UNBIND-RSA- 前缀），RSA 不可用时
// 降级为 HMAC-SHA256（UNBIND- 前缀，向后兼容）。
func (m *LicenseManager) generateOfflineUnbindCert(id string) (string, error) {
	if m.fingerprint == "" || m.fingerprint == "unknown" {
		return "", fmt.Errorf("machine fingerprint not available")
	}

	timestamp := time.Now().Unix()

	// 优先使用 RSA 签名
	if m.localKeys != nil {
		pubKeyPEM, err := m.localKeys.PublicKeyPEM()
		if err == nil && pubKeyPEM != "" {
			signingPayload := fmt.Sprintf("%s|%s|%d", id, m.fingerprint, timestamp)
			sig, err := m.localKeys.SignBase64([]byte(signingPayload))
			if err == nil {
				// 格式: licenseID|fingerprint|timestamp|publicKeyPEM(base64)|rsaSignature
				pubKeyB64 := base64.StdEncoding.EncodeToString([]byte(pubKeyPEM))
				certPayload := fmt.Sprintf("%s|%s|%d|%s|%s", id, m.fingerprint, timestamp, pubKeyB64, sig)
				return "UNBIND-RSA-" + base64.StdEncoding.EncodeToString([]byte(certPayload)), nil
			}
			m.logger.Warn("RSA signing for offline unbind failed, falling back to HMAC",
				zap.Error(err))
		}
	}

	// 降级：HMAC-SHA256
	signingPayload := fmt.Sprintf("%s|%s|%d", id, m.fingerprint, timestamp)
	key := m.deriveOfflineUnbindKey(m.fingerprint)
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(signingPayload))
	signature := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	certPayload := fmt.Sprintf("%s|%s|%d|%s", id, m.fingerprint, timestamp, signature)
	return "UNBIND-" + base64.StdEncoding.EncodeToString([]byte(certPayload)), nil
}

// Unbind releases the local license binding. When offline is true (or when no
// online client is configured) an offline unbind certificate is generated
// instead of contacting the website. The returned cert string is non-empty when
// the offline path is taken and should be sent to the official website to
// complete the unbind. When offline is false and online unbind fails, the
// method automatically falls back to offline mode.
func (m *LicenseManager) Unbind(id string, offline bool) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	lic := m.findByID(id)
	if lic == nil {
		return "", ErrLicenseNotFound
	}

	// Online path: only when not explicitly offline and a client is configured.
	if !offline && m.client != nil {
		if err := m.client.UnbindLicense(id, m.fingerprint); err != nil {
			m.logger.Warn("online unbind failed, falling back to offline mode",
				zap.String("id", id),
				zap.Error(err))
			cert, certErr := m.generateOfflineUnbindCert(id)
			if certErr != nil {
				return "", fmt.Errorf("%w: %v", ErrUnbindFailed, certErr)
			}
			lic.MachineFingerprint = ""
			if err := m.save(); err != nil {
				return cert, fmt.Errorf("save after unbind: %w", err)
			}
			m.logger.Warn("offline unbind certificate generated; please unbind on website manually",
				zap.String("id", id),
				zap.String("cert", cert))
			return cert, nil
		}

		lic.MachineFingerprint = ""
		if err := m.save(); err != nil {
			return "", fmt.Errorf("save after unbind: %w", err)
		}
		m.logger.Info("license unbound", zap.String("id", id))
		return "", nil
	}

	// Offline path: explicit offline mode or no client configured.
	cert, err := m.generateOfflineUnbindCert(id)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrUnbindFailed, err)
	}
	lic.MachineFingerprint = ""
	if err := m.save(); err != nil {
		return cert, fmt.Errorf("save after unbind: %w", err)
	}
	m.logger.Info("license unbound offline, certificate generated",
		zap.String("id", id),
		zap.String("cert", cert))
	return cert, nil
}

// VerifyOfflineUnbindCert parses and verifies an offline unbind certificate
// locally. 使用内置默认 secret 验证 HMAC 分支（向后兼容）。
// 新部署应改用 VerifyOfflineUnbindCertWithSecret 传入部署专属 secret。
//
// 支持两种格式：
//   - UNBIND-RSA-... : RSA-SHA256 签名（优先），自包含公钥，不依赖 HMAC secret
//   - UNBIND-...     : HMAC-SHA256 签名（降级兼容）
//
// Full revocation validation is performed on the official website; this helper
// only verifies structural and signature correctness.
func VerifyOfflineUnbindCert(cert string) (licenseID string, fingerprint string, timestamp int64, err error) {
	return VerifyOfflineUnbindCertWithSecret(cert, nil)
}

// VerifyOfflineUnbindCertWithSecret 与 VerifyOfflineUnbindCert 相同，但允许
// 注入部署专属 secret 验证 HMAC 分支。secret 为 nil 或空时回退到默认值。
// AUTO-FIX-2026-06-29 [P2]: 修复硬编码 secret 导致的凭证伪造风险。
func VerifyOfflineUnbindCertWithSecret(cert string, secret []byte) (licenseID string, fingerprint string, timestamp int64, err error) {
	// RSA 模式
	if strings.HasPrefix(cert, "UNBIND-RSA-") {
		decoded, dErr := base64.StdEncoding.DecodeString(strings.TrimPrefix(cert, "UNBIND-RSA-"))
		if dErr != nil {
			return "", "", 0, fmt.Errorf("decode RSA unbind certificate: %w", dErr)
		}

		parts := strings.Split(string(decoded), "|")
		if len(parts) != 5 {
			return "", "", 0, fmt.Errorf("invalid RSA unbind certificate: expected 5 fields, got %d", len(parts))
		}

		licenseID = parts[0]
		fingerprint = parts[1]
		timestamp, err = strconv.ParseInt(parts[2], 10, 64)
		if err != nil {
			return "", "", 0, fmt.Errorf("invalid timestamp in RSA unbind certificate: %w", err)
		}
		pubKeyB64 := parts[3]
		signatureB64 := parts[4]

		if licenseID == "" || fingerprint == "" {
			return "", "", 0, fmt.Errorf("invalid RSA unbind certificate: empty license id or fingerprint")
		}

		pubKeyBytes, dErr := base64.StdEncoding.DecodeString(pubKeyB64)
		if dErr != nil {
			return "", "", 0, fmt.Errorf("decode public key in unbind certificate: %w", dErr)
		}

		pubBlock, _ := pem.Decode(pubKeyBytes)
		if pubBlock == nil {
			return "", "", 0, fmt.Errorf("invalid public key PEM in unbind certificate")
		}

		rawPub, pErr := x509.ParsePKIXPublicKey(pubBlock.Bytes)
		if pErr != nil {
			return "", "", 0, fmt.Errorf("parse public key: %w", pErr)
		}

		rsaPub, ok := rawPub.(*rsa.PublicKey)
		if !ok {
			return "", "", 0, fmt.Errorf("unbind certificate public key is not RSA")
		}

		sigBytes, dErr := base64.StdEncoding.DecodeString(signatureB64)
		if dErr != nil {
			return "", "", 0, fmt.Errorf("decode RSA signature: %w", dErr)
		}

		signingPayload := fmt.Sprintf("%s|%s|%d", licenseID, fingerprint, timestamp)
		if vErr := VerifyLocalSignature(rsaPub, []byte(signingPayload), sigBytes); vErr != nil {
			return "", "", 0, fmt.Errorf("RSA unbind certificate signature verification failed: %w", vErr)
		}

		return licenseID, fingerprint, timestamp, nil
	}

	// HMAC 降级模式
	if !strings.HasPrefix(cert, "UNBIND-") {
		return "", "", 0, fmt.Errorf("invalid unbind certificate: missing UNBIND- prefix")
	}

	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(cert, "UNBIND-"))
	if err != nil {
		return "", "", 0, fmt.Errorf("decode unbind certificate: %w", err)
	}

	parts := strings.Split(string(decoded), "|")
	if len(parts) != 4 {
		return "", "", 0, fmt.Errorf("invalid unbind certificate: expected 4 fields, got %d", len(parts))
	}

	licenseID = parts[0]
	fingerprint = parts[1]
	timestamp, err = strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return "", "", 0, fmt.Errorf("invalid timestamp in unbind certificate: %w", err)
	}
	signature := parts[3]

	if licenseID == "" || fingerprint == "" {
		return "", "", 0, fmt.Errorf("invalid unbind certificate: empty license id or fingerprint")
	}

	key := deriveOfflineUnbindKeyWithSecret(fingerprint, secret)
	signingPayload := fmt.Sprintf("%s|%s|%d", licenseID, fingerprint, timestamp)
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(signingPayload))
	expectedSig := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	if !hmac.Equal([]byte(signature), []byte(expectedSig)) {
		return "", "", 0, fmt.Errorf("unbind certificate signature verification failed")
	}

	return licenseID, fingerprint, timestamp, nil
}

func (m *LicenseManager) Remove(id string) error {
	m.mu.Lock()

	lic := m.findByID(id)
	if lic == nil {
		m.mu.Unlock()
		return ErrLicenseNotFound
	}

	if m.client != nil {
		util.SafeGo(m.logger, "licenseManager.unbindLicense", func() {
			_ = m.client.UnbindLicense(id, m.fingerprint)
		})
	}

	// 收集被移除授权中的模块列表
	removedModules := make([]string, len(lic.Modules))
	copy(removedModules, lic.Modules)

	m.licenses = removeLicenseByID(m.licenses, id)

	if err := m.save(); err != nil {
		m.mu.Unlock()
		return fmt.Errorf("save after remove: %w", err)
	}

	m.mu.Unlock()

	// 联动停止并卸载不再被任何活跃授权/试用覆盖的模块
	m.unloadOrphanedModules(removedModules)

	m.logger.Info("license removed", zap.String("id", id))
	return nil
}

// unloadOrphanedModules 停止并卸载不再被任何活跃授权或试用覆盖的模块。
// 在锁外执行以避免与 Loader 内部锁产生死锁。
func (m *LicenseManager) unloadOrphanedModules(modules []string) {
	if m.loader == nil || len(modules) == 0 {
		return
	}

	for _, modName := range modules {
		// 仍被其他活跃授权或试用覆盖 → 跳过
		if m.HasModule(modName) {
			continue
		}
		// 模块未加载 → 跳过
		if !m.loader.IsLoaded(modName) {
			continue
		}

		m.logger.Info("unloading orphaned module after license removal",
			zap.String("module", modName))

		if err := m.loader.Stop(modName); err != nil {
			m.logger.Warn("failed to stop module after license removal",
				zap.String("module", modName),
				zap.Error(err))
			continue
		}

		// 卸载模块并清理数据（授权移除后数据不应保留）
		if err := m.loader.Unload(modName, true); err != nil {
			m.logger.Warn("failed to unload module after license removal",
				zap.String("module", modName),
				zap.Error(err))
		}

		// 发布模块卸载事件通知前端
		if m.eventBus != nil {
			m.eventBus.Publish(merge.EventTypeSystemEvent, map[string]interface{}{
				"type":   "module_unloaded",
				"module": modName,
				"reason": "license_removed",
			})
		}
	}
}

func (m *LicenseManager) ActiveModules() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	modules := make(map[string]bool)
	for _, lic := range m.licenses {
		if !lic.IsExpired() && m.licenseMatchesFingerprint(lic) {
			for _, mod := range lic.Modules {
				modules[mod] = true
			}
		}
	}

	result := make([]string, 0, len(modules))
	for mod := range modules {
		result = append(result, mod)
	}
	return result
}

// licenseMatchesFingerprint 校验授权的机器指纹是否与当前机器匹配。
// 纵深防御：即使加密存储被绕过，指纹不匹配的授权也不生效。
func (m *LicenseManager) licenseMatchesFingerprint(lic *License) bool {
	if lic.MachineFingerprint == "" {
		return true // 未绑定指纹的旧授权兼容
	}
	return lic.MachineFingerprint == m.fingerprint
}

func (m *LicenseManager) HasModule(moduleName string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, lic := range m.licenses {
		if !lic.IsExpired() && m.licenseMatchesFingerprint(lic) && lic.HasModule(moduleName) {
			return true
		}
	}

	if trial, ok := m.trials[moduleName]; ok && !trial.IsExpired() {
		return true
	}

	return false
}

// GetActiveLicense 返回当前最高等级的有效授权（AUTO-FIX-2026-06-30 [集成-6]）。
// 用于设备注册/TDengine 建库/归档等场景的配额校验。
// 无有效授权时返回 nil。
func (m *LicenseManager) GetActiveLicense() *License {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var best *License
	bestTierRank := -1
	tierRank := map[string]int{
		TierFree: 0, TierStandard: 1, TierProfessional: 2, TierEnterprise: 3,
	}
	for _, lic := range m.licenses {
		if lic.IsExpired() {
			continue
		}
		if !m.licenseMatchesFingerprint(lic) {
			continue
		}
		rank := tierRank[lic.Tier]
		if rank > bestTierRank {
			best = lic
			bestTierRank = rank
		}
	}
	return best
}

// ValidateVehicleCount 校验设备注册时的车辆数配额（集成-6）。
// 无有效授权时使用配置默认值（不阻断，仅警告）。
func (m *LicenseManager) ValidateVehicleCount(currentCount int) error {
	lic := m.GetActiveLicense()
	if lic == nil {
		// 无授权：允许使用配置默认值（向后兼容）
		return nil
	}
	return lic.ValidateVehicleCount(currentCount)
}

// ValidateVGroups 校验 TDengine 建库时的 vgroups 配额（集成-6）。
func (m *LicenseManager) ValidateVGroups(configuredVGroups int) error {
	lic := m.GetActiveLicense()
	if lic == nil {
		return nil
	}
	return lic.ValidateVGroups(configuredVGroups)
}

// ValidateReplica 校验 TDengine 建库时的 replica 配额（集成-6）。
func (m *LicenseManager) ValidateReplica(configuredReplica int) error {
	lic := m.GetActiveLicense()
	if lic == nil {
		return nil
	}
	return lic.ValidateReplica(configuredReplica)
}

// ValidateArchive 校验归档功能是否已授权（集成-6）。
func (m *LicenseManager) ValidateArchive() error {
	lic := m.GetActiveLicense()
	if lic == nil {
		return nil // 无授权时不阻断（向后兼容），由配额配置控制
	}
	return lic.ValidateArchive()
}

// ValidateMajorVersion 校验永久授权的版本锁定（集成-6）。
// 在 Activate 时调用，拒绝版本不兼容的永久授权。
func (m *LicenseManager) ValidateMajorVersion(currentMajor int) error {
	lic := m.GetActiveLicense()
	if lic == nil {
		return nil
	}
	return lic.ValidateMajorVersion(currentMajor)
}

// AutoStartTrials 自动为支持试用的未授权模块启动试用。
// 在引擎启动时调用，确保 809 等模块首次加载时自动获得 30 天试用。
// 已有授权或活跃试用的模块跳过；试用已过期或已用过的模块不重复启动。
func (m *LicenseManager) AutoStartTrials() {
	for modName := range TrialModules {
		if m.HasModule(modName) {
			continue // 已有授权或活跃试用
		}
		if err := m.StartTrial(modName); err != nil {
			m.logger.Debug("auto-trial not started",
				zap.String("module", modName),
				zap.Error(err))
		} else {
			m.logger.Info("auto-trial started for module",
				zap.String("module", modName))
		}
	}
}

func (m *LicenseManager) StartTrial(moduleName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkTrialStateFile(moduleName); err != nil {
		return err
	}

	for _, lic := range m.licenses {
		if !lic.IsExpired() && lic.HasModule(moduleName) {
			return fmt.Errorf("module %s already licensed", moduleName)
		}
	}

	if trial, ok := m.trials[moduleName]; ok {
		if trial.MachineFingerprint != "" && trial.MachineFingerprint != m.fingerprint {
			return fmt.Errorf("trial for %s is bound to another machine", moduleName)
		}
		if trial.IsExpired() {
			return fmt.Errorf("trial for %s already expired", moduleName)
		}
		return fmt.Errorf("trial for %s already active (expires %s)", moduleName, trial.ExpiresAt.Format("2006-01-02"))
	}

	duration, supported := TrialModules[moduleName]
	if !supported {
		return fmt.Errorf("module %s does not support trial", moduleName)
	}

	now := time.Now()
	trial := &TrialInfo{
		ModuleName:         moduleName,
		StartedAt:          now,
		ExpiresAt:          now.Add(duration),
		MachineFingerprint: m.fingerprint,
		FirstBootTime:      now,
	}
	// 使用本机 RSA 私钥签名试用记录，防止篡改时间字段
	m.signTrial(trial)
	m.trials[moduleName] = trial

	if err := m.save(); err != nil {
		delete(m.trials, moduleName)
		return fmt.Errorf("save trial: %w", err)
	}

	if err := m.saveTrialStateFile(moduleName, now); err != nil {
		m.logger.Warn("failed to save trial state file", zap.Error(err))
	}

	m.logger.Info("trial started",
		zap.String("module", moduleName),
		zap.Time("expires_at", now.Add(duration)))

	return nil
}

func (m *LicenseManager) GetTrials() map[string]*TrialInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string]*TrialInfo, len(m.trials))
	for k, v := range m.trials {
		result[k] = v
	}
	return result
}

func (m *LicenseManager) GetModuleStatus(moduleName string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, lic := range m.licenses {
		if lic.HasModule(moduleName) {
			if !m.licenseMatchesFingerprint(lic) {
				continue // 指纹不匹配，跳过
			}
			if lic.IsExpired() {
				return "expired"
			}
			if lic.ExpiresAt.Sub(time.Now()) < 7*24*time.Hour {
				return "expiring_soon"
			}
			return "licensed"
		}
	}

	if trial, ok := m.trials[moduleName]; ok {
		if trial.IsExpired() {
			return "trial_expired"
		}
		return "trial"
	}

	return "unlicensed"
}

func (m *LicenseManager) ListLicenses() []*License {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]*License, len(m.licenses))
	copy(result, m.licenses)
	return result
}

func (m *LicenseManager) GetStatus() interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()

	status := &LicenseStatus{
		MachineFingerprint: m.fingerprint,
		Licenses:           make([]LicenseInfo, 0, len(m.licenses)),
		ActiveModules:      make([]string, 0),
		Trials:             make(map[string]*TrialInfo),
	}

	moduleSet := make(map[string]bool)
	for _, lic := range m.licenses {
		info := LicenseInfo{
			ID:         lic.ID,
			Modules:    lic.Modules,
			ExpiresAt:  lic.ExpiresAt,
			CustomerID: lic.CustomerID,
			Expired:    lic.IsExpired(),
		}

		if !lic.IsExpired() {
			for _, mod := range lic.Modules {
				moduleSet[mod] = true
			}
		}

		status.Licenses = append(status.Licenses, info)
	}

	for mod, trial := range m.trials {
		status.Trials[mod] = trial
		if !trial.IsExpired() {
			moduleSet[mod] = true
		}
	}

	for mod := range moduleSet {
		status.ActiveModules = append(status.ActiveModules, mod)
	}

	return status
}

func (m *LicenseManager) StartValidation() {
	m.validateAll()

	m.dailyTick = time.NewTicker(24 * time.Hour)
	// AUTO-FIX-2026-06-30 [P2-9]: 联网验证频率从 7 天改为 1 天，
	// 降低攻击者通过断网绕过在线吊销的窗口期。
	m.weeklyTick = time.NewTicker(24 * time.Hour)

	util.SafeGo(m.logger, "licenseManager.goroutine3", func() {
		for {
			select {
			case <-m.dailyTick.C:
				m.validateAll()
			case <-m.weeklyTick.C:
				m.onlineValidate()
			case <-m.stopCh:
				return
			}
		}
	})

	m.logger.Info("license validation started",
		zap.String("fingerprint", m.fingerprint),
		zap.Int("licenses", len(m.licenses)),
		zap.Int("online_validate_interval_hours", 24),
		zap.Int("offline_deactivate_days", m.offlineDeactivateDays))
}

func (m *LicenseManager) StopValidation() {
	if m.dailyTick != nil {
		m.dailyTick.Stop()
	}
	if m.weeklyTick != nil {
		m.weeklyTick.Stop()
	}
	m.stopOnce.Do(func() { close(m.stopCh) })
}

func (m *LicenseManager) validateAll() {
	m.mu.RLock()
	defer m.mu.RUnlock()

	now := time.Now()
	for _, lic := range m.licenses {
		if lic.IsExpired() {
			m.logger.Warn("license expired",
				zap.String("id", lic.ID),
				zap.Time("expires_at", lic.ExpiresAt))
			m.publishLicenseExpired(lic)
		} else if lic.ExpiresAt.Sub(now) < 7*24*time.Hour {
			m.logger.Warn("license expiring soon",
				zap.String("id", lic.ID),
				zap.Time("expires_at", lic.ExpiresAt))
			days := int(lic.ExpiresAt.Sub(now).Hours() / 24)
			m.publishLicenseExpiringSoon(lic, days)
		}
	}

	for mod, trial := range m.trials {
		if trial.IsExpired() {
			m.logger.Warn("trial expired",
				zap.String("module", mod),
				zap.Time("expires_at", trial.ExpiresAt))
		} else if trial.ExpiresAt.Sub(now) < 3*24*time.Hour {
			m.logger.Warn("trial expiring soon",
				zap.String("module", mod),
				zap.Int("remaining_days", trial.RemainingDays()))
		}
	}
}

// publishLicenseExpiringSoon 在 License 即将到期（7 天内）时为每个授权模块
// 发布 system_event 通知前端。
func (m *LicenseManager) publishLicenseExpiringSoon(lic *License, days int) {
	if m.eventBus == nil {
		return
	}
	for _, mod := range lic.Modules {
		m.eventBus.Publish(merge.EventTypeSystemEvent, map[string]interface{}{
			"type":           "license_expiring_soon",
			"module":         mod,
			"expire_at":      lic.ExpiresAt.Format(time.RFC3339),
			"days_remaining": days,
		})
	}
}

// publishLicenseExpired 在 License 已到期时为每个授权模块发布 system_event
// 通知前端。
func (m *LicenseManager) publishLicenseExpired(lic *License) {
	if m.eventBus == nil {
		return
	}
	for _, mod := range lic.Modules {
		m.eventBus.Publish(merge.EventTypeSystemEvent, map[string]interface{}{
			"type":      "license_expired",
			"module":    mod,
			"expire_at": lic.ExpiresAt.Format(time.RFC3339),
		})
	}
}

func (m *LicenseManager) onlineValidate() {
	if m.client == nil {
		m.updateOfflineCache()
		m.checkOfflineDeactivation()
		return
	}

	m.mu.RLock()
	licenses := make([]*License, len(m.licenses))
	copy(licenses, m.licenses)
	m.mu.RUnlock()

	serverReachable := false
	for _, lic := range licenses {
		result, err := m.client.VerifyLicense(lic.ID, m.fingerprint)
		if err != nil {
			m.logger.Warn("online license verification failed, using offline cache",
				zap.String("id", lic.ID),
				zap.Error(err))
			continue
		}

		serverReachable = true

		if !result.Valid {
			m.logger.Warn("license revoked online",
				zap.String("id", lic.ID))

			m.mu.Lock()
			current := m.findByID(lic.ID)
			if current != nil {
				for _, mod := range current.Modules {
					m.markModuleExpired(mod)
				}
				m.licenses = removeLicenseByID(m.licenses, lic.ID)
				_ = m.save()
			}
			m.mu.Unlock()
		}
	}

	m.mu.Lock()
	if serverReachable {
		// 联网恢复：重置连续失败计时
		if !m.offlineFailureSince.IsZero() {
			m.logger.Info("online validation restored, offline failure counter reset",
				zap.Duration("offline_duration", time.Since(m.offlineFailureSince)))
		}
		m.offlineFailureSince = time.Time{}
	} else {
		// 连续失败：首次记录起始时间
		if m.offlineFailureSince.IsZero() {
			m.offlineFailureSince = time.Now()
			m.logger.Warn("online validation failed, starting offline failure timer",
				zap.Int("deactivate_after_days", m.offlineDeactivateDays))
		}
	}
	offlineFailureSince := m.offlineFailureSince
	deactivateDays := m.offlineDeactivateDays
	m.mu.Unlock()

	if serverReachable {
		m.syncAfterRecovery()
	} else {
		m.updateOfflineCache()
	}

	// AUTO-FIX-2026-06-30 [P2-9]: 连续断网超过阈值 → 停用所有授权模块
	if !offlineFailureSince.IsZero() && deactivateDays > 0 {
		if time.Since(offlineFailureSince) >= time.Duration(deactivateDays)*24*time.Hour {
			m.deactivateModulesDueToOfflineFailure(offlineFailureSince, deactivateDays)
		}
	}

	m.cleanupExpiredData()
}

// checkOfflineDeactivation 在无 client 配置时检查离线缓存是否过期需停用。
// AUTO-FIX-2026-06-30 [P2-9]: 无联网能力时基于离线缓存过期时间判定。
func (m *LicenseManager) checkOfflineDeactivation() {
	m.mu.Lock()
	if m.offlineFailureSince.IsZero() {
		m.offlineFailureSince = time.Now()
	}
	offlineFailureSince := m.offlineFailureSince
	deactivateDays := m.offlineDeactivateDays
	m.mu.Unlock()

	if deactivateDays > 0 && time.Since(offlineFailureSince) >= time.Duration(deactivateDays)*24*time.Hour {
		m.deactivateModulesDueToOfflineFailure(offlineFailureSince, deactivateDays)
	}
}

// deactivateModulesDueToOfflineFailure 停用所有授权模块（连续断网超阈值）。
// AUTO-FIX-2026-06-30 [P2-9]: 防止攻击者通过断网绕过在线吊销检查。
// 停用后模块不可用，联网恢复并验证通过后自动恢复（syncAfterRecovery 重建缓存）。
func (m *LicenseManager) deactivateModulesDueToOfflineFailure(since time.Time, days int) {
	m.mu.Lock()
	// 收集所有活跃模块
	modulesToDeactivate := make([]string, 0)
	moduleSet := make(map[string]bool)
	for _, lic := range m.licenses {
		if !lic.IsExpired() {
			for _, mod := range lic.Modules {
				if !moduleSet[mod] {
					moduleSet[mod] = true
					modulesToDeactivate = append(modulesToDeactivate, mod)
				}
			}
		}
	}
	// 标记所有 license 为已过期（停用模块访问）
	now := time.Now()
	for _, lic := range m.licenses {
		if !lic.IsExpired() {
			lic.ExpiresAt = now.Add(-1 * time.Second)
		}
	}
	// 清空离线缓存（强制重新联网验证）
	m.offlineCache = make(map[string]*offlineCacheEntry)
	_ = m.save()
	m.mu.Unlock()

	m.logger.Error("modules deactivated due to prolonged offline failure (possible network isolation attack)",
		zap.Time("offline_since", since),
		zap.Int("deactivate_threshold_days", days),
		zap.Duration("actual_offline_duration", time.Since(since)),
		zap.Strings("deactivated_modules", modulesToDeactivate))

	// 联动卸载模块
	if m.loader != nil && len(modulesToDeactivate) > 0 {
		for _, modName := range modulesToDeactivate {
			if !m.loader.IsLoaded(modName) {
				continue
			}
			if err := m.loader.Stop(modName); err != nil {
				m.logger.Warn("failed to stop module during offline deactivation",
					zap.String("module", modName),
					zap.Error(err))
				continue
			}
			if err := m.loader.Unload(modName, true); err != nil {
				m.logger.Warn("failed to unload module during offline deactivation",
					zap.String("module", modName),
					zap.Error(err))
			}
		}
	}

	// 发布系统事件通知前端
	if m.eventBus != nil {
		m.eventBus.Publish(merge.EventTypeSystemEvent, map[string]interface{}{
			"type":     "modules_deactivated_offline",
			"modules":  modulesToDeactivate,
			"reason":   "prolonged_offline_failure",
			"offline_since": since.Format(time.RFC3339),
		})
	}
}

func (m *LicenseManager) findByID(id string) *License {
	for _, lic := range m.licenses {
		if lic.ID == id {
			return lic
		}
	}
	return nil
}

type licenseStore struct {
	Licenses []*License            `json:"licenses"`
	Trials   map[string]*TrialInfo `json:"trials"`
}

func (m *LicenseManager) save() error {
	if m.configDir == "" {
		return nil
	}

	store := &licenseStore{
		Licenses: m.licenses,
		Trials:   m.trials,
	}

	// AUTO-FIX-2026-06-30 [P2-9]: AES-256-GCM 加密存储，密钥由机器指纹派生，
	// 防止明文文件被复制/篡改。详见 license_storage_crypto.go。
	return saveEncryptedLicenseStore(m.configDir, m.fingerprint, store, m.logger)
}

func (m *LicenseManager) load() error {
	if m.configDir == "" {
		return nil
	}

	// AUTO-FIX-2026-06-30 [P2-9]: 解密加载，兼容明文旧格式自动迁移。
	store, err := loadEncryptedLicenseStore(m.configDir, m.fingerprint, m.logger)
	if err != nil {
		return err
	}

	m.licenses = store.Licenses
	if store.Trials != nil {
		m.trials = store.Trials
	}

	// 校验试用记录签名：签名失败视为被篡改，立即标记为已过期
	for modName, trial := range m.trials {
		if !m.verifyTrialSignature(trial) {
			m.logger.Warn("trial record signature verification failed, marking as expired (possible tampering)",
				zap.String("module", modName))
			trial.ExpiresAt = time.Now().Add(-1 * time.Second)
		}
	}

	m.loadTrialStateFiles()
	return nil
}

type LicenseStatus struct {
	MachineFingerprint string              `json:"machine_fingerprint"`
	Licenses           []LicenseInfo       `json:"licenses"`
	ActiveModules      []string            `json:"active_modules"`
	Trials             map[string]*TrialInfo `json:"trials"`
}

type LicenseInfo struct {
	ID         string    `json:"id"`
	Modules    []string  `json:"modules"`
	ExpiresAt  time.Time `json:"expires_at"`
	CustomerID string    `json:"customer_id"`
	Expired    bool      `json:"expired"`
}

func parseAndVerifyLicense(licenseKey string) (*License, error) {
	parts := splitLicenseKey(licenseKey)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid license format")
	}

	payload, err := base64.StdEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("decode payload: %w", err)
	}

	signature, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("decode signature: %w", err)
	}

	// 公钥必须已初始化，否则拒绝任何 license（防止跳过签名验证）
	if parsedRSAPublicKey == nil && parsedECDSAPublicKey == nil {
		return nil, fmt.Errorf("%w: no public key available for verification", ErrInvalidLicense)
	}

	hash := sha256.Sum256(payload)
	verified := false
	if parsedECDSAPublicKey != nil {
		verified = ecdsa.VerifyASN1(parsedECDSAPublicKey, hash[:], signature)
	}
	if !verified && parsedRSAPublicKey != nil {
		if err := rsa.VerifyPKCS1v15(parsedRSAPublicKey, crypto.SHA256, hash[:], signature); err != nil {
			return nil, fmt.Errorf("%w: signature verification failed", ErrInvalidLicense)
		}
		verified = true
	}
	if !verified {
		return nil, fmt.Errorf("%w: signature verification failed", ErrInvalidLicense)
	}

	var lic License
	if err := json.Unmarshal(payload, &lic); err != nil {
		return nil, fmt.Errorf("unmarshal license: %w", err)
	}

	lic.Signature = parts[1]

	return &lic, nil
}

func splitLicenseKey(key string) []string {
	for i := len(key) - 1; i >= 0; i-- {
		if key[i] == '.' {
			return []string{key[:i], key[i+1:]}
		}
	}
	return []string{key}
}

func removeLicenseByID(licenses []*License, id string) []*License {
	for i, lic := range licenses {
		if lic.ID == id {
			return append(licenses[:i], licenses[i+1:]...)
		}
	}
	return licenses
}

type trialStateFile struct {
	ModuleName         string    `json:"module_name"`
	FirstStartedAt     time.Time `json:"first_started_at"`
	MachineFingerprint string    `json:"machine_fingerprint"`
	TrialCount         int       `json:"trial_count"`
}

func (m *LicenseManager) trialStateFilePath(moduleName string) string {
	return filepath.Join(m.configDir, fmt.Sprintf("trial_state_%s.json", moduleName))
}

func (m *LicenseManager) checkTrialStateFile(moduleName string) error {
	path := m.trialStateFilePath(moduleName)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return nil
	}

	var state trialStateFile
	if err := json.Unmarshal(data, &state); err != nil {
		return nil
	}

	if state.MachineFingerprint != "" && state.MachineFingerprint != m.fingerprint {
		return fmt.Errorf("trial for %s was previously started on a different machine", moduleName)
	}

	if state.TrialCount > 0 {
		if trial, ok := m.trials[moduleName]; ok && trial.IsExpired() {
			return fmt.Errorf("trial for %s has already been used and expired (cannot restart)", moduleName)
		}
		if _, ok := m.trials[moduleName]; !ok {
			return fmt.Errorf("trial for %s was previously used (cannot restart)", moduleName)
		}
	}

	return nil
}

func (m *LicenseManager) saveTrialStateFile(moduleName string, startedAt time.Time) error {
	if m.configDir == "" {
		return nil
	}

	if err := os.MkdirAll(m.configDir, 0700); err != nil {
		return err
	}

	path := m.trialStateFilePath(moduleName)

	var state trialStateFile
	existingData, err := os.ReadFile(path)
	if err == nil {
		_ = json.Unmarshal(existingData, &state)
	}

	state.ModuleName = moduleName
	state.MachineFingerprint = m.fingerprint
	state.TrialCount++
	if state.FirstStartedAt.IsZero() {
		state.FirstStartedAt = startedAt
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}

	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		return err
	}

	return os.Rename(tmpPath, path)
}

func (m *LicenseManager) loadTrialStateFiles() {
	if m.configDir == "" {
		return
	}

	entries, err := os.ReadDir(m.configDir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "trial_state_") || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		path := filepath.Join(m.configDir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		var state trialStateFile
		if err := json.Unmarshal(data, &state); err != nil {
			continue
		}

		if state.MachineFingerprint != "" && state.MachineFingerprint != m.fingerprint {
			m.logger.Warn("trial state fingerprint mismatch, marking trial as expired",
				zap.String("module", state.ModuleName))
			if trial, ok := m.trials[state.ModuleName]; ok {
				trial.ExpiresAt = time.Now().Add(-1 * time.Second)
			}
		}

		if state.TrialCount > 0 {
			if _, ok := m.trials[state.ModuleName]; !ok {
				m.logger.Warn("trial state exists but no active trial, blocking restart",
					zap.String("module", state.ModuleName))
			}
		}
	}
}

func (m *LicenseManager) HasModuleOffline(moduleName string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, lic := range m.licenses {
		if !lic.IsExpired() && m.licenseMatchesFingerprint(lic) && lic.HasModule(moduleName) {
			return true
		}
	}

	if trial, ok := m.trials[moduleName]; ok && !trial.IsExpired() {
		return true
	}

	if entry, ok := m.offlineCache[moduleName]; ok {
		if time.Now().Before(entry.ValidUntil) {
			return true
		}
	}

	return false
}

func (m *LicenseManager) updateOfflineCache() {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	for _, lic := range m.licenses {
		if lic.IsExpired() {
			continue
		}
		for _, mod := range lic.Modules {
			m.offlineCache[mod] = &offlineCacheEntry{
				LicenseID:  lic.ID,
				Modules:    lic.Modules,
				ExpiresAt:  lic.ExpiresAt,
				CachedAt:   now,
				ValidUntil: now.Add(time.Duration(m.offlineCacheMaxDays) * 24 * time.Hour),
			}
		}
	}

	for mod := range m.offlineCache {
		if now.After(m.offlineCache[mod].ValidUntil) {
			delete(m.offlineCache, mod)
		}
	}
}

func (m *LicenseManager) syncAfterRecovery() {
	if m.client == nil {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, lic := range m.licenses {
		result, err := m.client.VerifyLicense(lic.ID, m.fingerprint)
		if err != nil {
			m.logger.Warn("post-recovery sync verification failed",
				zap.String("id", lic.ID),
				zap.Error(err))
			continue
		}

		if result.Valid {
			for _, mod := range lic.Modules {
				m.offlineCache[mod] = &offlineCacheEntry{
					LicenseID:  lic.ID,
					Modules:    lic.Modules,
					ExpiresAt:  lic.ExpiresAt,
					CachedAt:   time.Now(),
					ValidUntil: time.Now().Add(time.Duration(m.offlineCacheMaxDays) * 24 * time.Hour),
				}
			}
		}
	}

	m.logger.Info("post-recovery license sync completed")
}

func (m *LicenseManager) IsModuleDataReadOnly(moduleName string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if entry, ok := m.expiredDataRetention[moduleName]; ok {
		return entry.ReadOnly
	}
	return false
}

func (m *LicenseManager) markModuleExpired(moduleName string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.expiredDataRetention[moduleName]; ok {
		return
	}

	now := time.Now()
	m.expiredDataRetention[moduleName] = &expiredDataEntry{
		ModuleName:   moduleName,
		ExpiredAt:    now,
		DataDeleteAt: now.Add(30 * 24 * time.Hour),
		ReadOnly:     true,
	}

	m.logger.Info("module data marked as read-only (30-day retention)",
		zap.String("module", moduleName),
		zap.Time("data_delete_at", now.Add(30*24*time.Hour)))
}

func (m *LicenseManager) restoreModuleAccess(moduleName string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.expiredDataRetention[moduleName]; ok {
		delete(m.expiredDataRetention, moduleName)
		m.logger.Info("module data access restored", zap.String("module", moduleName))
	}
}

func (m *LicenseManager) cleanupExpiredData() {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	for mod, entry := range m.expiredDataRetention {
		if now.After(entry.DataDeleteAt) {
			delete(m.expiredDataRetention, mod)
			m.logger.Info("expired module data auto-cleaned after 30 days",
				zap.String("module", mod))
		}
	}
}

func (m *LicenseManager) GetExpiredDataStatus() map[string]*expiredDataEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string]*expiredDataEntry, len(m.expiredDataRetention))
	for k, v := range m.expiredDataRetention {
		result[k] = v
	}
	return result
}