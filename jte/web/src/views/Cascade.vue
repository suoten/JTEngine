<template>
  <div class="cascade-page">
    <div class="page-header">
      <h2>级联平台管理</h2>
      <p class="header-desc">下级平台接入管理、转发规则配置、视频协商状态监控</p>
    </div>

    <el-tabs v-model="activeTab" class="cascade-tabs">
      <!-- ======================== 平台列表 ======================== -->
      <el-tab-pane label="平台列表" name="platforms">
        <div class="tab-toolbar">
          <el-button type="primary" @click="openPlatformDialog()" :icon="Plus">添加平台</el-button>
          <el-button @click="loadPlatforms" :icon="Refresh" :loading="loading">刷新</el-button>
        </div>

        <!-- AUTO-FIX-2026-07-02: 平台状态实时监控（在线/离线/最后心跳） -->
        <el-table :data="platforms" v-loading="loading" stripe style="width: 100%">
          <el-table-column prop="name" label="平台名称" min-width="140" />
          <el-table-column prop="role" label="角色" width="100">
            <template #default="{ row }">
              <el-tag :type="row.role === 'upstream' ? 'warning' : 'success'" size="small">
                {{ row.role === 'upstream' ? '上级平台' : '下级平台' }}
              </el-tag>
            </template>
          </el-table-column>
          <el-table-column prop="host" label="地址" min-width="160">
            <template #default="{ row }">{{ row.host }}:{{ row.port }}</template>
          </el-table-column>
          <el-table-column prop="link_type" label="链路" width="80">
            <template #default="{ row }">{{ row.link_type === 0 ? '主链路' : '从链路' }}</template>
          </el-table-column>
          <el-table-column label="状态" width="120">
            <template #default="{ row }">
              <span :class="['status-badge', getOnlineStatus(row)]">
                <span class="status-dot"></span>{{ getStatusLabel(row) }}
              </span>
            </template>
          </el-table-column>
          <el-table-column label="最后心跳" width="170">
            <template #default="{ row }">
              <span v-if="row.last_heartbeat">{{ formatTime(row.last_heartbeat) }}</span>
              <span v-else class="text-muted">—</span>
            </template>
          </el-table-column>
          <el-table-column prop="enabled" label="启用" width="70">
            <template #default="{ row }">
              <el-tag :type="row.enabled ? 'success' : 'info'" size="small">
                {{ row.enabled ? '是' : '否' }}
              </el-tag>
            </template>
          </el-table-column>
          <el-table-column label="操作" width="160" fixed="right">
            <template #default="{ row }">
              <el-button size="small" text @click="openPlatformDialog(row)">编辑</el-button>
              <el-button size="small" text type="danger" @click="deletePlatform(row)">删除</el-button>
            </template>
          </el-table-column>
        </el-table>
      </el-tab-pane>

      <!-- ======================== 转发规则 ======================== -->
      <el-tab-pane label="转发规则" name="forward-rules">
        <div class="tab-toolbar">
          <el-button type="primary" @click="openRuleDialog()" :icon="Plus">添加规则</el-button>
          <el-button @click="loadRules" :icon="Refresh" :loading="rulesLoading">刷新</el-button>
        </div>

        <el-table :data="forwardRules" v-loading="rulesLoading" stripe style="width: 100%">
          <el-table-column prop="platform_id" label="目标平台" min-width="120" />
          <el-table-column prop="source_platform_id" label="源平台" min-width="120">
            <template #default="{ row }">{{ row.source_platform_id || '所有平台' }}</template>
          </el-table-column>
          <el-table-column prop="data_type" label="数据类型" width="100">
            <template #default="{ row }">
              <el-tag size="small" :type="dataTypeTag(row.data_type)">{{ dataTypeLabel(row.data_type) }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column prop="phone" label="车辆" min-width="120">
            <template #default="{ row }">{{ row.phone || '全部车辆' }}</template>
          </el-table-column>
          <el-table-column prop="alarm_types" label="报警类型" min-width="120">
            <template #default="{ row }">{{ row.alarm_types || '全部' }}</template>
          </el-table-column>
          <el-table-column prop="min_level" label="最低级别" width="90">
            <template #default="{ row }">{{ levelLabel(row.min_level) }}</template>
          </el-table-column>
          <el-table-column label="时间限制" width="140">
            <template #default="{ row }">
              <span v-if="row.time_start">{{ row.time_start }} ~ {{ row.time_end }}</span>
              <span v-else class="text-muted">全天</span>
            </template>
          </el-table-column>
          <el-table-column prop="enabled" label="启用" width="70">
            <template #default="{ row }">
              <el-tag :type="row.enabled ? 'success' : 'info'" size="small">{{ row.enabled ? '是' : '否' }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column label="操作" width="160" fixed="right">
            <template #default="{ row }">
              <el-button size="small" text @click="openRuleDialog(row)">编辑</el-button>
              <el-button size="small" text type="danger" @click="deleteRule(row)">删除</el-button>
            </template>
          </el-table-column>
        </el-table>
      </el-tab-pane>

      <!-- ======================== 视频协商 ======================== -->
      <el-tab-pane label="视频协商" name="video-negotiation">
        <div class="tab-toolbar">
          <span class="text-muted">平台间视频协商/转发状态（实时更新）</span>
          <el-button @click="loadOnlinePlatforms" :icon="Refresh" :loading="negotiationLoading">刷新</el-button>
        </div>
        <el-table :data="onlineSessions" v-loading="negotiationLoading" stripe style="width: 100%">
          <el-table-column prop="platform_id" label="平台ID" min-width="120" />
          <el-table-column prop="status" label="连接状态" width="120">
            <template #default="{ row }">
              <el-tag :type="row.status === 'connected' ? 'success' : 'danger'" size="small">
                {{ row.status === 'connected' ? '已连接' : '断开' }}
              </el-tag>
            </template>
          </el-table-column>
          <el-table-column prop="active_streams" label="活跃视频流" width="110">
            <template #default="{ row }">{{ row.active_streams || 0 }}</template>
          </el-table-column>
          <el-table-column prop="forwarded_streams" label="转发中" width="90">
            <template #default="{ row }">{{ row.forwarded_streams || 0 }}</template>
          </el-table-column>
          <el-table-column prop="negotiation_count" label="协商次数" width="100">
            <template #default="{ row }">{{ row.negotiation_count || 0 }}</template>
          </el-table-column>
          <el-table-column label="最后协商时间" width="170">
            <template #default="{ row }">
              <span v-if="row.last_negotiation">{{ formatTime(row.last_negotiation) }}</span>
              <span v-else class="text-muted">—</span>
            </template>
          </el-table-column>
          <el-table-column prop="address" label="远端地址" min-width="160">
            <template #default="{ row }">{{ row.address || '—' }}</template>
          </el-table-column>
        </el-table>
        <el-empty v-if="!negotiationLoading && onlineSessions.length === 0" description="暂无在线平台会话" />
      </el-tab-pane>
    </el-tabs>

    <!-- ======================== 平台编辑弹窗 ======================== -->
    <el-dialog v-model="platformDialogVisible" :title="editingPlatform.id ? '编辑平台' : '添加平台'" width="500px">
      <el-form :model="editingPlatform" label-width="100px">
        <el-form-item label="平台名称" required>
          <el-input v-model="editingPlatform.name" placeholder="如：北京交通委" />
        </el-form-item>
        <el-form-item label="角色" required>
          <el-select v-model="editingPlatform.role" style="width: 100%">
            <el-option label="下级平台（被动接入）" value="downstream" />
            <el-option label="上级平台（主动连接）" value="upstream" />
          </el-select>
        </el-form-item>
        <el-form-item label="用户名" required>
          <el-input v-model="editingPlatform.user_id" placeholder="平台鉴权用户名" />
        </el-form-item>
        <el-form-item label="密码" required>
          <el-input v-model="editingPlatform.password" type="password" show-password placeholder="平台鉴权密码" />
        </el-form-item>
        <el-form-item v-if="editingPlatform.role === 'upstream'" label="地址" required>
          <el-input v-model="editingPlatform.host" placeholder="上级平台IP" />
        </el-form-item>
        <el-form-item v-if="editingPlatform.role === 'upstream'" label="端口" required>
          <el-input-number v-model="editingPlatform.port" :min="1" :max="65535" style="width: 100%" />
        </el-form-item>
        <el-form-item label="链路类型">
          <el-select v-model="editingPlatform.link_type" style="width: 100%">
            <el-option label="主链路" :value="0" />
            <el-option label="从链路" :value="1" />
          </el-select>
        </el-form-item>
        <el-form-item label="启用">
          <el-switch v-model="editingPlatform.enabled" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="platformDialogVisible = false">取消</el-button>
        <el-button type="primary" @click="savePlatform" :loading="saving">保存</el-button>
      </template>
    </el-dialog>

    <!-- ======================== 转发规则编辑弹窗 ======================== -->
    <el-dialog v-model="ruleDialogVisible" :title="editingRule.id ? '编辑转发规则' : '添加转发规则'" width="560px">
      <el-form :model="editingRule" label-width="110px">
        <el-form-item label="目标平台ID" required>
          <el-input v-model="editingRule.platform_id" placeholder="上级平台ID" :disabled="!!editingRule.id" />
        </el-form-item>
        <el-form-item label="源平台ID">
          <el-input v-model="editingRule.source_platform_id" placeholder="留空=所有下级平台" />
        </el-form-item>
        <el-form-item label="数据类型" required>
          <el-select v-model="editingRule.data_type" style="width: 100%">
            <el-option label="位置数据" value="location" />
            <el-option label="报警数据" value="alarm" />
            <el-option label="视频数据" value="video" />
          </el-select>
        </el-form-item>
        <el-form-item label="车辆手机号">
          <el-input v-model="editingRule.phone" placeholder="留空=全部车辆" />
        </el-form-item>
        <el-form-item v-if="editingRule.data_type === 'alarm'" label="报警类型">
          <el-input v-model="editingRule.alarm_types" placeholder="逗号分隔，留空=全部" />
        </el-form-item>
        <el-form-item v-if="editingRule.data_type === 'alarm'" label="最低级别">
          <el-select v-model="editingRule.min_level" style="width: 100%">
            <el-option label="全部" :value="0" />
            <el-option label="一般" :value="1" />
            <el-option label="严重" :value="2" />
            <el-option label="紧急" :value="3" />
          </el-select>
        </el-form-item>
        <el-form-item label="时间限制">
          <div style="display: flex; gap: 8px; align-items: center;">
            <el-time-picker v-model="editingRule.time_start" format="HH:mm:ss" value-format="HH:mm:ss" placeholder="开始" />
            <span>~</span>
            <el-time-picker v-model="editingRule.time_end" format="HH:mm:ss" value-format="HH:mm:ss" placeholder="结束" />
          </div>
        </el-form-item>
        <el-form-item label="启用">
          <el-switch v-model="editingRule.enabled" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="ruleDialogVisible = false">取消</el-button>
        <el-button type="primary" @click="saveRule" :loading="saving">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { ref, onMounted, onUnmounted } from 'vue'
import { Plus, Refresh } from '@element-plus/icons-vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { cascadeApi, forwardRuleApi } from '../api'

const activeTab = ref('platforms')

// 平台列表
const platforms = ref([])
const loading = ref(false)
const platformDialogVisible = ref(false)
const editingPlatform = ref({})
const saving = ref(false)

// 转发规则
const forwardRules = ref([])
const rulesLoading = ref(false)
const ruleDialogVisible = ref(false)
const editingRule = ref({})

// 视频协商
const onlineSessions = ref([])
const negotiationLoading = ref(false)

let pollTimer = null

async function loadPlatforms() {
  loading.value = true
  try {
const res = await cascadeApi.getPlatforms()
if (res.code === 0) {
platforms.value = res.data?.items || res.data || []
}
  } catch (e) {
    ElMessage.error('加载平台列表失败')
  } finally {
    loading.value = false
  }
}

async function loadRules() {
  rulesLoading.value = true
  try {
    const res = await forwardRuleApi.getList()
    if (res.code === 0) {
      forwardRules.value = Array.isArray(res.data) ? res.data : (res.data?.items || [])
    }
  } catch (e) {
    ElMessage.error('加载转发规则失败')
  } finally {
    rulesLoading.value = false
  }
}

async function loadOnlinePlatforms() {
  negotiationLoading.value = true
  try {
    const res = await cascadeApi.getOnlinePlatforms()
    if (res.code === 0) {
      onlineSessions.value = Array.isArray(res.data) ? res.data : (res.data?.items || [])
    }
  } catch (e) {
    // 静默处理
    onlineSessions.value = []
  } finally {
    negotiationLoading.value = false
  }
}

function openPlatformDialog(platform) {
  editingPlatform.value = platform
    ? { ...platform }
    : { name: '', role: 'downstream', user_id: '', password: '', host: '', port: 2404, link_type: 0, enabled: true }
  platformDialogVisible.value = true
}

async function savePlatform() {
  const p = editingPlatform.value
  if (!p.name || !p.user_id || !p.password) {
    ElMessage.warning('请填写必填项')
    return
  }
  if (p.role === 'upstream' && (!p.host || !p.port)) {
    ElMessage.warning('上级平台需填写地址和端口')
    return
  }
  saving.value = true
  try {
    const data = { ...p }
    if (!data.id) delete data.id
    const res = p.id
      ? await cascadeApi.updatePlatform(p.id, data)
      : await cascadeApi.createPlatform(data)
    if (res.code === 0) {
      ElMessage.success(p.id ? '更新成功' : '添加成功')
      platformDialogVisible.value = false
      loadPlatforms()
    } else {
      ElMessage.error(res.message || '操作失败')
    }
  } catch (e) {
    ElMessage.error('操作失败')
  } finally {
    saving.value = false
  }
}

async function deletePlatform(platform) {
  try {
    await ElMessageBox.confirm(`确认删除平台「${platform.name}」？`, '删除确认', { type: 'warning' })
    const res = await cascadeApi.deletePlatform(platform.id)
    if (res.code === 0) {
      ElMessage.success('删除成功')
      loadPlatforms()
    } else {
      ElMessage.error(res.message || '删除失败')
    }
  } catch (e) { /* cancelled */ }
}

function openRuleDialog(rule) {
  editingRule.value = rule
    ? { ...rule }
    : { platform_id: '', source_platform_id: '', data_type: 'location', phone: '', alarm_types: '', min_level: 0, time_start: '', time_end: '', enabled: true }
  ruleDialogVisible.value = true
}

async function saveRule() {
  const r = editingRule.value
  if (!r.platform_id) {
    ElMessage.warning('请填写目标平台ID')
    return
  }
  if (!r.data_type) {
    ElMessage.warning('请选择数据类型')
    return
  }
  saving.value = true
  try {
    const data = { ...r }
    if (!data.id) delete data.id
    const res = r.id
      ? await forwardRuleApi.update(r.id, data)
      : await forwardRuleApi.create(data)
    if (res.code === 0) {
      ElMessage.success(r.id ? '更新成功' : '添加成功')
      ruleDialogVisible.value = false
      loadRules()
    } else {
      ElMessage.error(res.message || '操作失败')
    }
  } catch (e) {
    ElMessage.error('操作失败')
  } finally {
    saving.value = false
  }
}

async function deleteRule(rule) {
  try {
    await ElMessageBox.confirm('确认删除此转发规则？', '删除确认', { type: 'warning' })
    const res = await forwardRuleApi.delete(rule.id)
    if (res.code === 0) {
      ElMessage.success('删除成功')
      loadRules()
    } else {
      ElMessage.error(res.message || '删除失败')
    }
  } catch (e) { /* cancelled */ }
}

// 状态辅助
function getOnlineStatus(platform) {
  if (!platform.enabled) return 'offline'
  if (platform.status === 'connected') return 'online'
  return 'offline'
}

function getStatusLabel(platform) {
  if (!platform.enabled) return '已禁用'
  if (platform.status === 'connected') return '在线'
  return '离线'
}

function formatTime(t) {
  if (!t) return '—'
  const d = new Date(t)
  return d.toLocaleString('zh-CN', { hour12: false })
}

function dataTypeLabel(type) {
  return { location: '位置', alarm: '报警', video: '视频' }[type] || type
}

function dataTypeTag(type) {
  return { location: 'success', alarm: 'danger', video: 'warning' }[type] || ''
}

function levelLabel(level) {
  return { 0: '全部', 1: '一般', 2: '严重', 3: '紧急' }[level] || '全部'
}

onMounted(() => {
  loadPlatforms()
  loadRules()
  loadOnlinePlatforms()
  // AUTO-FIX-2026-07-02: 平台状态实时监控（15s 轮询）
  pollTimer = setInterval(() => {
    if (activeTab.value === 'platforms') loadPlatforms()
    if (activeTab.value === 'video-negotiation') loadOnlinePlatforms()
  }, 15000)
})

onUnmounted(() => {
  if (pollTimer) clearInterval(pollTimer)
})
</script>

<style scoped>
.cascade-page { padding: 16px; }
.page-header { margin-bottom: 16px; }
.page-header h2 { margin: 0 0 4px; font-size: 20px; }
.header-desc { margin: 0; color: var(--jte-text-muted); font-size: 13px; }
.cascade-tabs { background: var(--jte-surface); border-radius: 8px; padding: 12px; }
.tab-toolbar { display: flex; justify-content: space-between; align-items: center; margin-bottom: 12px; gap: 8px; flex-wrap: wrap; }
.status-badge { display: inline-flex; align-items: center; gap: 4px; font-size: 12px; }
.status-badge .status-dot { width: 6px; height: 6px; border-radius: 50%; }
.status-badge.online .status-dot { background: #22c55e; }
.status-badge.online { color: #22c55e; }
.status-badge.offline .status-dot { background: #ef4444; }
.status-badge.offline { color: #ef4444; }
.text-muted { color: var(--jte-text-muted); font-size: 12px; }
/* 响应式布局适配 */
@media (max-width: 768px) {
  .cascade-page { padding: 8px; }
  .cascade-tabs { padding: 4px; }
  .tab-toolbar { flex-direction: column; align-items: stretch; }
}
</style>
