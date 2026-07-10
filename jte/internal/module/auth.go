package module

import (
	"fmt"
	"time"
	"sync"

	"go.uber.org/zap"
)

type AuthManager struct {
	mu       sync.RWMutex
	cache    map[string]*AuthInfo
	logger   *zap.Logger
	client   *WebsiteClient
	cacheTTL time.Duration
}

type AuthInfo struct {
	LicenseKey      string    `json:"license_key"`
	MachineFP       string    `json:"machine_fingerprint"`
	Version         string    `json:"version"`
	ExpiresAt       time.Time `json:"expires_at"`
	BoundAt         time.Time `json:"bound_at"`
	CachedAt        time.Time `json:"cached_at"`
	Valid           bool      `json:"valid"`
}

func NewAuthManager(logger *zap.Logger, client *WebsiteClient) *AuthManager {
	return &AuthManager{
		cache:    make(map[string]*AuthInfo),
		logger:   logger,
		client:   client,
		cacheTTL: 7 * 24 * time.Hour,
	}
}

func (a *AuthManager) Login(licenseKey string) error {
	machineFP, err := GetMachineFingerprint()
	if err != nil {
		return fmt.Errorf("get machine fingerprint: %w", err)
	}

	result, err := a.client.BindLicense(licenseKey, machineFP)
	if err != nil {
		return fmt.Errorf("bind license: %w", err)
	}

	authInfo := &AuthInfo{
		LicenseKey: licenseKey,
		MachineFP:  machineFP,
		Version:    result.Version,
		ExpiresAt:  result.ExpiresAt,
		BoundAt:    time.Now(),
		CachedAt:   time.Now(),
		Valid:      true,
	}

	a.mu.Lock()
	a.cache[licenseKey] = authInfo
	a.mu.Unlock()

	a.logger.Info("license bound",
		zap.String("license_key", maskKey(licenseKey)),
		zap.String("version", result.Version))

	return nil
}

func (a *AuthManager) Logout(licenseKey string) error {
	machineFP, err := GetMachineFingerprint()
	if err != nil {
		return fmt.Errorf("get machine fingerprint: %w", err)
	}

	if err := a.client.UnbindLicense(licenseKey, machineFP); err != nil {
		a.logger.Warn("unbind license from server failed", zap.Error(err))
	}

	a.mu.Lock()
	delete(a.cache, licenseKey)
	a.mu.Unlock()

	a.logger.Info("license unbound", zap.String("license_key", maskKey(licenseKey)))
	return nil
}

func (a *AuthManager) Validate(licenseKey string) bool {
	a.mu.RLock()
	info, ok := a.cache[licenseKey]
	a.mu.RUnlock()

	if !ok {
		return false
	}

	if time.Since(info.CachedAt) > a.cacheTTL {
		result, err := a.client.VerifyLicense(licenseKey, info.MachineFP)
		if err != nil {
			a.logger.Warn("license verify failed, using cache", zap.Error(err))
			return info.Valid && time.Now().Before(info.ExpiresAt)
		}
		info.Valid = result.Valid
		info.CachedAt = time.Now()
	}

	return info.Valid && time.Now().Before(info.ExpiresAt)
}

func (a *AuthManager) GetAuthInfo(licenseKey string) (*AuthInfo, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	info, ok := a.cache[licenseKey]
	if !ok {
		return nil, false
	}
	return info, true
}

func maskKey(key string) string {
	if len(key) <= 8 {
		return "****"
	}
	return key[:4] + "****" + key[len(key)-4:]
}