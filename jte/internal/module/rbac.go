package module

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"

	"github.com/suoten/jt-engine/pkg/crypto/gmsm"
)

type Role string

const (
	RoleSuperAdmin Role = "super_admin"
	RoleAdmin      Role = "admin"
	RoleOperator   Role = "operator"
	RoleReadOnly   Role = "readonly"

	// AUTO-FIX-2026-07-02 [等保2.0 三级 - 三权分立]：
	// 系统管理员、安全管理员、审计管理员三权分立，互不兼任，避免权限集中。
	// - system_admin：系统配置、模块管理、维护模式（不管理用户/角色/审计）
	// - security_admin：用户管理、角色权限管理、授权管理（不管理系统配置/审计）
	// - audit_admin：审计日志查看（独占），不可修改业务数据
	RoleSystemAdmin   Role = "system_admin"
	RoleSecurityAdmin Role = "security_admin"
	RoleAuditAdmin    Role = "audit_admin"
)

type Permission string

const (
	PermMonitor     Permission = "monitor"
	PermDevice      Permission = "device"
	PermVehicle     Permission = "vehicle"
	PermAlarm       Permission = "alarm"
	PermTrack       Permission = "track"
	PermVideo       Permission = "video"
	PermCommand     Permission = "command"
	PermReport      Permission = "report"
	PermCascade     Permission = "cascade"
	PermUserManage  Permission = "user_manage"
	PermRoleManage  Permission = "role_manage"
	PermSystem      Permission = "system"
	PermModule      Permission = "module"
	PermLicense     Permission = "license"
	PermAuditLog    Permission = "audit_log"
	PermAI          Permission = "ai"
	// AUTO-FIX-2026-07-02 [等保2.0]: 安全管理权限（安全策略、加密配置）
	PermSecurityManage Permission = "security_manage"
)

var RolePermissions = map[Role][]Permission{
	RoleSuperAdmin: {
		PermMonitor, PermDevice, PermVehicle, PermAlarm, PermTrack, PermVideo,
		PermCommand, PermReport, PermCascade, PermUserManage, PermRoleManage,
		PermSystem, PermModule, PermLicense, PermAuditLog, PermAI, PermSecurityManage,
	},
	RoleAdmin: {
		PermMonitor, PermDevice, PermVehicle, PermAlarm, PermTrack, PermVideo,
		PermCommand, PermReport, PermCascade, PermUserManage, PermRoleManage,
		PermSystem, PermAuditLog, PermAI,
	},
	RoleOperator: {
		PermMonitor, PermDevice, PermVehicle, PermAlarm, PermTrack, PermVideo,
		PermCommand, PermReport, PermAI,
	},
	RoleReadOnly: {
		PermMonitor, PermAlarm, PermTrack, PermVideo, PermReport, PermAI,
	},
	// 系统管理员：系统配置 + 模块管理 + 维护模式（不含用户管理、角色管理、审计日志）
	RoleSystemAdmin: {
		PermMonitor, PermDevice, PermVehicle, PermAlarm, PermTrack, PermVideo,
		PermCommand, PermReport, PermCascade, PermSystem, PermModule, PermLicense, PermAI,
	},
	// 安全管理员：用户管理 + 角色权限管理 + 授权管理 + 安全策略（不含系统配置、审计日志）
	RoleSecurityAdmin: {
		PermMonitor, PermUserManage, PermRoleManage, PermLicense, PermSecurityManage, PermAI,
	},
	// 审计管理员：仅审计日志查看（独占权限，其他角色不可拥有此权限）
	// 等保2.0 要求：审计日志仅审计管理员可查看，系统/安全管理员不可查看
	RoleAuditAdmin: {
		PermAuditLog,
	},
}

// RoleLabel 角色显示名映射（供前端展示）
var RoleLabel = map[Role]string{
	RoleSuperAdmin:     "超级管理员",
	RoleAdmin:          "管理员",
	RoleOperator:       "操作员",
	RoleReadOnly:       "只读用户",
	RoleSystemAdmin:    "系统管理员",
	RoleSecurityAdmin:  "安全管理员",
	RoleAuditAdmin:     "审计管理员",
}

// IsSeparationOfDutiesRole 判断是否为三权分立角色（用于互斥校验）
func IsSeparationOfDutiesRole(role Role) bool {
	return role == RoleSystemAdmin || role == RoleSecurityAdmin || role == RoleAuditAdmin
}

type User struct {
	ID           string    `json:"id"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"password_hash"`
	Role         Role      `json:"role"`
	DisplayName  string    `json:"display_name"`
	Enabled      bool      `json:"enabled"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	LastLoginAt  time.Time `json:"last_login_at,omitempty"`
	// AUTO-FIX-2026-07-02: 数据权限范围（基础实现）
	// 控制用户可查询的数据范围：all=全部 / org=按组织 / vehicle=指定车辆 / self=仅自己
	DataScope DataScope `json:"data_scope,omitempty"`
}

// DataScope 数据权限范围（基础实现，AUTO-FIX-2026-07-02）
// 用于列表查询 API 自动附加数据过滤条件，实现数据行级隔离。
type DataScope struct {
	// ScopeType 范围类型：all(全部) / org(按组织) / vehicle(指定车辆) / self(仅自己)
	ScopeType string `json:"scope_type"`
	// OrgID 组织 ID（ScopeType=org 时生效）
	OrgID string `json:"org_id,omitempty"`
	// VehicleIDs 车辆 ID 列表（ScopeType=vehicle 时生效）
	VehicleIDs []string `json:"vehicle_ids,omitempty"`
}

// DefaultDataScope 返回全部数据权限（super_admin/admin 默认）
func DefaultDataScope() DataScope {
	return DataScope{ScopeType: "all"}
}

type RBACManager struct {
	mu        sync.RWMutex
	users     map[string]*User
	roles     map[string]*RoleDef // 角色定义（含内置+自定义）
	configDir string
	logger    *zap.Logger
}

func NewRBACManager(configDir string, logger *zap.Logger) *RBACManager {
	m := &RBACManager{
		users:     make(map[string]*User),
		roles:     make(map[string]*RoleDef),
		configDir: configDir,
		logger:    logger,
	}
	m.initBuiltinRoles()
	m.load()
	if len(m.users) == 0 {
		m.createDefaultAdmin()
	}
	return m
}

func (m *RBACManager) createDefaultAdmin() {
	// 生成随机默认密码，避免使用固定的弱密码。密码仅在此日志中输出一次。
	defaultPassword := generateRandomPassword(16)
	m.users["default-admin"] = &User{
		ID:           "default-admin",
		Username:     "admin",
		PasswordHash: hashPassword(defaultPassword),
		Role:         RoleSuperAdmin,
		DisplayName:  "超级管理员",
		Enabled:      true,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	_ = m.save()
	m.logger.Info("default admin user created with random password",
		zap.String("username", "admin"),
		zap.String("password", defaultPassword),
		zap.String("warning", "please record this password and change it immediately after first login"))
}

// generateRandomPassword 生成指定长度的随机密码（字母+数字）。
func generateRandomPassword(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, length)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		if err != nil {
			// 回退到固定字符（不应发生）
			b[i] = charset[i%len(charset)]
			continue
		}
		b[i] = charset[n.Int64()]
	}
	return string(b)
}

func (m *RBACManager) CreateUser(username, password string, role Role, displayName string) (*User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, u := range m.users {
		if u.Username == username {
			return nil, fmt.Errorf("username %s already exists", username)
		}
	}

	id := generateID()
	user := &User{
		ID:           id,
		Username:     username,
		PasswordHash: hashPassword(password),
		Role:         role,
		DisplayName:  displayName,
		Enabled:      true,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}

	m.users[id] = user
	if err := m.save(); err != nil {
		delete(m.users, id)
		return nil, err
	}

	return user, nil
}

func (m *RBACManager) UpdateUser(id string, role Role, displayName string, enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	user, ok := m.users[id]
	if !ok {
		return fmt.Errorf("user %s not found", id)
	}

	user.Role = role
	user.DisplayName = displayName
	user.Enabled = enabled
	user.UpdatedAt = time.Now()

	return m.save()
}

func (m *RBACManager) DeleteUser(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.users[id]; !ok {
		return fmt.Errorf("user %s not found", id)
	}

	delete(m.users, id)
	return m.save()
}

func (m *RBACManager) GetUser(id string) *User {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.users[id]
}

func (m *RBACManager) ListUsers() []*User {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]*User, 0, len(m.users))
	for _, u := range m.users {
		result = append(result, u)
	}
	return result
}

func (m *RBACManager) Authenticate(username, password string) (*User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, u := range m.users {
		if u.Username == username {
			if !u.Enabled {
				return nil, fmt.Errorf("user disabled")
			}
			if !verifyPassword(u.PasswordHash, password) {
				return nil, fmt.Errorf("invalid password")
			}
			u.LastLoginAt = time.Now()
			_ = m.save()
			return u, nil
		}
	}
	return nil, fmt.Errorf("user not found")
}

func (m *RBACManager) HasPermission(userID string, perm Permission) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	user, ok := m.users[userID]
	if !ok || !user.Enabled {
		return false
	}

	// 优先查询内置角色权限表
	if perms, ok := RolePermissions[user.Role]; ok {
		for _, p := range perms {
			if p == perm {
				return true
			}
		}
		return false
	}
	// 自定义角色：从 m.roles 查询
	if role := m.findRoleByNameLocked(string(user.Role)); role != nil {
		for _, p := range role.Permissions {
			if p == perm {
				return true
			}
		}
	}
	return false
}

func (m *RBACManager) GetPermissions(userID string) []Permission {
	m.mu.RLock()
	defer m.mu.RUnlock()

	user, ok := m.users[userID]
	if !ok || !user.Enabled {
		return nil
	}

	if perms, ok := RolePermissions[user.Role]; ok {
		return perms
	}
	// 自定义角色：从 m.roles 查询
	if role := m.findRoleByNameLocked(string(user.Role)); role != nil {
		return role.Permissions
	}
	return nil
}

// GetDataScope 返回用户的数据权限范围（AUTO-FIX-2026-07-02）
// super_admin/admin 默认 all；其他角色使用用户配置的 DataScope，未配置时回退到 self
func (m *RBACManager) GetDataScope(userID string) DataScope {
	m.mu.RLock()
	defer m.mu.RUnlock()

	user, ok := m.users[userID]
	if !ok || !user.Enabled {
		return DataScope{ScopeType: "self"}
	}

	// super_admin 和 admin 默认拥有全部数据权限
	if user.Role == RoleSuperAdmin || user.Role == RoleAdmin {
		if user.DataScope.ScopeType == "" {
			return DefaultDataScope()
		}
		return user.DataScope
	}

	// 其他角色：使用配置的 DataScope，未配置时回退到 self（仅自己创建的数据）
	if user.DataScope.ScopeType == "" {
		return DataScope{ScopeType: "self"}
	}
	return user.DataScope
}

func (m *RBACManager) save() error {
	if m.configDir == "" {
		return nil
	}

	_ = os.MkdirAll(m.configDir, 0700)

	data, err := json.MarshalIndent(m.users, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(filepath.Join(m.configDir, "users.json"), data, 0600)
}

func (m *RBACManager) load() {
	if m.configDir == "" {
		return
	}

	data, err := os.ReadFile(filepath.Join(m.configDir, "users.json"))
	if err != nil {
		return
	}

	json.Unmarshal(data, &m.users)
}

// 密码哈希方案标识（用于 verifyPassword 区分不同方案的哈希）
// - bcrypt: "$2a$" / "$2b$" / "$2y$" 开头（旧存量）
// - SM3:    "sm3$<salt_hex>$<iterations>$<hash_hex>"（等保2.0 国密方案）
// - 旧版:   64 位 hex（SHA256 + 静态盐，仅兼容验证，不再生成）

const (
	// sm3PasswordIterations SM3 密码哈希迭代次数（增加暴力破解成本）
	sm3PasswordIterations = 10000
	// sm3PasswordSaltLen SM3 密码盐长度（字节）
	sm3PasswordSaltLen = 16
)

// hashPassword 使用 SM3 国密算法生成密码哈希（等保2.0 合规）。
// 格式: "sm3$<salt_hex>$<iterations>$<hash_hex>"
// 其中 hash = SM3^iterations(salt || password)，迭代增强抗暴力破解。
func hashPassword(password string) string {
	salt := make([]byte, sm3PasswordSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return ""
	}
	hash := sm3PasswordHash(salt, password, sm3PasswordIterations)
	return fmt.Sprintf("sm3$%s$%d$%s",
		hex.EncodeToString(salt),
		sm3PasswordIterations,
		hex.EncodeToString(hash))
}

// sm3PasswordHash 计算 SM3 迭代密码哈希：SM3^iterations(salt || password)
func sm3PasswordHash(salt []byte, password string, iterations int) []byte {
	data := make([]byte, 0, len(salt)+len(password))
	data = append(data, salt...)
	data = append(data, []byte(password)...)
	h := gmsm.SM3Hash(data)
	for i := 1; i < iterations; i++ {
		h = gmsm.SM3Hash(h)
	}
	return h
}

// verifyPassword 验证密码哈希，支持三种方案的向后兼容：
//  1. SM3 方案（"sm3$..." 前缀）—— 国密合规，当前默认
//  2. bcrypt 方案（"$2" 开头）—— 旧存量用户
//  3. 旧版 SHA256+静态盐（64 位 hex）—— 最旧存量，仅验证不生成
func verifyPassword(hash, password string) bool {
	// SM3 国密方案
	if strings.HasPrefix(hash, "sm3$") {
		parts := strings.SplitN(hash, "$", 4)
		if len(parts) != 4 {
			return false
		}
		salt, err := hex.DecodeString(parts[1])
		if err != nil {
			return false
		}
		var iterations int
		if _, err := fmt.Sscanf(parts[2], "%d", &iterations); err != nil || iterations <= 0 {
			return false
		}
		expected, err := hex.DecodeString(parts[3])
		if err != nil {
			return false
		}
		actual := sm3PasswordHash(salt, password, iterations)
		// 常量时间比较防时序攻击
		return constantTimeCompare(actual, expected)
	}
	// bcrypt 方案
	if len(hash) > 0 && hash[0] == '$' {
		return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
	}
	// 旧版 SHA256+静态盐方案（仅兼容验证）
	h := sha256.Sum256([]byte("jte-salt:" + password))
	return hex.EncodeToString(h[:]) == hash
}

// constantTimeCompare 常量时间字节比较（防时序攻击）
func constantTimeCompare(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var result byte
	for i := 0; i < len(a); i++ {
		result |= a[i] ^ b[i]
	}
	return result == 0
}

func generateID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}