<template>
  <div class="app-layout" :class="{ 'sidebar-collapsed': appStore.sidebarCollapsed, 'mobile-sidebar-open': appStore.mobileSidebarOpen, 'no-sidebar': isPublicRoute }">
    <div class="free-banner" v-if="!isPublicRoute && authStore.isFree">
      <el-icon :size="14"><InfoFilled /></el-icon>
      <span>免费版 — 仅支持20台设备接入，如需更多请升级专业版</span>
      <a href="https://jte.dev/pricing" target="blank" class="banner-link">了解详情 →</a>
    </div>
    <!-- FIXED: [响应式] 移动端遮罩层 [2026-07-17] -->
    <div v-if="!isPublicRoute && appStore.mobileSidebarOpen" class="sidebar-overlay" @click="appStore.closeMobileSidebar()"></div>
    <aside v-if="!isPublicRoute" class="sidebar" :class="{ collapsed: appStore.sidebarCollapsed }">
      <div class="sidebar-header">
        <Logo :icon-size="32" :show-sub="!appStore.sidebarCollapsed" subtitle="Dashboard" />
        <!-- FIXED: [响应式] 移动端关闭按钮 [2026-07-17] -->
        <el-icon class="sidebar-close-btn" @click="appStore.closeMobileSidebar()"><Close /></el-icon>
      </div>
      <nav class="sidebar-nav">
        <!-- ==================== 监控中心（免费） ==================== -->
        <div class="nav-group-label">监控中心</div>
        <router-link v-if="isMenuVisible('overview')" to="/" class="nav-item" active-class="active">
          <el-icon><Monitor /></el-icon><span>概览</span>
        </router-link>
        <router-link v-if="isMenuVisible('map')" to="/map" class="nav-item" active-class="active">
          <el-icon><Location /></el-icon><span>实时地图</span>
        </router-link>
        <router-link v-if="isMenuVisible('sessions')" to="/sessions" class="nav-item" active-class="active">
          <el-icon><Connection /></el-icon><span>会话管理</span>
        </router-link>
        <router-link v-if="isMenuVisible('vehicles')" to="/vehicles" class="nav-item" active-class="active">
          <el-icon><Van /></el-icon><span>车辆管理</span>
        </router-link>
        <router-link v-if="isMenuVisible('drivers')" to="/drivers" class="nav-item" active-class="active">
          <el-icon><Avatar /></el-icon><span>驾驶员管理</span>
        </router-link>
        <router-link v-if="isMenuVisible('geofences')" to="/geofences" class="nav-item" active-class="active">
          <el-icon><Aim /></el-icon><span>电子围栏</span>
        </router-link>
        <router-link v-if="isMenuVisible('alarms')" to="/alarms" class="nav-item" active-class="active">
          <el-icon><Warning /></el-icon><span>报警中心</span>
        </router-link>
        <router-link v-if="isMenuVisible('logs')" to="/logs" class="nav-item" active-class="active">
          <el-icon><Document /></el-icon><span>协议日志</span>
        </router-link>
        <!-- FIXED-2026-07-24: 设备管理/轨迹回放/视频监控/指令下发 是 JT/T 808 核心功能，免费 -->
        <router-link v-if="isMenuVisible('devices')" to="/devices" class="nav-item" active-class="active">
          <el-icon><Cpu /></el-icon><span>设备管理</span>
        </router-link>
        <router-link v-if="isMenuVisible('tracks')" to="/tracks" class="nav-item" active-class="active">
          <el-icon><MapLocation /></el-icon><span>轨迹回放</span>
        </router-link>
        <router-link v-if="isMenuVisible('video')" to="/video" class="nav-item" active-class="active">
          <el-icon><VideoCamera /></el-icon><span>视频监控</span>
        </router-link>
        <router-link v-if="isMenuVisible('commands')" to="/commands" class="nav-item" active-class="active">
          <el-icon><Promotion /></el-icon><span>指令下发</span>
        </router-link>

        <!-- ==================== JT/T 809 级联平台（授权） ==================== -->
        <div class="nav-group-label licensed-group">JT/T 809 级联平台</div>
        <router-link
          v-if="isMenuVisible('cascade')"
          to="/cascade"
          class="nav-item"
          active-class="active"
          :class="{ disabled: !hasModule('protocol_809') }"
          :title="!hasModule('protocol_809') ? '模块未授权' : ''"
          @click="handleModuleClick($event, !hasModule('protocol_809'), 'protocol_809')"
        >
          <el-icon><Share /></el-icon><span>级联平台</span>
          <span v-if="trialDays('protocol_809')" class="trial-badge">{{ trialDays('protocol_809') }}天</span>
          <span v-if="!hasModule('protocol_809')" class="unauth-tag">未授权</span>
          <span v-if="!hasModule('protocol_809')" class="lock-icon"><Lock /></span>
        </router-link>

        <!-- ==================== JT/T 905 北斗出租（授权） ==================== -->
        <div class="nav-group-label licensed-group">JT/T 905 北斗出租</div>
        <router-link
          v-if="isMenuVisible('taxi')"
          to="/taxi"
          class="nav-item"
          active-class="active"
          :class="{ disabled: !hasModule('protocol_905') }"
          :title="!hasModule('protocol_905') ? '模块未授权' : ''"
          @click="handleModuleClick($event, !hasModule('protocol_905'), 'protocol_905')"
        >
          <el-icon><Position /></el-icon><span>出租车监控</span>
          <span v-if="trialDays('protocol_905')" class="trial-badge">{{ trialDays('protocol_905') }}天</span>
          <span v-if="!hasModule('protocol_905')" class="unauth-tag">未授权</span>
          <span v-if="!hasModule('protocol_905')" class="lock-icon"><Lock /></span>
        </router-link>

        <!-- ==================== 数据存储与报表（授权） ==================== -->
        <div class="nav-group-label licensed-group">数据存储与报表</div>
        <router-link
          v-if="isMenuVisible('reports')"
          to="/reports"
          class="nav-item"
          active-class="active"
          :class="{ disabled: !hasModule('storage') && !hasModule('db_storage') }"
          :title="!hasModule('storage') && !hasModule('db_storage') ? '模块未授权' : ''"
          @click="handleModuleClick($event, !hasModule('storage') && !hasModule('db_storage'), 'storage')"
        >
          <el-icon><DataAnalysis /></el-icon><span>报表中心</span>
          <span v-if="trialDays('storage')" class="trial-badge">{{ trialDays('storage') }}天</span>
          <span v-if="!hasModule('storage') && !hasModule('db_storage')" class="unauth-tag">未授权</span>
          <span v-if="!hasModule('storage') && !hasModule('db_storage')" class="lock-icon"><Lock /></span>
        </router-link>
        <router-link
          v-if="isMenuVisible('archive')"
          to="/archive-query"
          class="nav-item"
          active-class="active"
          :class="{ disabled: !hasModule('storage') && !hasModule('db_storage') }"
          :title="!hasModule('storage') && !hasModule('db_storage') ? '模块未授权' : ''"
          @click="handleModuleClick($event, !hasModule('storage') && !hasModule('db_storage'), 'storage')"
        >
          <el-icon><Files /></el-icon><span>归档查询</span>
          <span v-if="trialDays('storage')" class="trial-badge">{{ trialDays('storage') }}天</span>
          <span v-if="!hasModule('storage') && !hasModule('db_storage')" class="unauth-tag">未授权</span>
          <span v-if="!hasModule('storage') && !hasModule('db_storage')" class="lock-icon"><Lock /></span>
        </router-link>

        <!-- ==================== AI 智能分析（授权） ==================== -->
        <div class="nav-group-label licensed-group">AI 智能分析</div>
        <router-link
          v-if="isMenuVisible('ai')"
          to="/ai"
          class="nav-item"
          active-class="active"
          :class="{ disabled: !hasModule('ai') }"
          :title="!hasModule('ai') ? '模块未授权' : ''"
          @click="handleModuleClick($event, !hasModule('ai'), 'ai')"
        >
          <el-icon><MagicStick /></el-icon><span>AI分析</span>
          <span v-if="trialDays('ai')" class="trial-badge">{{ trialDays('ai') }}天</span>
          <span v-if="!hasModule('ai')" class="unauth-tag">未授权</span>
          <span v-if="!hasModule('ai')" class="lock-icon"><Lock /></span>
        </router-link>
        <router-link
          v-if="isMenuVisible('alarm_filter')"
          to="/ai/alarm-filter"
          class="nav-item"
          active-class="active"
          :class="{ disabled: !hasModule('ai') }"
          :title="!hasModule('ai') ? '模块未授权' : ''"
          @click="handleModuleClick($event, !hasModule('ai'), 'ai')"
        >
          <el-icon><Filter /></el-icon><span>报警过滤</span>
          <span v-if="!hasModule('ai')" class="unauth-tag">未授权</span>
          <span v-if="!hasModule('ai')" class="lock-icon"><Lock /></span>
        </router-link>
        <router-link
          v-if="isMenuVisible('driver_fatigue')"
          to="/ai/driver-fatigue"
          class="nav-item"
          active-class="active"
          :class="{ disabled: !hasModule('ai') }"
          :title="!hasModule('ai') ? '模块未授权' : ''"
          @click="handleModuleClick($event, !hasModule('ai'), 'ai')"
        >
          <el-icon><User /></el-icon><span>疲劳驾驶</span>
          <span v-if="!hasModule('ai')" class="unauth-tag">未授权</span>
          <span v-if="!hasModule('ai')" class="lock-icon"><Lock /></span>
        </router-link>
        <router-link
          v-if="isMenuVisible('risk_scoring')"
          to="/ai/risk-scoring"
          class="nav-item"
          active-class="active"
          :class="{ disabled: !hasModule('ai') }"
          :title="!hasModule('ai') ? '模块未授权' : ''"
          @click="handleModuleClick($event, !hasModule('ai'), 'ai')"
        >
          <el-icon><TrendCharts /></el-icon><span>风险评分</span>
          <span v-if="!hasModule('ai')" class="unauth-tag">未授权</span>
          <span v-if="!hasModule('ai')" class="lock-icon"><Lock /></span>
        </router-link>

        <!-- ==================== AI 对话助手（授权） ==================== -->
        <div class="nav-group-label licensed-group">AI 对话助手</div>
        <router-link
          v-if="isMenuVisible('nlp')"
          to="/ai/nlp"
          class="nav-item"
          active-class="active"
          :class="{ disabled: !hasModule('ai_nlp') }"
          :title="!hasModule('ai_nlp') ? '模块未授权' : ''"
          @click="handleModuleClick($event, !hasModule('ai_nlp'), 'ai_nlp')"
        >
          <el-icon><ChatDotRound /></el-icon><span>智能助手</span>
          <span v-if="trialDays('ai_nlp')" class="trial-badge">{{ trialDays('ai_nlp') }}天</span>
          <span v-if="!hasModule('ai_nlp')" class="unauth-tag">未授权</span>
          <span v-if="!hasModule('ai_nlp')" class="lock-icon"><Lock /></span>
        </router-link>

        <!-- ==================== 系统管理（免费） ==================== -->
        <div class="nav-group-label">系统管理</div>
        <router-link v-if="isMenuVisible('system') && canManageSystem" to="/system" class="nav-item" active-class="active">
          <el-icon><Setting /></el-icon><span>系统设置</span>
        </router-link>
        <router-link v-if="isMenuVisible('modules') && canManageModule" to="/system/modules" class="nav-item" active-class="active">
          <el-icon><Grid /></el-icon><span>模块管理</span>
        </router-link>
        <router-link v-if="isMenuVisible('users') && canManageUser" to="/system/users" class="nav-item" active-class="active">
          <el-icon><User /></el-icon><span>用户管理</span>
        </router-link>
        <router-link v-if="isMenuVisible('config') && canManageSystem" to="/system/config" class="nav-item" active-class="active">
          <el-icon><Tools /></el-icon><span>系统参数</span>
        </router-link>
        <!-- FIXED-2026-07-24: 配置管理拆分为独立菜单 -->
        <router-link v-if="isMenuVisible('map_config') && canManageSystem" to="/system/map-config" class="nav-item" active-class="active">
          <el-icon><Location /></el-icon><span>地图配置</span>
        </router-link>
        <router-link v-if="isMenuVisible('menu_config') && canManageSystem" to="/system/menu-config" class="nav-item" active-class="active">
          <el-icon><Menu /></el-icon><span>菜单配置</span>
        </router-link>
        <router-link v-if="isMenuVisible('ai_config') && canManageSystem" to="/system/ai-config" class="nav-item" active-class="active">
          <el-icon><MagicStick /></el-icon><span>AI 配置</span>
        </router-link>
        <router-link v-if="isMenuVisible('storage') && canManageSystem" to="/system/storage" class="nav-item" active-class="active">
          <el-icon><Coin /></el-icon><span>存储管理</span>
        </router-link>
        <router-link v-if="isMenuVisible('auth') && canManageLicense" to="/system/auth" class="nav-item" active-class="active">
          <el-icon><Key /></el-icon><span>授权管理</span>
        </router-link>
        <router-link v-if="isMenuVisible('roles') && canManageUser" to="/system/roles" class="nav-item" active-class="active">
          <el-icon><UserFilled /></el-icon><span>角色权限</span>
        </router-link>
        <router-link v-if="isMenuVisible('logs_system') && canAuditLog" to="/system/logs" class="nav-item" active-class="active">
          <el-icon><Document /></el-icon><span>审计日志</span>
        </router-link>
      </nav>
      <div class="sidebar-footer">
        <div style="display: flex; align-items: center; justify-content: space-between; margin-bottom: 8px;">
          <div class="system-status">
            <div class="live-indicator"></div>
            <span>{{ $t('system.running') }}</span>
          </div>
          <div style="display: flex; gap: 8px;">
            <!-- FIXED: [UI/UX] 主题切换按钮 [2026-07-17] -->
            <el-icon class="theme-toggle-btn" @click="appStore.toggleTheme()" :title="appStore.isDark ? '切换亮色' : '切换暗色'">
              <Sunny v-if="appStore.isDark" />
              <Moon v-else />
            </el-icon>
            <el-select v-model="currentLocale" size="small" style="width: 90px;" @change="changeLocale">
              <el-option label="中文" value="zh-CN" />
              <el-option label="EN" value="en-US" />
            </el-select>
          </div>
        </div>
      </div>
    </aside>
    <main class="main-content" :class="{ 'no-sidebar': isPublicRoute }">
      <!-- FIXED: [响应式] 移动端顶部栏含菜单按钮 [2026-07-17] -->
      <div v-if="!isPublicRoute" class="mobile-topbar">
        <el-icon @click="appStore.toggleMobileSidebar()" :size="20"><Fold /></el-icon>
        <span class="mobile-title">JTE Dashboard</span>
      </div>
      <router-view />
    </main>

    <!-- AUTO-FIX-2026-06-30 [P1-7]: 模块购买解锁弹窗 -->
    <ModulePurchaseModal
      v-if="!isPublicRoute"
      v-model="purchaseModalVisible"
      :module-name="purchaseModuleName"
      :trial-loading="purchaseTrialLoading"
      @start-trial="handleStartTrial"
    />
  </div>
</template>

<script setup>
import { useAuthStore } from './stores/auth'
import { usePermissionStore } from './stores/permission'
import { useAppStore } from './stores/app'
import { useI18n } from 'vue-i18n'
import { ref, onMounted, onUnmounted, computed, watch } from 'vue'
import { useRoute } from 'vue-router'
import { UserFilled, Close, Fold, Sunny, Moon } from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'
import Logo from './components/Logo.vue'
import ModulePurchaseModal from './components/ModulePurchaseModal.vue'

const authStore = useAuthStore()
const permStore = usePermissionStore()
const appStore = useAppStore()
const route = useRoute()
const { locale } = useI18n()
const currentLocale = ref(locale.value)

// 公开路由（登录页等）不显示侧边栏
const isPublicRoute = computed(() => !!route.meta.public)

// 菜单显示配置（从 localStorage 读取，按用户 ID 隔离）
const visibleMenus = ref({})
const MENU_LS_KEY = computed(() => {
  const userId = permStore.currentUser?.id || 'default'
  return `jte_visible_menus_${userId}`
})

// 默认所有菜单都显示
const ALL_MENU_KEYS = [
  'overview', 'map', 'sessions', 'vehicles', 'drivers', 'geofences', 'alarms', 'logs',
  'taxi', 'devices', 'tracks', 'archive', 'video', 'commands',
  'reports', 'cascade', 'ai', 'alarm_filter', 'driver_fatigue', 'risk_scoring', 'nlp',
  'system', 'modules', 'users', 'config', 'map_config', 'menu_config', 'ai_config', 'storage', 'auth', 'roles', 'logs_system'
]

// 初始化菜单显示配置
function initVisibleMenus() {
  const saved = localStorage.getItem(MENU_LS_KEY.value)
  if (saved) {
    try {
      visibleMenus.value = JSON.parse(saved)
    } catch {
      visibleMenus.value = ALL_MENU_KEYS.reduce((acc, key) => ({ ...acc, [key]: true }), {})
    }
  } else {
    visibleMenus.value = ALL_MENU_KEYS.reduce((acc, key) => ({ ...acc, [key]: true }), {})
  }
}

// 切换菜单显示状态
function toggleMenuVisibility(key) {
  visibleMenus.value[key] = !visibleMenus.value[key]
  localStorage.setItem(MENU_LS_KEY.value, JSON.stringify(visibleMenus.value))
}

// 检查菜单是否可见
function isMenuVisible(key) {
  return visibleMenus.value[key] !== false
}

// 监听菜单配置变化，触发重新渲染
watch(
  () => ({ ...visibleMenus.value }),
  (newVal) => {
    // 保存到 localStorage（当 Config.vue 更新时）
    if (Object.keys(newVal).length > 0) {
      const userId = permStore.currentUser?.id || 'default'
      localStorage.setItem(`jte_visible_menus_${userId}`, JSON.stringify(newVal))
    }
  },
  { deep: true }
)

// 监听登录状态变化，切换用户时重新加载菜单配置
watch(
  () => permStore.currentUser?.id,
  (newUserId) => {
    if (newUserId) {
      initVisibleMenus()
    }
  }
)

// 暴露给全局，方便 Config.vue 调用
window.jteMenuConfig = {
  visibleMenus,
  toggleMenuVisibility,
  ALL_MENU_KEYS,
  MENU_LABELS: {
    overview: '概览',
    map: '实时地图',
    sessions: '会话管理',
    vehicles: '车辆管理',
    drivers: '驾驶员管理',
    geofences: '电子围栏',
    alarms: '报警中心',
    logs: '协议日志',
    taxi: '出租车监控',
    devices: '设备管理',
    tracks: '轨迹回放',
    archive: '归档查询',
    video: '视频监控',
    commands: '指令下发',
    reports: '报表中心',
    cascade: '级联平台',
    ai: 'AI分析',
    alarm_filter: '报警过滤',
    driver_fatigue: '疲劳驾驶',
    risk_scoring: '风险评分',
    nlp: '智能助手',
    system: '系统设置',
    modules: '模块管理',
    users: '用户管理',
    config: '系统参数',
    map_config: '地图配置',
    menu_config: '菜单配置',
    ai_config: 'AI 配置',
    storage: '存储管理',
    auth: '授权管理',
    roles: '角色权限',
    logs_system: '审计日志'
  }
}

onMounted(() => {
  initVisibleMenus()
})

// AUTO-FIX-2026-06-30 [P1-7]: 模块购买解锁弹窗
const purchaseModalVisible = ref(false)
const purchaseModuleName = ref('')
const purchaseTrialLoading = ref(false)

// AUTO-FIX-2026-07-02: 统一处理模块菜单点击
// - 已授权：router-link 自动处理路由跳转（不阻止默认行为）
// - 未授权：阻止跳转，弹出授权引导弹窗
function handleModuleClick(event, isUnauthorized, primaryModule) {
  if (isUnauthorized) {
    // 阻止 router-link 的默认导航
    event.preventDefault()
    openPurchase(primaryModule)
  }
  // 已授权：不阻止默认行为，router-link 正常跳转
}

// 每个锁定菜单项的主模块（OR 条件下取主模块）
function openPurchase(moduleName) {
  purchaseModuleName.value = moduleName
  purchaseModalVisible.value = true
}

async function handleStartTrial(moduleName) {
  purchaseTrialLoading.value = true
  try {
    await authStore.startTrial(moduleName)
    ElMessage.success('试用已开启')
    purchaseModalVisible.value = false
  } catch (e) {
    ElMessage.error(e.message || '试用开启失败')
  } finally {
    purchaseTrialLoading.value = false
  }
}

// 809 试用倒计时标签：试用中返回剩余天数，否则返回 0
function trialDays(moduleName) {
  if (authStore.getModuleStatus(moduleName) !== 'trial') return 0
  return authStore.getTrialRemainingDays(moduleName)
}

// AUTO-FIX-2026-06-26: 第六轮前端修复 - 模块检查兼容多种命名（809/protocol_809、storage/db_storage）
function hasModule(moduleName) {
  const active = authStore.activeModules || []
  if (active.includes(moduleName)) return true
  // 兼容别名
  const aliases = {
    protocol_809: ['809', 'protocol_809'],
    protocol_1045: ['1045', 'protocol_1045'],
    protocol_1078: ['1078', 'protocol_1078'],
    protocol_905: ['905', 'protocol_905'],
    storage: ['storage', 'db_storage'],
    ai: ['ai'],
    ai_nlp: ['ai_nlp'],
  }
  const aliasList = aliases[moduleName] || [moduleName]
  return aliasList.some(a => active.includes(a))
}

const canManageSystem = computed(() => permStore.hasPermission('system'))
const canManageModule = computed(() => permStore.hasPermission('module'))
const canManageUser = computed(() => permStore.hasPermission('user_manage') || permStore.hasPermission('role_manage'))
const canManageLicense = computed(() => permStore.hasPermission('license'))
const canAuditLog = computed(() => permStore.hasPermission('audit_log'))

function changeLocale(val) {
  locale.value = val
  localStorage.setItem('jte-locale', val)
}

// AUTO-FIX-2026-07-02: License 状态联动 - 登录后自动刷新 authStore.modules
// 监听 permStore.isLoggedIn 变化，登录成功后立即拉取最新模块状态
watch(
  () => permStore.isLoggedIn,
  async (loggedIn, wasLoggedIn) => {
    if (loggedIn && !wasLoggedIn) {
      // 登录成功：自动刷新模块授权状态（无需手动刷新浏览器）
      await authStore.fetchStatus()
    } else if (!loggedIn && wasLoggedIn) {
      // 登出：重置 authStore
      authStore.$reset()
    }
  }
)

// AUTO-FIX-2026-06-26: 授权变更后菜单自动更新 [2026-06-26]
// AUTO-FIX-2026-07-02: 同时订阅 permission_changed 事件，权限变更时自动刷新
// FIXED: [P0] WebSocket 连接携带 JWT token + 指数退避重连 + 手动关闭标志 [2026-07-17]
let licenseWS = null
let licenseWSReconnect = null
let licenseWSReconnectCount = 0
let licenseWSManuallyClosed = false
let licenseWSHeartbeatTimer = null // FIXED: [WebSocket心跳] 定时发送心跳保活 [2026-07-17]

function connectLicenseWatcher() {
  if (licenseWSManuallyClosed) return
  const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  const token = localStorage.getItem('jte_token') || ''
  // FIXED: [P0] WebSocket 连接必须携带 JWT token，否则后端返回 401 [2026-07-17]
  const wsUrl = `${protocol}//${window.location.host}/ws/v1/stream?token=${encodeURIComponent(token)}`
  try {
    licenseWS = new WebSocket(wsUrl)
  } catch (e) {
    scheduleLicenseReconnect()
    return
  }

  licenseWS.onopen = () => {
    licenseWSReconnectCount = 0
    if (licenseWS.readyState === WebSocket.OPEN) {
      // 订阅 system_event（模块/授权变更）+ permission_changed（权限变更）
      licenseWS.send(JSON.stringify({
        action: 'subscribe',
        topics: ['system_event', 'permission_changed'],
      }))
      // FIXED: [WebSocket心跳] 每60秒发送心跳保活 [2026-07-17]
      licenseWSHeartbeatTimer = setInterval(() => {
        if (licenseWS && licenseWS.readyState === WebSocket.OPEN) {
          licenseWS.send(JSON.stringify({ action: 'ping' }))
        }
      }, 60000)
    }
  }

  licenseWS.onmessage = (event) => {
    try {
      const msg = JSON.parse(event.data)
      // 授权移除/到期/即将到期 → 刷新模块菜单
      if (msg.topic === 'system_event' && msg.data) {
        const evt = msg.data
        if (evt.type === 'module_unloaded' || evt.type === 'license_expired' ||
            evt.type === 'license_expiring_soon' || evt.type === 'module_loaded' ||
            evt.type === 'license_activated') {
          // 模块状态变更：自动刷新菜单显示（无需手动刷新浏览器）
          authStore.fetchStatus()
        }
      }
      // AUTO-FIX-2026-07-02: 权限变更推送 - 后端修改用户角色/权限后实时刷新
      if (msg.topic === 'permission_changed' && msg.data) {
        const evt = msg.data
        // 仅处理当前用户的权限变更（避免其他用户变更触发不必要的刷新）
        if (!evt.user_id || (permStore.currentUser && evt.user_id === permStore.currentUser.id)) {
          permStore.onPermissionChanged()
          ElMessage.info('您的权限已更新，菜单已自动刷新')
        }
      }
    } catch (e) { /* ignore */ }
  }

  licenseWS.onclose = () => {
    if (licenseWSManuallyClosed) return
    if (licenseWSHeartbeatTimer) { clearInterval(licenseWSHeartbeatTimer); licenseWSHeartbeatTimer = null }
    scheduleLicenseReconnect()
  }
  licenseWS.onerror = () => { if (licenseWS) licenseWS.close() }
}

function scheduleLicenseReconnect() {
  if (licenseWSReconnect) return
  // FIXED: [WebSocket断连] 指数退避重连（1s→2s→4s→...→30s） [2026-07-17]
  const delay = Math.min(1000 * Math.pow(2, licenseWSReconnectCount), 30000)
  licenseWSReconnectCount++
  licenseWSReconnect = setTimeout(() => {
    licenseWSReconnect = null
    connectLicenseWatcher()
  }, delay)
}

// AUTO-FIX-2026-07-02: 多标签页同步 - 其他标签页激活授权码后，本标签页也刷新菜单
if (typeof window !== 'undefined') {
  window.addEventListener('storage', (e) => {
    // 其他标签页可能更新了 token（激活授权码后 token 可能变化）
    if (e.key === 'jte_token' && e.newValue && e.newValue !== permStore.token) {
      permStore.token = e.newValue
      authStore.fetchStatus()
    }
  })
}

onMounted(() => {
  if (!authStore.loaded) {
    authStore.fetchStatus()
  }
  if (!permStore.isLoggedIn) {
    permStore.restoreSession()
  }
  connectLicenseWatcher()
})

onUnmounted(() => {
  licenseWSManuallyClosed = true // FIXED: 防止组件卸载后重连 [2026-07-17]
  if (licenseWSReconnect) clearTimeout(licenseWSReconnect)
  if (licenseWSHeartbeatTimer) clearInterval(licenseWSHeartbeatTimer)
  if (licenseWS) { licenseWS.onclose = null; licenseWS.close() }
})
</script>

<style scoped>
.app-layout { display: flex; min-height: 100vh; }
.sidebar { width: 220px; background: var(--jte-surface); border-right: 1px solid var(--jte-border); display: flex; flex-direction: column; position: fixed; top: 0; left: 0; bottom: 0; z-index: 10; overflow-y: auto; transition: width 0.2s ease, transform 0.3s ease; }
.sidebar.collapsed { width: 64px; }
.sidebar.collapsed .nav-item span,
.sidebar.collapsed .nav-group-label,
.sidebar.collapsed .sidebar-footer { display: none; }
.sidebar.collapsed .nav-item { justify-content: center; }
.sidebar-close-btn { display: none; cursor: pointer; color: var(--jte-text-muted); }
.theme-toggle-btn { cursor: pointer; color: var(--jte-text-muted); font-size: 16px; }
.theme-toggle-btn:hover { color: var(--jte-text); }
.sidebar-overlay { display: none; }
.mobile-topbar { display: none; }
.sidebar-header { padding: 20px; border-bottom: 1px solid var(--jte-border); display: flex; align-items: center; justify-content: space-between; }
.sidebar-nav { flex: 1; padding: 12px 8px; display: flex; flex-direction: column; gap: 2px; }
.nav-group-label { font-size: 11px; color: var(--jte-text-muted); padding: 16px 12px 6px; text-transform: uppercase; letter-spacing: 0.05em; font-weight: 500; }
/* FIXED-2026-07-24: 授权模块分组样式——加锁图标+琥珀色，与免费分组视觉区分 */
.nav-group-label.licensed-group { color: var(--jte-warning, #f59e0b); }
.nav-group-label.licensed-group::after { content: ' 🔒'; font-size: 10px; opacity: 0.7; }
.nav-item { display: flex; align-items: center; gap: 10px; padding: 10px 12px; border-radius: 8px; color: var(--jte-text-muted); text-decoration: none; font-size: 14px; font-weight: 450; transition: all 0.15s ease; cursor: pointer; }
.nav-item:hover { color: var(--jte-text); background: var(--jte-surface-2); }
.nav-item.active { color: #fff; background: var(--jte-accent); }
.nav-item .el-icon { font-size: 18px; }
/* AUTO-FIX-2026-07-02: 未授权菜单灰显但仍可点击（点击弹出授权引导弹窗） */
.nav-item.disabled { opacity: 0.5; color: var(--jte-text-muted); }
.nav-item.disabled:hover { color: var(--jte-warning, #f59e0b); }
.lock-icon { font-size: 12px; margin-left: auto; opacity: 0.5; }
/* AUTO-FIX-2026-07-02: "未授权" 标签 */
.unauth-tag { font-size: 10px; color: #fff; background: var(--jte-text-muted, #909399); padding: 1px 6px; border-radius: 8px; margin-left: auto; font-weight: 500; }
.nav-item .unauth-tag + .lock-icon { margin-left: 4px; }
/* AUTO-FIX-2026-06-30 [P1-7]: 试用中倒计时内联标签 */
.trial-badge { font-size: 10px; color: #fff; background: var(--jte-warning, #f59e0b); padding: 1px 6px; border-radius: 8px; margin-left: auto; font-weight: 500; }
.nav-item .trial-badge + .unauth-tag { margin-left: 4px; }
.nav-item .trial-badge + .lock-icon { margin-left: 6px; }
.nav-item .unauth-tag + .lock-icon { margin-left: 4px; }
.sidebar-footer { padding: 16px 20px; border-top: 1px solid var(--jte-border); }
.system-status { display: flex; align-items: center; gap: 8px; font-size: 12px; color: var(--jte-text-muted); }
.live-indicator { width: 8px; height: 8px; border-radius: 50%; background: #67c23a; animation: pulse 2s infinite; }
@keyframes pulse { 0%, 100% { opacity: 1; } 50% { opacity: 0.5; } }
.free-banner { position: fixed; top: 0; left: 220px; right: 0; height: 32px; background: linear-gradient(90deg, rgba(99, 102, 241, 0.15), rgba(139, 92, 246, 0.15)); border-bottom: 1px solid var(--jte-border); display: flex; align-items: center; justify-content: center; gap: 8px; font-size: 12px; color: var(--jte-accent); z-index: 9; }
.banner-link { color: var(--jte-accent); text-decoration: none; font-weight: 500; margin-left: 4px; }
.banner-link:hover { text-decoration: underline; }
.main-content { flex: 1; margin-left: 220px; margin-top: 32px; min-height: calc(100vh - 32px); background: var(--jte-bg); }
.main-content.no-sidebar { margin-left: 0; margin-top: 0; min-height: 100vh; }
.app-layout.no-sidebar { display: block; }
.app-layout.sidebar-collapsed .sidebar { width: 64px; }
.app-layout.sidebar-collapsed .main-content { margin-left: 64px; }
.app-layout.sidebar-collapsed .free-banner { left: 64px; }

/* FIXED: [响应式] 移动端适配 [2026-07-17] */
@media (max-width: 768px) {
  .sidebar { transform: translateX(-100%); width: 240px; }
  .sidebar-close-btn { display: block; }
  .sidebar-overlay { display: block; position: fixed; top: 0; left: 0; right: 0; bottom: 0; background: rgba(0,0,0,0.5); z-index: 9; }
  .mobile-sidebar-open .sidebar { transform: translateX(0); }
  .main-content { margin-left: 0; margin-top: 0; }
  .free-banner { left: 0; }
  .mobile-topbar { display: flex; align-items: center; gap: 12px; padding: 12px 16px; background: var(--jte-surface); border-bottom: 1px solid var(--jte-border); position: sticky; top: 0; z-index: 8; }
  .mobile-title { font-size: 16px; font-weight: 600; }
  .page-header { padding: 16px; }
  .page-content { padding: 16px; }
}
</style>
