package module

import (
	"fmt"
	"time"
)

// RoleDef 角色定义（支持自定义角色）
type RoleDef struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`         // 角色标识（如 "operator"）
	DisplayName string       `json:"display_name"` // 显示名称（如 "操作员"）
	Permissions []Permission `json:"permissions"`
	BuiltIn     bool         `json:"built_in"` // 是否内置角色（不可删除）
	CreatedAt   time.Time    `json:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at"`
}

// CreateRole 创建自定义角色
func (m *RBACManager) CreateRole(name, displayName string, perms []Permission) (*RoleDef, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 校验角色名合法性
	if name == "" {
		return nil, fmt.Errorf("role name is required")
	}
	// 不能与内置角色重名
	if m.isBuiltInRole(name) {
		return nil, fmt.Errorf("cannot create built-in role: %s", name)
	}
	// 校验是否已存在
	if m.findRoleByNameLocked(name) != nil {
		return nil, fmt.Errorf("role %s already exists", name)
	}

	role := &RoleDef{
		ID:          generateID(),
		Name:        name,
		DisplayName: displayName,
		Permissions: perms,
		BuiltIn:     false,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	if m.roles == nil {
		m.roles = make(map[string]*RoleDef)
	}
	m.roles[role.ID] = role
	return role, nil
}

// UpdateRole 更新自定义角色（内置角色仅允许更新 Permissions/DisplayName）
func (m *RBACManager) UpdateRole(id string, displayName string, perms []Permission) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	role, ok := m.roles[id]
	if !ok {
		return fmt.Errorf("role %s not found", id)
	}
	role.DisplayName = displayName
	role.Permissions = perms
	role.UpdatedAt = time.Now()
	return nil
}

// DeleteRole 删除自定义角色（内置角色不可删除；已被用户引用的角色不可删除）
func (m *RBACManager) DeleteRole(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	role, ok := m.roles[id]
	if !ok {
		return fmt.Errorf("role %s not found", id)
	}
	if role.BuiltIn {
		return fmt.Errorf("cannot delete built-in role: %s", role.Name)
	}
	// 检查是否有用户仍在使用该角色
	for _, u := range m.users {
		if string(u.Role) == role.Name {
			return fmt.Errorf("role %s is in use by user %s", role.Name, u.Username)
		}
	}
	delete(m.roles, id)
	return nil
}

// ListRoles 列出所有角色（含内置角色）
func (m *RBACManager) ListRoles() []*RoleDef {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]*RoleDef, 0, len(m.roles))
	for _, r := range m.roles {
		result = append(result, r)
	}
	return result
}

// GetRole 查询单个角色
func (m *RBACManager) GetRole(id string) (*RoleDef, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	r, ok := m.roles[id]
	return r, ok
}

// GetRoleByName 按名称查询角色
func (m *RBACManager) GetRoleByName(name string) (*RoleDef, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.findRoleByNameLocked(name), true
}

// findRoleByNameLocked 内部查找（调用方需持锁）
func (m *RBACManager) findRoleByNameLocked(name string) *RoleDef {
	for _, r := range m.roles {
		if r.Name == name {
			return r
		}
	}
	return nil
}

// isBuiltInRole 判断是否为内置角色（调用方需持锁）
func (m *RBACManager) isBuiltInRole(name string) bool {
	switch Role(name) {
	case RoleSuperAdmin, RoleAdmin, RoleOperator, RoleReadOnly:
		return true
	}
	return false
}

// initBuiltinRoles 初始化内置角色到 m.roles（在 NewRBACManager 中调用）
func (m *RBACManager) initBuiltinRoles() {
	now := time.Now()
	builtins := []*RoleDef{
		{
			ID:          "role-super-admin",
			Name:        string(RoleSuperAdmin),
			DisplayName: "超级管理员",
			Permissions: RolePermissions[RoleSuperAdmin],
			BuiltIn:     true,
			CreatedAt:   now,
			UpdatedAt:   now,
		},
		{
			ID:          "role-admin",
			Name:        string(RoleAdmin),
			DisplayName: "管理员",
			Permissions: RolePermissions[RoleAdmin],
			BuiltIn:     true,
			CreatedAt:   now,
			UpdatedAt:   now,
		},
		{
			ID:          "role-operator",
			Name:        string(RoleOperator),
			DisplayName: "操作员",
			Permissions: RolePermissions[RoleOperator],
			BuiltIn:     true,
			CreatedAt:   now,
			UpdatedAt:   now,
		},
		{
			ID:          "role-readonly",
			Name:        string(RoleReadOnly),
			DisplayName: "只读用户",
			Permissions: RolePermissions[RoleReadOnly],
			BuiltIn:     true,
			CreatedAt:   now,
			UpdatedAt:   now,
		},
	}
	if m.roles == nil {
		m.roles = make(map[string]*RoleDef)
	}
	for _, r := range builtins {
		m.roles[r.ID] = r
	}
}
