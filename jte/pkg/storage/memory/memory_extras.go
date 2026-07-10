package memory

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/jte-engine/jte/pkg/storage"
)

// QueryDrivers 查询驾驶员信息列表
func (m *MemoryStore) QueryDrivers(ctx context.Context, opts storage.ListOptions) (*storage.ListResult, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var items []*storage.DriverInfoData
	for _, d := range m.driverInfo {
		if opts.Phone != "" && d.Phone != opts.Phone {
			continue
		}
		items = append(items, d)
	}

	// 按接收时间倒序
	sort.Slice(items, func(i, j int) bool {
		return items[i].ReceivedAt.After(items[j].ReceivedAt)
	})

	total := int64(len(items))
	page := opts.Page
	if page < 1 {
		page = 1
	}
	pageSize := opts.PageSize
	if pageSize < 1 {
		pageSize = 20
	}
	start := (page - 1) * pageSize
	end := start + pageSize
	if start > len(items) {
		start = len(items)
	}
	if end > len(items) {
		end = len(items)
	}

	return &storage.ListResult{
		Items: items[start:end],
		Total: total,
		Page:  page,
		Size:  pageSize,
	}, nil
}

// DeleteDriver 删除驾驶员信息
func (m *MemoryStore) DeleteDriver(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, d := range m.driverInfo {
		if d.ID == id {
			m.driverInfo = append(m.driverInfo[:i], m.driverInfo[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("driver not found: %s", id)
}

// SaveGeofence 保存电子围栏
func (m *MemoryStore) SaveGeofence(ctx context.Context, g *storage.Geofence) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if g.CreatedAt.IsZero() {
		g.CreatedAt = time.Now()
	}
	g.UpdatedAt = time.Now()
	m.geofences[g.ID] = g
	return nil
}

// GetGeofence 查询单个电子围栏
func (m *MemoryStore) GetGeofence(ctx context.Context, id string) (*storage.Geofence, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	g, ok := m.geofences[id]
	if !ok {
		return nil, fmt.Errorf("geofence not found: %s", id)
	}
	return g, nil
}

// ListGeofences 查询电子围栏列表
func (m *MemoryStore) ListGeofences(ctx context.Context, opts storage.ListOptions) (*storage.ListResult, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var items []*storage.Geofence
	for _, g := range m.geofences {
		if opts.OrgID != "" && g.OrgID != opts.OrgID {
			continue
		}
		items = append(items, g)
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})

	total := int64(len(items))
	page := opts.Page
	if page < 1 {
		page = 1
	}
	pageSize := opts.PageSize
	if pageSize < 1 {
		pageSize = 20
	}
	start := (page - 1) * pageSize
	end := start + pageSize
	if start > len(items) {
		start = len(items)
	}
	if end > len(items) {
		end = len(items)
	}

	return &storage.ListResult{
		Items: items[start:end],
		Total: total,
		Page:  page,
		Size:  pageSize,
	}, nil
}

// DeleteGeofence 删除电子围栏
func (m *MemoryStore) DeleteGeofence(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.geofences[id]; !ok {
		return fmt.Errorf("geofence not found: %s", id)
	}
	delete(m.geofences, id)
	return nil
}

// AUTO-FIX-2026-06-29 [P1-6]: 809 转发规则内存实现（与 SQLite 实现语义一致）

// SaveForwardRule 保存或更新转发规则。
func (m *MemoryStore) SaveForwardRule(ctx context.Context, rule *storage.ForwardRule) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if rule.CreatedAt.IsZero() {
		rule.CreatedAt = time.Now()
	}
	rule.UpdatedAt = time.Now()
	// 深拷贝避免外部修改污染内部状态
	cp := *rule
	m.forwardRules[rule.ID] = &cp
	return nil
}

// GetForwardRule 按主键查询单条转发规则。
func (m *MemoryStore) GetForwardRule(ctx context.Context, id string) (*storage.ForwardRule, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	r, ok := m.forwardRules[id]
	if !ok {
		return nil, fmt.Errorf("forward rule not found: %s", id)
	}
	cp := *r
	return &cp, nil
}

// ListForwardRules 按上级平台 ID 查询转发规则列表。
// platformID 为空时返回全部规则。
func (m *MemoryStore) ListForwardRules(ctx context.Context, platformID string) ([]*storage.ForwardRule, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	items := make([]*storage.ForwardRule, 0, len(m.forwardRules))
	for _, r := range m.forwardRules {
		if platformID != "" && r.PlatformID != platformID {
			continue
		}
		cp := *r
		items = append(items, &cp)
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].CreatedAt.Before(items[j].CreatedAt)
	})
	return items, nil
}

// DeleteForwardRule 按主键删除转发规则。
func (m *MemoryStore) DeleteForwardRule(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.forwardRules[id]; !ok {
		return fmt.Errorf("forward rule not found: %s", id)
	}
	delete(m.forwardRules, id)
	return nil
}

// AUTO-FIX-2026-07-02 [P1]: 809 级联平台配置内存实现（与 SQLite 实现语义一致）

// SavePlatform 保存或更新平台配置。
func (m *MemoryStore) SavePlatform(ctx context.Context, p *storage.Platform) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p.CreatedAt.IsZero() {
		p.CreatedAt = time.Now()
	}
	p.UpdatedAt = time.Now()
	// 深拷贝避免外部修改污染内部状态
	cp := *p
	m.platforms[p.ID] = &cp
	return nil
}

// GetPlatform 按主键查询单条平台配置。
func (m *MemoryStore) GetPlatform(ctx context.Context, id string) (*storage.Platform, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, ok := m.platforms[id]
	if !ok {
		return nil, fmt.Errorf("platform not found: %s", id)
	}
	cp := *p
	return &cp, nil
}

// ListPlatforms 按角色查询平台配置列表。
// role 为空时返回全部平台（"downstream"=下级平台，"upstream"=上级平台）。
func (m *MemoryStore) ListPlatforms(ctx context.Context, role string) ([]*storage.Platform, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	items := make([]*storage.Platform, 0, len(m.platforms))
	for _, p := range m.platforms {
		if role != "" && p.Role != role {
			continue
		}
		cp := *p
		items = append(items, &cp)
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].CreatedAt.Before(items[j].CreatedAt)
	})
	return items, nil
}

// DeletePlatform 按主键删除平台配置。
func (m *MemoryStore) DeletePlatform(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.platforms[id]; !ok {
		return fmt.Errorf("platform not found: %s", id)
	}
	delete(m.platforms, id)
	return nil
}
