package module

// ===================================================================
// AUTO-FIX-2026-06-30 [集成-6]: 存储分级定价 + 永久授权持续收入策略
//
// 授权等级（Tier）：
//   free         免费版（≤10 辆车，单节点，无归档/AI）
//   standard     入门版（≤10000 辆车，≤2 vgroups，无集群）
//   professional 标准版（≤100000 辆车，≤10 vgroups，含归档）
//   enterprise   企业版（≤1000000 辆车，集群，SRTP，全部功能）
//
// 永久授权版本锁定：
//   - 永久授权仅含购买时的主版本（MajorVersion）
//   - 小版本（MINOR.PATCH）免费升级
//   - 大版本升级费 = 永久授权原价 × 50%
//   - 付费支持单独购买（standard/premium）
//   - 云服务按次/按年收费（独立于授权码）
// ===================================================================

import (
	"fmt"
	"strings"
)

// 授权等级常量
const (
	TierFree         = "free"
	TierStandard     = "standard"
	TierProfessional = "professional"
	TierEnterprise   = "enterprise"
)

// 授权类型常量
const (
	LicenseTypePermanent   = "permanent"
	LicenseTypeSubscription = "subscription"
)

// 付费支持等级
const (
	SupportNone     = "none"
	SupportStandard = "standard"
	SupportPremium  = "premium"
)

// 功能标识常量（用于 Features 字段校验）
const (
	FeatureArchive  = "archive"
	FeatureAI       = "ai"
	FeatureCluster  = "cluster"
	FeatureVideo    = "video"
	FeatureSRTP     = "srtp"
	FeatureUnlimited = "unlimited"
)

// tierDefaults 各等级默认配额（当 License 字段为 0 时回退）
// 商业化要求：入门1万 / 标准10万 / 企业100万 车辆限额，超限拦截。
var tierDefaults = map[string]struct {
	MaxVehicles int
	MaxVGroups  int
	MaxReplica  int
	Features    []string
}{
	TierFree:         {10, 1, 1, nil},
	TierStandard:     {10000, 2, 1, []string{FeatureVideo}},
	TierProfessional: {100000, 10, 2, []string{FeatureArchive, FeatureVideo}},
	TierEnterprise:   {1000000, 0, 3, []string{FeatureArchive, FeatureAI, FeatureCluster, FeatureVideo, FeatureSRTP, FeatureUnlimited}},
}

// HasFeature 检查授权是否包含指定功能。
// enterprise 等级自动包含所有功能。
func (l *License) HasFeature(feature string) bool {
	for _, f := range l.Features {
		if f == feature || f == FeatureUnlimited {
			return true
		}
	}
	// 检查等级默认功能
	if defaults, ok := tierDefaults[l.Tier]; ok {
		for _, f := range defaults.Features {
			if f == feature || f == FeatureUnlimited {
				return true
			}
		}
	}
	return false
}

// IsPermanent 是否永久授权。
func (l *License) IsPermanent() bool {
	return strings.EqualFold(l.LicenseType, LicenseTypePermanent)
}

// IsSubscription 是否订阅授权。
func (l *License) IsSubscription() bool {
	return l.LicenseType == "" || strings.EqualFold(l.LicenseType, LicenseTypeSubscription)
}

// GetMaxVehicles 获取最大车辆数（License 值优先，回退到等级默认）。
// 返回 0 表示无限制（enterprise）。
func (l *License) GetMaxVehicles() int {
	if l.MaxVehicles > 0 {
		return l.MaxVehicles
	}
	if defaults, ok := tierDefaults[l.Tier]; ok {
		return defaults.MaxVehicles
	}
	return 0 // 未知等级，无限制
}

// GetMaxVGroups 获取最大 TDengine vgroups。
func (l *License) GetMaxVGroups() int {
	if l.MaxVGroups > 0 {
		return l.MaxVGroups
	}
	if defaults, ok := tierDefaults[l.Tier]; ok {
		return defaults.MaxVGroups
	}
	return 0
}

// GetMaxReplica 获取最大 TDengine 副本数。
func (l *License) GetMaxReplica() int {
	if l.MaxReplica > 0 {
		return l.MaxReplica
	}
	if defaults, ok := tierDefaults[l.Tier]; ok {
		return defaults.MaxReplica
	}
	return 0
}

// IsUnlimited 是否无限制（enterprise 或包含 unlimited feature）。
func (l *License) IsUnlimited() bool {
	if l.HasFeature(FeatureUnlimited) {
		return true
	}
	return l.Tier == TierEnterprise
}

// ValidateVehicleCount 校验车辆数是否在授权范围内。
// 所有等级（含 enterprise）均有硬性车辆上限，超限返回 error。
// enterprise 上限 100 万；超出需联系商务定制。
func (l *License) ValidateVehicleCount(currentCount int) error {
	max := l.GetMaxVehicles()
	if max > 0 && currentCount >= max {
		return fmt.Errorf("vehicle count %d exceeds license limit %d (tier=%s, please upgrade)",
			currentCount, max, l.Tier)
	}
	return nil
}

// ValidateVGroups 校验 TDengine vgroups 配置是否在授权范围内。
func (l *License) ValidateVGroups(configuredVGroups int) error {
	if l.IsUnlimited() {
		return nil
	}
	max := l.GetMaxVGroups()
	if max > 0 && configuredVGroups > max {
		return fmt.Errorf("TDengine vgroups %d exceeds license limit %d (tier=%s)",
			configuredVGroups, max, l.Tier)
	}
	return nil
}

// ValidateReplica 校验 TDengine replica 配置。
func (l *License) ValidateReplica(configuredReplica int) error {
	if l.IsUnlimited() {
		return nil
	}
	max := l.GetMaxReplica()
	if max > 0 && configuredReplica > max {
		return fmt.Errorf("TDengine replica %d exceeds license limit %d (tier=%s)",
			configuredReplica, max, l.Tier)
	}
	return nil
}

// ValidateArchive 校验归档功能是否已授权。
func (l *License) ValidateArchive() error {
	if !l.HasFeature(FeatureArchive) {
		return fmt.Errorf("archive feature not licensed (tier=%s, features=%v, please upgrade to professional or higher)",
			l.Tier, l.Features)
	}
	return nil
}

// ValidateMajorVersion 校验永久授权的版本锁定。
// 永久授权仅含购买时主版本，大版本升级需付费。
// currentMajor: 当前宿主主版本号
// 返回 error 时表示版本不兼容，需购买大版本升级。
func (l *License) ValidateMajorVersion(currentMajor int) error {
	if !l.IsPermanent() {
		return nil // 订阅授权不锁版本
	}
	if l.MajorVersion == 0 {
		return nil // 未声明版本，兼容旧授权
	}
	if currentMajor > l.MajorVersion {
		return fmt.Errorf("permanent license locked to major version %d, current is %d "+
			"(major version upgrade requires payment: 50%% of permanent license price, "+
			"contact sales to purchase upgrade)",
			l.MajorVersion, currentMajor)
	}
	return nil
}

// MajorVersionUpgradeFee 计算大版本升级费用。
// 公式：永久授权原价 × 50%
func MajorVersionUpgradeFee(originalPrice float64) float64 {
	return originalPrice * 0.5
}

// HasSupport 检查是否购买付费支持。
func (l *License) HasSupport() bool {
	return l.SupportTier != "" && l.SupportTier != SupportNone
}

// GetTierDefaults 获取指定等级的默认配额（测试/调试用）。
func GetTierDefaults(tier string) (maxVehicles, maxVGroups, maxReplica int, features []string, ok bool) {
	d, exists := tierDefaults[tier]
	if !exists {
		return 0, 0, 0, nil, false
	}
	return d.MaxVehicles, d.MaxVGroups, d.MaxReplica, d.Features, true
}

// ValidateTier 校验授权等级是否有效。
func ValidateTier(tier string) bool {
	switch strings.ToLower(tier) {
	case TierFree, TierStandard, TierProfessional, TierEnterprise:
		return true
	default:
		return false
	}
}
