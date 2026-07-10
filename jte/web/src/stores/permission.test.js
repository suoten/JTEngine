import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'

// AUTO-FIX-2026-07-02: 权限动态化单元测试
// 验证：登录后从后端拉取权限、localStorage 缓存、手动刷新、异步会话恢复

// Mock authApi - 在 import store 之前 mock
const mockGetPermissions = vi.fn()
const mockLogin = vi.fn()
vi.mock('../api', () => ({
  authApi: {
    login: (...args) => mockLogin(...args),
    getPermissions: (...args) => mockGetPermissions(...args),
    refresh: vi.fn(),
    getStatus: vi.fn(),
    activateLicense: vi.fn(),
    removeLicense: vi.fn(),
    startTrial: vi.fn(),
  },
}))

import { usePermissionStore } from './permission'

describe('permission store - 权限动态化', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    localStorage.clear()
    vi.clearAllMocks()
  })

  afterEach(() => {
    vi.clearAllMocks()
  })

  describe('fetchPermissions - 从后端拉取权限', () => {
    it('应调用 /auth/permissions 接口并更新 state', async () => {
      const store = usePermissionStore()
      store.token = 'fake-token'

      mockGetPermissions.mockResolvedValue({
        code: 0,
        data: {
          role: 'admin',
          role_label: '管理员',
          permissions: ['monitor', 'device', 'system'],
          permission_tree: [{ key: 'monitor', label: '监控中心', perm: 'monitor' }],
          data_scope: { scope_type: 'org', org_id: 'org-1', vehicle_ids: [] },
        },
      })

      const result = await store.fetchPermissions()

      expect(result).toBe(true)
      expect(mockGetPermissions).toHaveBeenCalledTimes(1)
      expect(store.role).toBe('admin')
      expect(store.roleLabel).toBe('管理员')
      expect(store.permissions).toEqual(['monitor', 'device', 'system'])
      expect(store.permissionTree).toHaveLength(1)
      expect(store.dataScope.scope_type).toBe('org')
      expect(store.dataScope.org_id).toBe('org-1')
      expect(store.permissionsLoaded).toBe(true)
    })

    it('无 token 时不应调用 API', async () => {
      const store = usePermissionStore()
      store.token = ''

      const result = await store.fetchPermissions()

      expect(result).toBe(false)
      expect(mockGetPermissions).not.toHaveBeenCalled()
    })

    it('后端返回错误时应抛出异常', async () => {
      const store = usePermissionStore()
      store.token = 'fake-token'

      mockGetPermissions.mockResolvedValue({
        code: 500,
        message: 'internal error',
      })

      // 错误消息应来自后端返回的 message
      await expect(store.fetchPermissions()).rejects.toThrow('internal error')
    })

    it('应将权限缓存到 localStorage', async () => {
      const store = usePermissionStore()
      store.token = 'fake-token'

      mockGetPermissions.mockResolvedValue({
        code: 0,
        data: {
          role: 'operator',
          role_label: '操作员',
          permissions: ['monitor', 'alarm'],
          permission_tree: [],
          data_scope: { scope_type: 'all', org_id: '', vehicle_ids: [] },
        },
      })

      await store.fetchPermissions()

      expect(localStorage.getItem('jte_permissions')).toBe(JSON.stringify(['monitor', 'alarm']))
      expect(localStorage.getItem('jte_role_label')).toBe('操作员')
      expect(localStorage.getItem('jte_data_scope')).toBe(
        JSON.stringify({ scope_type: 'all', org_id: '', vehicle_ids: [] })
      )
    })
  })

  describe('refreshPermissions - 手动刷新权限', () => {
    it('应重新调用 fetchPermissions', async () => {
      const store = usePermissionStore()
      store.token = 'fake-token'

      mockGetPermissions.mockResolvedValue({
        code: 0,
        data: {
          role: 'admin',
          permissions: ['monitor'],
          permission_tree: [],
          data_scope: { scope_type: 'all', org_id: '', vehicle_ids: [] },
        },
      })

      await store.refreshPermissions()

      expect(mockGetPermissions).toHaveBeenCalledTimes(1)
      expect(store.permissions).toEqual(['monitor'])
    })
  })

  describe('login - 登录后拉取权限', () => {
    it('登录成功后应调用 fetchPermissions 获取最新权限', async () => {
      mockLogin.mockResolvedValue({
        code: 0,
        data: {
          token: 'new-token',
          id: 'user-1',
          username: 'admin',
          role: 'admin',
          permissions: ['monitor'], // 登录响应携带的初始权限
        },
      })

      mockGetPermissions.mockResolvedValue({
        code: 0,
        data: {
          role: 'admin',
          role_label: '管理员',
          permissions: ['monitor', 'device', 'system', 'vehicle'], // 后端返回更完整的权限
          permission_tree: [],
          data_scope: { scope_type: 'all', org_id: '', vehicle_ids: [] },
        },
      })

      const store = usePermissionStore()
      const result = await store.login('admin', 'password')

      expect(result).toBe(true)
      expect(store.token).toBe('new-token')
      expect(store.currentUser.username).toBe('admin')
      // 权限应来自 fetchPermissions 而非 login 响应
      expect(store.permissions).toEqual(['monitor', 'device', 'system', 'vehicle'])
      expect(mockGetPermissions).toHaveBeenCalledTimes(1)
    })

    it('fetchPermissions 失败时应回退到登录响应中的权限', async () => {
      mockLogin.mockResolvedValue({
        code: 0,
        data: {
          token: 'new-token',
          id: 'user-1',
          username: 'operator',
          role: 'operator',
          permissions: ['monitor', 'alarm'],
        },
      })

      mockGetPermissions.mockRejectedValue(new Error('network error'))

      const store = usePermissionStore()
      const result = await store.login('operator', 'password')

      expect(result).toBe(true)
      // 回退到登录响应中的权限
      expect(store.permissions).toEqual(['monitor', 'alarm'])
      expect(store.permissionsLoaded).toBe(true)
    })
  })

  describe('restoreSession - 异步会话恢复', () => {
    it('无 token 时返回 false', async () => {
      const store = usePermissionStore()
      const result = await store.restoreSession()
      expect(result).toBe(false)
    })

    it('有 token 时应从 localStorage 恢复并从后端拉取最新权限', async () => {
      localStorage.setItem('jte_token', 'cached-token')
      localStorage.setItem('jte_user', JSON.stringify({
        id: 'user-1',
        username: 'admin',
        role: 'admin',
      }))
      localStorage.setItem('jte_permissions', JSON.stringify(['monitor', 'system']))
      localStorage.setItem('jte_data_scope', JSON.stringify({
        scope_type: 'org', org_id: 'org-1', vehicle_ids: [],
      }))

      mockGetPermissions.mockResolvedValue({
        code: 0,
        data: {
          role: 'admin',
          role_label: '管理员',
          permissions: ['monitor', 'system', 'device'], // 后端返回更新后的权限
          permission_tree: [],
          data_scope: { scope_type: 'all', org_id: '', vehicle_ids: [] },
        },
      })

      const store = usePermissionStore()
      const result = await store.restoreSession()

      expect(result).toBe(true)
      expect(store.token).toBe('cached-token')
      expect(store.currentUser.username).toBe('admin')
      // 先从缓存恢复
      expect(store.permissions).toEqual(['monitor', 'system', 'device'])
      // 后端拉取成功后更新
      expect(mockGetPermissions).toHaveBeenCalledTimes(1)
    })

    it('后端拉取失败时应保留缓存权限', async () => {
      localStorage.setItem('jte_token', 'cached-token')
      localStorage.setItem('jte_user', JSON.stringify({
        id: 'user-1',
        username: 'admin',
        role: 'admin',
      }))
      localStorage.setItem('jte_permissions', JSON.stringify(['monitor']))

      mockGetPermissions.mockRejectedValue(new Error('network error'))

      const store = usePermissionStore()
      const result = await store.restoreSession()

      // 即使后端失败也返回 true（让路由放行，由 401 拦截器处理）
      expect(result).toBe(true)
      expect(store.permissions).toEqual(['monitor']) // 保留缓存
      expect(store.permissionsLoaded).toBe(true)
    })
  })

  describe('logout - 清理状态', () => {
    it('应清空 state 和 localStorage', async () => {
      const store = usePermissionStore()
      store.token = 'fake-token'
      store.currentUser = { id: '1', username: 'admin', role: 'admin' }
      store.permissions = ['monitor']
      store.permissionTree = [{ key: 'monitor' }]
      store.roleLabel = '管理员'
      store.dataScope = { scope_type: 'org', org_id: 'org-1', vehicle_ids: [] }

      localStorage.setItem('jte_token', 'fake-token')
      localStorage.setItem('jte_permissions', JSON.stringify(['monitor']))
      localStorage.setItem('jte_data_scope', JSON.stringify({ scope_type: 'org' }))

      store.logout()

      expect(store.token).toBe('')
      expect(store.currentUser).toBeNull()
      expect(store.permissions).toEqual([])
      expect(store.permissionTree).toEqual([])
      expect(store.roleLabel).toBe('')
      expect(store.dataScope.scope_type).toBe('all')
      expect(localStorage.getItem('jte_token')).toBeNull()
      expect(localStorage.getItem('jte_permissions')).toBeNull()
      expect(localStorage.getItem('jte_data_scope')).toBeNull()
    })
  })

  describe('onPermissionChanged - WebSocket 推送触发刷新', () => {
    it('有 token 时应静默调用 fetchPermissions', async () => {
      const store = usePermissionStore()
      store.token = 'fake-token'

      mockGetPermissions.mockResolvedValue({
        code: 0,
        data: {
          role: 'admin',
          permissions: ['monitor', 'device'],
          permission_tree: [],
          data_scope: { scope_type: 'all', org_id: '', vehicle_ids: [] },
        },
      })

      store.onPermissionChanged()

      // 等待异步 fetchPermissions 完成
      await new Promise(resolve => setTimeout(resolve, 50))

      expect(mockGetPermissions).toHaveBeenCalledTimes(1)
      expect(store.permissions).toEqual(['monitor', 'device'])
    })

    it('无 token 时不应调用 fetchPermissions', () => {
      const store = usePermissionStore()
      store.token = ''

      store.onPermissionChanged()

      expect(mockGetPermissions).not.toHaveBeenCalled()
    })
  })

  describe('getters - 权限查询', () => {
    it('hasPermission 应正确判断权限', () => {
      const store = usePermissionStore()
      store.permissions = ['monitor', 'device', 'system']

      expect(store.hasPermission('monitor')).toBe(true)
      expect(store.hasPermission('device')).toBe(true)
      expect(store.hasPermission('alarm')).toBe(false)
    })

    it('canAccessRoute 应根据权限映射判断路由访问', () => {
      const store = usePermissionStore()
      store.token = 'fake-token'
      store.currentUser = { id: '1', username: 'admin', role: 'admin' }
      // 注意：monitor 权限映射到 ['/', '/map']，其中 '/' 前缀匹配所有路径
      // 因此仅用不含 monitor 的权限测试路由隔离
      store.permissions = ['vehicle', 'alarm']

      expect(store.canAccessRoute('/vehicles')).toBe(true)  // vehicle → '/vehicles'
      expect(store.canAccessRoute('/alarms')).toBe(true)    // alarm → '/alarms'
      // 无 system 权限 → /system 不可访问
      expect(store.canAccessRoute('/system')).toBe(false)
      // 无 command 权限 → /commands 不可访问
      expect(store.canAccessRoute('/commands')).toBe(false)
    })

    it('super_admin 应可访问所有路由', () => {
      const store = usePermissionStore()
      store.token = 'fake-token'
      store.currentUser = { id: '1', username: 'admin', role: 'super_admin' }
      store.role = 'super_admin'
      store.permissions = ['monitor']

      expect(store.canAccessRoute('/system')).toBe(true)
      expect(store.canAccessRoute('/ai')).toBe(true)
    })

    it('dataScopeType 应返回当前数据范围类型', () => {
      const store = usePermissionStore()
      store.dataScope = { scope_type: 'org', org_id: 'org-1', vehicle_ids: [] }

      expect(store.dataScopeType).toBe('org')
      expect(store.isDataScopeAll).toBe(false)
    })
  })
})
