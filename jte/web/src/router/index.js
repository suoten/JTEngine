import { createRouter, createWebHistory } from 'vue-router'
import { usePermissionStore } from '../stores/permission'
import { useAuthStore } from '../stores/auth'

const routes = [
  {
    path: '/login',
    name: 'Login',
    component: () => import('../views/Login.vue'),
    meta: { public: true },
  },
  {
    path: '/',
    name: 'Overview',
    component: () => import('../views/Overview.vue'),
    meta: { permission: 'monitor' },
  },
  {
    path: '/map',
    name: 'Map',
    component: () => import('../views/MapView.vue'),
    meta: { permission: 'monitor' },
  },
  {
    path: '/sessions',
    name: 'Sessions',
    component: () => import('../views/Sessions.vue'),
    meta: { permission: 'monitor' },
  },
  {
    path: '/vehicles',
    name: 'Vehicles',
    component: () => import('../views/Vehicles.vue'),
    meta: { permission: 'vehicle' },
  },
  {
    path: '/drivers',
    name: 'Drivers',
    component: () => import('../views/Drivers.vue'),
    meta: { permission: 'vehicle' },
  },
  {
    path: '/geofences',
    name: 'Geofences',
    component: () => import('../views/Geofence.vue'),
    meta: { permission: 'vehicle' },
  },
  {
    path: '/alarms',
    name: 'Alarms',
    component: () => import('../views/Alarms.vue'),
    meta: { permission: 'alarm' },
  },
  {
    path: '/logs',
    name: 'ProtocolLogs',
    component: () => import('../views/ProtocolLogs.vue'),
    meta: { permission: 'monitor' },
  },
  {
    path: '/devices',
    name: 'Devices',
    component: () => import('../views/Devices.vue'),
    // FIXED-2026-07-24: 设备管理是 JT/T 808 核心功能，开源版免费，无需模块授权
    meta: { permission: 'device' },
  },
  {
    path: '/tracks',
    name: 'Tracks',
    component: () => import('../views/Tracks.vue'),
    // FIXED-2026-07-24: 轨迹回放是 JT/T 808 核心功能，开源版免费，无需模块授权
    meta: { permission: 'track' },
  },
  {
    // AUTO-FIX-2026-07-02: 归档数据查询（联合归档+实时数据，归档任务监控）
    path: '/archive-query',
    name: 'ArchiveQuery',
    component: () => import('../views/ArchiveQuery.vue'),
    meta: { permission: 'track', requireModule: ['storage'] },
  },
  {
    // AUTO-FIX-2026-07-02: JT/T 905 出租车专用界面
    path: '/taxi',
    name: 'Taxi',
    component: () => import('../views/Taxi.vue'),
    meta: { permission: 'monitor', requireModule: ['protocol_905'] },
  },
  {
    // FIXED: 视频监控（JT/T 1078）为免费自带功能，无需模块授权
    path: '/video',
    name: 'Video',
    component: () => import('../views/Video.vue'),
    meta: { permission: 'video' },
  },
  {
    path: '/commands',
    name: 'Commands',
    component: () => import('../views/Commands.vue'),
    // FIXED-2026-07-24: 指令下发是 JT/T 808 核心功能，开源版免费，无需模块授权
    meta: { permission: 'command' },
  },
  {
    path: '/reports',
    name: 'Reports',
    component: () => import('../views/Reports.vue'),
    meta: { permission: 'report', requireModule: ['storage'] },
  },
  {
    path: '/cascade',
    name: 'Cascade',
    component: () => import('../views/Cascade.vue'),
    meta: { permission: 'cascade', requireModule: ['protocol_809'] },
  },
  {
    path: '/system',
    name: 'System',
    component: () => import('../views/system/Index.vue'),
    meta: { permission: 'system' },
  },
  {
    path: '/system/modules',
    name: 'SystemModules',
    component: () => import('../views/system/Modules.vue'),
    meta: { permission: 'module' },
  },
  {
    path: '/system/users',
    name: 'SystemUsers',
    component: () => import('../views/system/Users.vue'),
    meta: { permission: 'user_manage' },
  },
  {
    // FIXED-2026-07-24: 配置管理拆分为独立页面
    path: '/system/config',
    name: 'SystemConfig',
    component: () => import('../views/system/Config.vue'),
    meta: { permission: 'system' },
  },
  {
    path: '/system/map-config',
    name: 'MapConfig',
    component: () => import('../views/system/MapConfig.vue'),
    meta: { permission: 'system' },
  },
  {
    path: '/system/menu-config',
    name: 'MenuConfig',
    component: () => import('../views/system/MenuConfig.vue'),
    meta: { permission: 'system' },
  },
  {
    path: '/system/ai-config',
    name: 'AIConfig',
    component: () => import('../views/system/AIConfig.vue'),
    meta: { permission: 'system' },
  },
  {
    path: '/system/auth',
    name: 'SystemAuth',
    component: () => import('../views/system/Auth.vue'),
    meta: { permission: 'license' },
  },
  {
    path: '/system/roles',
    name: 'SystemRoles',
    component: () => import('../views/system/Roles.vue'),
    meta: { permission: 'role_manage' },
  },
  {
    path: '/system/logs',
    name: 'SystemLogs',
    component: () => import('../views/system/Logs.vue'),
    meta: { permission: 'audit_log' },
  },
  {
    path: '/system/storage',
    name: 'SystemStorage',
    component: () => import('../views/system/Storage.vue'),
    meta: { permission: 'system' },
  },
  {
    path: '/ai',
    name: 'Ai',
    component: () => import('../views/ai/Index.vue'),
    meta: { permission: 'ai', requireModule: ['ai'] },
  },
  {
    path: '/ai/alarm-filter',
    name: 'AiAlarmFilter',
    component: () => import('../views/ai/AlarmFilter.vue'),
    meta: { permission: 'ai', requireModule: ['ai'] },
  },
  {
    path: '/ai/driver-fatigue',
    name: 'AiDriverFatigue',
    component: () => import('../views/ai/DriverFatigue.vue'),
    meta: { permission: 'ai', requireModule: ['ai'] },
  },
  {
    path: '/ai/risk-scoring',
    name: 'AiRiskScoring',
    component: () => import('../views/ai/RiskScoring.vue'),
    meta: { permission: 'ai', requireModule: ['ai'] },
  },
  {
    path: '/ai/nlp',
    name: 'AiNlp',
    component: () => import('../views/ai/Nlp.vue'),
    meta: { permission: 'ai', requireModule: ['ai_nlp'] },
  },
  {
    path: '/:pathMatch(.*)*',
    name: 'NotFound',
    component: () => import('../views/NotFound.vue'),
    meta: { public: true },
  },
]

const router = createRouter({
  history: createWebHistory(),
  routes,
})

// 模块命名变体：后端可能返回 "809" 或 "protocol_809"、"storage" 或 "db_storage"，
// 这里统一兼容，避免因命名不一致导致有授权的用户被误拦截。
const MODULE_ALIASES = {
  protocol_809: ['protocol_809', '809'],
  protocol_1045: ['protocol_1045', '1045'],
  protocol_1078: ['protocol_1078', '1078'],
  protocol_905: ['protocol_905', '905'],
  storage: ['storage', 'db_storage'],
  ai: ['ai'],
  ai_nlp: ['ai_nlp'],
}

function hasAnyModule(authStore, requiredModules) {
  if (!requiredModules || requiredModules.length === 0) return true
  const active = authStore.activeModules || []
  if (active.length === 0) return false
  return requiredModules.some((m) => {
    const aliases = MODULE_ALIASES[m] || [m]
    return aliases.some((alias) => active.includes(alias))
  })
}

router.beforeEach(async (to, from, next) => {
  if (to.meta.public) {
    next()
    return
  }

  const permStore = usePermissionStore()
  const authStore = useAuthStore()

  // AUTO-FIX-2026-07-02: restoreSession 改为异步（从后端拉取最新权限 + 校验 token）
  if (!permStore.isLoggedIn) {
    const ok = await permStore.restoreSession()
    if (!ok) {
      next('/login')
      return
    }
  }

  // 确保授权模块数据已加载，避免刷新后 activeModules 为空导致 requireModule 误判
  if (!authStore.loaded) {
    await authStore.fetchStatus()
  }

  // 校验模块授权：路由配置了 requireModule 时，必须拥有其中任一模块才可访问
  if (to.meta.requireModule && to.meta.requireModule.length > 0) {
    if (!hasAnyModule(authStore, to.meta.requireModule)) {
      next('/')
      return
    }
  }

  if (to.meta.permission && !permStore.hasPermission(to.meta.permission)) {
    next('/')
    return
  }

  next()
})

export default router
