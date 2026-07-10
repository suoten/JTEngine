<template>
  <div class="app-layout">
    <div class="free-banner" v-if="authStore.isFree">
      <el-icon :size="14"><InfoFilled /></el-icon>
      <span>免费版 — 仅支持20台设备接入，如需更多请升级专业版</span>
      <a href="https://jte.dev/pricing" target="_blank" class="banner-link">了解详情 →</a>
    </div>
    <aside class="sidebar">
      <div class="sidebar-header">
        <Logo :icon-size="32" :show-sub="true" subtitle="Dashboard" />
      </div>
      <nav class="sidebar-nav">
        <div class="nav-group-label">监控中心</div>
        <router-link to="/" class="nav-item" active-class="active">
          <el-icon><Monitor /></el-icon><span>概览</span>
        </router-link>
        <router-link to="/map" class="nav-item" active-class="active">
          <el-icon><Location /></el-icon><span>实时地图</span>
        </router-link>
        <router-link to="/sessions" class="nav-item" active-class="active">
          <el-icon><Connection /></el-icon><span>会话管理</span>
        </router-link>
        <router-link to="/vehicles" class="nav-item" active-class="active">
          <el-icon><Van /></el-icon><span>车辆管理</span>
        </router-link>
        <router-link to="/drivers" class="nav-item" active-class="active">
          <el-icon><Avatar /></el-icon><span>驾驶员管理</span>
        </router-link>
        <router-link to="/geofences" class="nav-item" active-class="active">
          <el-icon><Aim /></el-icon><span>电子围栏</span>
        </router-link>
        <router-link to="/alarms" class="nav-item" active-class="active">
          <el-icon><Warning /></el-icon><span>报警中心</span>
        </router-link>
        <router-link to="/logs" class="nav-item" active-class="active">
          <el-icon><Document /></el-icon><span>协议日志</span>
        </router-link>
        <!-- AUTO-FIX-2026-07-02: JT/T 905 出租车专用菜单（905 模块授权后可见） -->
        <router-link
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
          <span v-if="!hasModule('protocol_905')" class="lock-icon">🔒</span>
        </router-link>

        <div class="nav-group-label">设备管理</div>
        <router-link
          to="/devices"
          class="nav-item"
          active-class="active"
          :class="{ disabled: !hasModule('protocol_809') && !hasModule('protocol_1045') }"
          :title="!hasModule('protocol_809') && !hasModule('protocol_1045') ? '模块未授权' : ''"
          @click="handleModuleClick($event, !hasModule('protocol_809') && !hasModule('protocol_1045'), 'protocol_809')"
        >
          <el-icon><Cpu /></el-icon><span>设备管理</span>
          <span v-if="trialDays('protocol_809') || trialDays('protocol_1045')" class="trial-badge">{{ trialDays('protocol_809') || trialDays('protocol_1045') }}天</span>
          <span v-if="!hasModule('protocol_809') && !hasModule('protocol_1045')" class="unauth-tag">未授权</span>
          <span v-if="!hasModule('protocol_809') && !hasModule('protocol_1045')" class="lock-icon">🔒</span>
        </router-link>
        <router-link
          to="/tracks"
          class="nav-item"
          active-class="active"
          :class="{ disabled: !hasModule('protocol_809') }"
          :title="!hasModule('protocol_809') ? '模块未授权' : ''"
          @click="handleModuleClick($event, !hasModule('protocol_809'), 'protocol_809')"
        >
          <el-icon><MapLocation /></el-icon><span>轨迹回放</span>
          <span v-if="trialDays('protocol_809')" class="trial-badge">{{ trialDays('protocol_809') }}天</span>
          <span v-if="!hasModule('protocol_809')" class="unauth-tag">未授权</span>
          <span v-if="!hasModule('protocol_809')" class="lock-icon">🔒</span>
        </router-link>
        <!-- AUTO-FIX-2026-07-02: 归档数据查询菜单（存储模块授权后可见） -->
        <router-link
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
          <span v-if="!hasModule('storage') && !hasModule('db_storage')" class="lock-icon">🔒</span>
        </router-link>
        <router-link
          to="/video"
          class="nav-item"
          active-class="active"
          :class="{ disabled: !hasModule('protocol_1078') && !hasModule('protocol_905') }"
          :title="!hasModule('protocol_1078') && !hasModule('protocol_905') ? '模块未授权' : ''"
          @click="handleModuleClick($event, !hasModule('protocol_1078') && !hasModule('protocol_905'), 'protocol_1078')"
        >
          <el-icon><VideoCamera /></el-icon><span>视频监控</span>
          <span v-if="trialDays('protocol_1078')" class="trial-badge">{{ trialDays('protocol_1078') }}天</span>
          <span v-if="!hasModule('protocol_1078') && !hasModule('protocol_905')" class="unauth-tag">未授权</span>
          <span v-if="!hasModule('protocol_1078') && !hasModule('protocol_905')" class="lock-icon">🔒</span>
        </router-link>
        <router-link
          to="/commands"
          class="nav-item"
          active-class="active"
          :class="{ disabled: !hasModule('protocol_809') }"
          :title="!hasModule('protocol_809') ? '模块未授权' : ''"
          @click="handleModuleClick($event, !hasModule('protocol_809'), 'protocol_809')"
        >
          <el-icon><Promotion /></el-icon><span>指令下发</span>
          <span v-if="trialDays('protocol_809')" class="trial-badge">{{ trialDays('protocol_809') }}天</span>
          <span v-if="!hasModule('protocol_809')" class="unauth-tag">未授权</span>
          <span v-if="!hasModule('protocol_809')" class="lock-icon">🔒</span>
        </router-link>

        <div class="nav-group-label">数据分析</div>
        <router-link
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
          <span v-if="!hasModule('storage') && !hasModule('db_storage')" class="lock-icon">🔒</span>
        </router-link>
        <router-link
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
          <span v-if="!hasModule('protocol_809')" class="lock-icon">🔒</span>
        </router-link>

        <div class="nav-group-label">AI智能</div>
        <router-link
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
          <span v-if="!hasModule('ai')" class="lock-icon">🔒</span>
        </router-link>
        <router-link
          to="/ai/alarm-filter"
          class="nav-item"
          active-class="active"
          :class="{ disabled: !hasModule('ai') }"
          :title="!hasModule('ai') ? '模块未授权' : ''"
          @click="handleModuleClick($event, !hasModule('ai'), 'ai')"
        >
          <el-icon><Filter /></el-icon><span>报警过滤</span>
          <span v-if="!hasModule('ai')" class="unauth-tag">未授权</span>
          <span v-if="!hasModule('ai')" class="lock-icon">🔒</span>
        </router-link>
        <router-link
          to="/ai/driver-fatigue"
          class="nav-item"
          active-class="active"
          :class="{ disabled: !hasModule('ai') }"
          :title="!hasModule('ai') ? '模块未授权' : ''"
          @click="handleModuleClick($event, !hasModule('ai'), 'ai')"
        >
          <el-icon><User /></el-icon><span>疲劳驾驶</span>
          <span v-if="!hasModule('ai')" class="unauth-tag">未授权</span>
          <span v-if="!hasModule('ai')" class="lock-icon">🔒</span>
        </router-link>
        <router-link
          to="/ai/risk-scoring"
          class="nav-item"
          active-class="active"
          :class="{ disabled: !hasModule('ai') }"
          :title="!hasModule('ai') ? '模块未授权' : ''"
          @click="handleModuleClick($event, !hasModule('ai'), 'ai')"
        >
          <el-icon><TrendCharts /></el-icon><span>风险评分</span>
          <span v-if="!hasModule('ai')" class="unauth-tag">未授权</span>
          <span v-if="!hasModule('ai')" class="lock-icon">🔒</span>
        </router-link>
        <router-link
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
          <span v-if="!hasModule('ai_nlp')" class="lock-icon">🔒</span>
        </router-link>

        <!-- AUTO-FIX-2026-06-26: 第六轮前端修复 - 系统管理菜单增加权限控制 -->
        <div class="nav-group-label">系统管理</div>
        <router-link v-if="canManageSystem" to="/system" class="nav-item" active-class="active">
          <el-icon><Setting /></el-icon><span>系统设置</span>
        </router-link>
        <router-link v-if="canManageModule" to="/system/modules" class="nav-item" active-class="active">
          <el-icon><Grid /></el-icon><span>模块管理</span>
        </router-link>
        <router-link v-if="canManageUser" to="/system/users" class="nav-item" active-class="active">
          <el-icon><User /></el-icon><span>用户管理</span>
        </router-link>
        <router-link v-if="canManageSystem" to="/system/config" class="nav-item" active-class="active">
          <el-icon><Tools /></el-icon><span>配置管理</span>
        </router-link>
        <router-link v-if="canManageSystem" to="/system/storage" class="nav-item" active-class="active">
          <el-icon><Coin /></el-icon><span>存储管理</span>
        </router-link>
        <router-link v-if="canManageLicense" to="/system/auth" class="nav-item" active-class="active">
          <el-icon><Key /></el-icon><span>授权管理</span>
        </router-link>
        <router-link v-if="canManageUser" to="/system/roles" class="nav-item" active-class="active">
          <el-icon><UserFilled /></el-icon><span>角色权限</span>
        </router-link>
        <router-link v-if="canAuditLog" to="/system/logs" class="nav-item" active-class="active">
          <el-icon><Document /></el-icon><span>审计日志</span>
        </router-link>
      </nav>
      <div class="sidebar-footer">
        <div style="display: flex; align-items: center; justify-content: space-between; margin-bottom: 8px;">
          <div class="system-status">
            <div class="live-indicator"></div>
            <span>{{ $t('system.running') }}</span>
          </div>
          <el-select v-model="currentLocale" size="small" style="width: 90px;" @change="changeLocale">
            <el-option label="中文" value="zh-CN" />
            <el-option label="EN" value="en-US" />
          </el-select>
        </div>
      </div>
    </aside>
    <main class="main-content">
      <router-view />
    </main>

    <!-- AUTO-FIX-2026-06-30 [P1-7]: 模块购买解锁弹窗 -->
    <ModulePurchaseModal
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
import { useI18n } from 'vue-i18n'
import { ref, onMounted, onUnmounted, computed, watch } from 'vue'
import { UserFilled } from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'
import Logo from './components/Logo.vue'
import ModulePurchaseModal from './components/ModulePurchaseModal.vue'

const authStore = useAuthStore()
const permStore = usePermissionStore()
const { locale } = useI18n()
const currentLocale = ref(locale.value)

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
let licenseWS = null
let licenseWSReconnect = null

function connectLicenseWatcher() {
  const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  const wsUrl = `${protocol}//${window.location.host}/ws/v1/stream`
  try {
    licenseWS = new WebSocket(wsUrl)
  } catch (e) {
    scheduleLicenseReconnect()
    return
  }

  licenseWS.onopen = () => {
    if (licenseWS.readyState === WebSocket.OPEN) {
      // 订阅 system_event（模块/授权变更）+ permission_changed（权限变更）
      licenseWS.send(JSON.stringify({
        action: 'subscribe',
        topics: ['system_event', 'permission_changed'],
      }))
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

  licenseWS.onclose = () => { scheduleLicenseReconnect() }
  licenseWS.onerror = () => { if (licenseWS) licenseWS.close() }
}

function scheduleLicenseReconnect() {
  if (licenseWSReconnect) return
  licenseWSReconnect = setTimeout(() => {
    licenseWSReconnect = null
    connectLicenseWatcher()
  }, 5000)
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
  if (licenseWSReconnect) clearTimeout(licenseWSReconnect)
  if (licenseWS) { licenseWS.onclose = null; licenseWS.close() }
})
</script>

<style scoped>
.app-layout { display: flex; min-height: 100vh; }
.sidebar { width: 220px; background: var(--jte-surface); border-right: 1px solid var(--jte-border); display: flex; flex-direction: column; position: fixed; top: 0; left: 0; bottom: 0; z-index: 10; overflow-y: auto; }
.sidebar-header { padding: 20px; border-bottom: 1px solid var(--jte-border); display: flex; align-items: center; }
.sidebar-nav { flex: 1; padding: 12px 8px; display: flex; flex-direction: column; gap: 2px; }
.nav-group-label { font-size: 11px; color: var(--jte-text-muted); padding: 16px 12px 6px; text-transform: uppercase; letter-spacing: 0.05em; font-weight: 500; }
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
</style>
