import { defineStore } from 'pinia'
import { authApi } from '../api'

// AUTO-FIX-2026-07-02: 权限动态化改造
// 原实现将 ROLE_PERMISSIONS 硬编码在前端，新增角色或调整权限都需要改前端代码。
// 现改为登录后从后端 /auth/permissions 接口动态拉取权限列表 + 数据范围，
// 并缓存到 localStorage 支持离线降级；权限变更时通过 WebSocket 推送更新。
//
// ROLE_PERMISSIONS 仅保留作为「后端不可达 / 离线模式」时的兜底降级，
// 不再作为权限的权威来源。

// 离线兜底：仅在后端 /auth/permissions 不可达时使用
const ROLE_PERMISSIONS = {
  super_admin: [
    'monitor', 'device', 'vehicle', 'alarm', 'track', 'video',
    'command', 'report', 'cascade', 'user_manage', 'role_manage',
    'system', 'module', 'license', 'audit_log', 'ai',
  ],
  admin: [
    'monitor', 'device', 'vehicle', 'alarm', 'track', 'video',
    'command', 'report', 'cascade', 'user_manage', 'role_manage',
    'system', 'audit_log', 'ai',
  ],
  operator: [
    'monitor', 'device', 'vehicle', 'alarm', 'track', 'video',
    'command', 'report', 'ai',
  ],
  readonly: [
    'monitor', 'alarm', 'track', 'video', 'report', 'ai',
  ],
}

const ROLE_LABELS = {
  super_admin: '超级管理员',
  admin: '管理员',
  operator: '操作员',
  readonly: '只读用户',
}

// AUTO-FIX-2026-06-26: 第六轮前端修复 - 修复权限路由映射漏洞
// user_manage 不应包含 /system/roles（需要 role_manage）
// system 不应包含 /system/logs（需要 audit_log）
const PERM_ROUTE_MAP = {
  monitor: ['/', '/map'],
  device: ['/devices'],
  vehicle: ['/vehicles'],
  alarm: ['/alarms'],
  track: ['/tracks'],
  video: ['/video'],
  command: ['/commands'],
  report: ['/reports'],
  cascade: ['/cascade'],
  user_manage: ['/system/users'],
  role_manage: ['/system/roles'],
  system: ['/system', '/system/config'],
  module: ['/system/modules'],
  license: ['/system/auth'],
  audit_log: ['/system/logs'],
  ai: ['/ai', '/ai/alarm-filter', '/ai/driver-fatigue', '/ai/risk-scoring', '/ai/nlp'],
}

// localStorage 缓存键
const LS_PERMISSIONS = 'jte_permissions'
const LS_PERM_TREE = 'jte_permission_tree'
const LS_ROLE_LABEL = 'jte_role_label'
const LS_DATA_SCOPE = 'jte_data_scope'

export const usePermissionStore = defineStore('permission', {
  state: () => ({
    currentUser: null,
    role: '',
    roleLabel: '',
    permissions: [],
    permissionTree: [],
    // AUTO-FIX-2026-07-02: 数据权限范围（all/org/vehicle/self）
    // 由 /auth/permissions 返回，列表查询 API 自动附加过滤条件
    dataScope: { scope_type: 'all', org_id: '', vehicle_ids: [] },
    token: '',
    // AUTO-FIX-2026-07-02 [等保2.0]: CSRF token（登录响应下发，写请求需携带 X-CSRF-Token 头）
    csrfToken: '',
    // 是否已从后端加载过权限（区分「未登录」与「正在加载」）
    permissionsLoaded: false,
  }),

  getters: {
    isLoggedIn: (state) => !!state.currentUser && !!state.token,
    isSuperAdmin: (state) => state.role === 'super_admin',
    isAdmin: (state) => state.role === 'super_admin' || state.role === 'admin',
    // roleLabel 优先取后端返回值，否则查内置映射表
    roleLabelGetter: (state) => state.roleLabel || ROLE_LABELS[state.role] || state.role,
    hasPermission: (state) => (perm) => state.permissions.includes(perm),
    canMonitor: (state) => state.permissions.includes('monitor'),
    canManageDevice: (state) => state.permissions.includes('device'),
    canManageVehicle: (state) => state.permissions.includes('vehicle'),
    canSendCommand: (state) => state.permissions.includes('command'),
    canManageCascade: (state) => state.permissions.includes('cascade'),
    canManageUser: (state) => state.permissions.includes('user_manage'),
    canManageSystem: (state) => state.permissions.includes('system'),
    canManageModule: (state) => state.permissions.includes('module'),
    canManageLicense: (state) => state.permissions.includes('license'),
    canAuditLog: (state) => state.permissions.includes('audit_log'),
    allowedRoutes: (state) => {
      const routes = new Set()
      for (const perm of state.permissions) {
        const mapped = PERM_ROUTE_MAP[perm]
        if (mapped) {
          mapped.forEach(r => routes.add(r))
        }
      }
      return routes
    },
    canAccessRoute: (state) => (path) => {
      if (!state.isLoggedIn) return false
      if (state.role === 'super_admin') return true
      for (const perm of state.permissions) {
        const mapped = PERM_ROUTE_MAP[perm]
        if (mapped && mapped.some(r => path.startsWith(r))) return true
      }
      return false
    },
    // 数据权限 getter
    dataScopeType: (state) => state.dataScope?.scope_type || 'all',
    isDataScopeAll: (state) => (state.dataScope?.scope_type || 'all') === 'all',
  },

  actions: {
    async login(username, password, deviceFingerprint = '') {
      const res = await authApi.login({
        username,
        password,
        // AUTO-FIX-2026-07-02 [防克隆]: 上报设备指纹，后端用于绑定校验 + 异常登录检测
        device_fingerprint: deviceFingerprint,
      })
      if (res.code === 0 && res.data) {
        this.token = res.data.token
        // AUTO-FIX-2026-07-02 [等保2.0]: 保存 CSRF token（HttpOnly cookie + 响应体双下发）
        this.csrfToken = res.data.csrf_token || ''
        this.currentUser = {
          id: res.data.id,
          username: res.data.username,
          role: res.data.role,
        }
        this.role = res.data.role || 'readonly'
        // 登录响应可能携带 permissions，但权威来源是 /auth/permissions
        this.permissions = res.data.permissions || ROLE_PERMISSIONS[this.role] || ROLE_PERMISSIONS.readonly
        localStorage.setItem('jte_token', this.token)
        localStorage.setItem('jte_user', JSON.stringify(this.currentUser))

        // AUTO-FIX-2026-07-02: 登录后从后端拉取最新权限（含自定义角色 + 数据权限）
        // 不阻塞登录流程：失败时回退到登录响应中的 permissions 或 ROLE_PERMISSIONS
        try {
          await this.fetchPermissions()
        } catch (e) {
          console.warn('[permission] fetchPermissions after login failed, using fallback:', e.message)
          this.permissionsLoaded = true
        }
        return true
      }
      // AUTO-FIX-2026-07-02 [防克隆]: 透传 HTTP 状态码，便于 Login.vue 区分 401/429
      const err = new Error(res.message || 'Login failed')
      err.status = res.status || (res.code === 401 ? 401 : (res.code === 429 ? 429 : 0))
      throw err
    },

    logout() {
      this.currentUser = null
      this.role = ''
      this.roleLabel = ''
      this.permissions = []
      this.permissionTree = []
      this.dataScope = { scope_type: 'all', org_id: '', vehicle_ids: [] }
      this.token = ''
      this.csrfToken = ''
      this.permissionsLoaded = false
      localStorage.removeItem('jte_token')
      localStorage.removeItem('jte_user')
      localStorage.removeItem(LS_PERMISSIONS)
      localStorage.removeItem(LS_PERM_TREE)
      localStorage.removeItem(LS_ROLE_LABEL)
      localStorage.removeItem(LS_DATA_SCOPE)
    },

    // AUTO-FIX-2026-07-02: 从后端拉取权限树 + 数据范围（权威来源）
    // 登录后、权限变更时、手动刷新时调用。
    // 失败时不清理已有权限（避免后端临时故障导致用户失权）。
    async fetchPermissions() {
      if (!this.token) return false
      try {
        const res = await authApi.getPermissions()
        if (res.code === 0 && res.data) {
          const data = res.data
          if (data.role) this.role = data.role
          if (data.role_label) this.roleLabel = data.role_label
          this.permissions = Array.isArray(data.permissions) ? data.permissions : []
          this.permissionTree = Array.isArray(data.permission_tree) ? data.permission_tree : []
          if (data.data_scope) {
            this.dataScope = {
              scope_type: data.data_scope.scope_type || 'all',
              org_id: data.data_scope.org_id || '',
              vehicle_ids: Array.isArray(data.data_scope.vehicle_ids) ? data.data_scope.vehicle_ids : [],
            }
          }
          this.permissionsLoaded = true
          this._cacheToLocalStorage()
          return true
        }
        throw new Error(res.message || 'fetch permissions failed')
      } catch (e) {
        console.error('Failed to fetch permissions:', e)
        throw e
      }
    },

    // 手动刷新权限（供"刷新权限"按钮调用）
    async refreshPermissions() {
      try {
        return await this.fetchPermissions()
      } catch (e) {
        console.error('Failed to refresh permissions:', e)
        throw e
      }
    },

    // 缓存权限到 localStorage（支持页面刷新后快速恢复 + 离线降级）
    _cacheToLocalStorage() {
      try {
        localStorage.setItem(LS_PERMISSIONS, JSON.stringify(this.permissions))
        localStorage.setItem(LS_PERM_TREE, JSON.stringify(this.permissionTree))
        if (this.roleLabel) localStorage.setItem(LS_ROLE_LABEL, this.roleLabel)
        localStorage.setItem(LS_DATA_SCOPE, JSON.stringify(this.dataScope))
      } catch (e) { /* ignore quota errors */ }
    },

    // 从 localStorage 恢复缓存的权限（页面刷新时使用，先快速渲染，再异步从后端校验）
    _restoreFromCache() {
      try {
        const permsStr = localStorage.getItem(LS_PERMISSIONS)
        const treeStr = localStorage.getItem(LS_PERM_TREE)
        const labelStr = localStorage.getItem(LS_ROLE_LABEL)
        const scopeStr = localStorage.getItem(LS_DATA_SCOPE)
        if (permsStr) this.permissions = JSON.parse(permsStr)
        if (treeStr) this.permissionTree = JSON.parse(treeStr)
        if (labelStr) this.roleLabel = labelStr
        if (scopeStr) this.dataScope = JSON.parse(scopeStr)
      } catch (e) { /* ignore */ }
    },

    // AUTO-FIX-2026-07-02: 异步恢复会话 - 先从 localStorage 快速恢复，
    // 再异步从后端校验 token + 拉取最新权限（支持权限实时变更）
    // 返回 true 表示有可恢复的会话（即使后端校验失败也返回 true，让路由放行后由 401 拦截器处理）
    async restoreSession() {
      const token = localStorage.getItem('jte_token')
      const userStr = localStorage.getItem('jte_user')
      if (!token || !userStr) return false

      try {
        const user = JSON.parse(userStr)
        this.token = token
        this.currentUser = user
        this.role = user.role || 'readonly'

        // 先从缓存恢复权限（快速渲染菜单）
        this._restoreFromCache()
        if (this.permissions.length === 0) {
          // 无缓存时用兜底表（避免白屏）
          this.permissions = ROLE_PERMISSIONS[this.role] || ROLE_PERMISSIONS.readonly
        }

        // 异步从后端校验 token + 拉取最新权限（不阻塞路由放行）
        // 失败时保留缓存权限（让用户继续操作，由 401 拦截器在请求时处理 token 失效）
        try {
          await this.fetchPermissions()
        } catch (e) {
          console.warn('[permission] restoreSession: backend fetch failed, using cached permissions:', e.message)
          this.permissionsLoaded = true
        }
        return true
      } catch {
        this.logout()
        return false
      }
    },

    // AUTO-FIX-2026-07-02: WebSocket 推送权限变更时调用
    // 后端修改用户角色/权限后，通过 WebSocket 推送 permission_changed 事件，
    // 前端收到后调用此方法重新拉取权限
    onPermissionChanged() {
      if (this.token) {
        // 静默刷新：不抛出错误，失败时保留现有权限
        this.fetchPermissions().catch(() => {})
      }
    },
  },
})

export { ROLE_PERMISSIONS, ROLE_LABELS, PERM_ROUTE_MAP }
