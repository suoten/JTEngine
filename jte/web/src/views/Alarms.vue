<template>
  <div class="alarms-page">
    <div class="page-header">
      <h1 class="page-title">报警中心</h1>
      <p class="header-desc">报警实时推送、处理闭环（确认→派单→处理→归档）、结果上报终端</p>
    </div>

    <div class="page-content">
      <!-- 统计卡片 -->
      <el-row :gutter="16" style="margin-bottom: 16px;">
        <el-col :xs="12" :sm="6">
          <el-card class="stat-card" shadow="never">
            <div class="stat-value">{{ alarmStats.total || 0 }}</div>
            <div class="stat-label">总报警数</div>
          </el-card>
        </el-col>
        <el-col :xs="12" :sm="6">
          <el-card class="stat-card" shadow="never">
            <div class="stat-value" style="color: var(--jte-danger);">{{ alarmStats.today || 0 }}</div>
            <div class="stat-label">{{ $t('alarm.today') }}</div>
          </el-card>
        </el-col>
        <el-col :xs="12" :sm="6">
          <el-card class="stat-card" shadow="never">
            <div class="stat-value" style="color: var(--jte-warning);">{{ alarmStats.jt808 || 0 }}</div>
            <div class="stat-label">808报警</div>
          </el-card>
        </el-col>
        <el-col :xs="12" :sm="6">
          <el-card class="stat-card" shadow="never">
            <div class="stat-value" style="color: var(--jte-accent);">{{ alarmStats.jt1045 || 0 }}</div>
            <div class="stat-label">1045报警</div>
          </el-card>
        </el-col>
      </el-row>

      <el-card shadow="never">
        <template #header>
          <div class="card-header">
            <el-tabs v-model="activeTab" class="header-tabs">
              <el-tab-pane label="实时报警" name="realtime" />
              <el-tab-pane label="历史报警" name="history" />
            </el-tabs>
            <div class="filter-bar">
              <el-select v-model="filterSource" placeholder="来源" size="small" style="width: 120px;" clearable>
                <el-option label="JT/T 808" value="jt808" />
                <el-option label="JT/T 809" value="jt809" />
                <el-option label="JT/T 1045" value="jt1045" />
              </el-select>
              <el-select v-model="filterAI" placeholder="AI过滤" size="small" style="width: 120px;" clearable>
                <el-option label="全部" value="" />
                <el-option label="AI误报" value="false_alarm" />
                <el-option label="AI真实" value="real" />
                <el-option label="未过滤" value="unfiltered" />
              </el-select>
              <el-select v-if="activeTab === 'history'" v-model="filterStatus" placeholder="处理状态" size="small" style="width: 120px;" clearable>
                <el-option label="待处理" value="pending" />
                <el-option label="已确认" value="acked" />
                <el-option label="处理中" value="processing" />
                <el-option label="已归档" value="closed" />
              </el-select>
              <el-button size="small" @click="fetchAlarms" :icon="Refresh" :loading="loading" />
            </div>
          </div>
        </template>

        <el-table :data="filteredAlarms" style="width: 100%" size="small" v-loading="loading" @row-click="openDetail">
          <el-table-column prop="id" label="报警ID" width="180">
            <template #default="{ row }">
              <span class="alarm-id">{{ row.id }}</span>
            </template>
          </el-table-column>
          <el-table-column prop="vehicle_id" label="车辆" min-width="120" />
          <el-table-column prop="type" label="类型" width="120">
            <template #default="{ row }">
              <el-tag size="small" :type="alarmTagType(row.type)">{{ row.type }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column prop="source" label="来源" width="90">
            <template #default="{ row }">
              <el-tag size="small" type="info">{{ (row.source || '').toUpperCase() }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column label="AI过滤" width="100">
            <template #default="{ row }">
              <el-tag v-if="getAIFilter(row).isFalse" size="small" type="warning">误报</el-tag>
              <el-tag v-else-if="getAIFilter(row).filtered" size="small" type="success">真实</el-tag>
              <span v-else class="text-muted">-</span>
            </template>
          </el-table-column>
          <el-table-column label="处理状态" width="100">
            <template #default="{ row }">
              <el-tag :type="statusTagType(getAlarmStatus(row))" size="small">{{ statusLabel(getAlarmStatus(row)) }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column prop="received_at" label="接收时间" min-width="160">
            <template #default="{ row }">{{ formatTime(row.received_at) }}</template>
          </el-table-column>
          <el-table-column label="操作" width="200" fixed="right">
            <template #default="{ row }">
              <el-button size="small" text @click.stop="openDetail(row)">详情</el-button>
              <el-button v-if="getAlarmStatus(row) === 'pending'" size="small" text type="primary" @click.stop="openHandleDialog(row, 'ack')">确认</el-button>
              <el-button v-if="getAlarmStatus(row) === 'acked'" size="small" text type="warning" @click.stop="openHandleDialog(row, 'process')">派单</el-button>
              <el-button v-if="getAlarmStatus(row) === 'processing'" size="small" text type="success" @click.stop="openHandleDialog(row, 'close')">归档</el-button>
            </template>
          </el-table-column>
        </el-table>

        <!-- 历史报警分页 -->
        <div v-if="activeTab === 'history'" class="pagination-bar">
          <el-pagination
            v-model:current-page="page"
            v-model:page-size="pageSize"
            :total="total"
            layout="total, prev, pager, next"
            @current-change="fetchAlarms"
            small
          />
        </div>
      </el-card>
    </div>

    <!-- ======================== 报警详情弹窗 ======================== -->
    <el-dialog v-model="detailVisible" title="报警详情" width="700px" @open="loadDetail">
      <div v-loading="detailLoading">
        <el-descriptions :column="2" border size="small" v-if="detailData">
          <el-descriptions-item label="报警ID">{{ detailData.id }}</el-descriptions-item>
          <el-descriptions-item label="车辆ID">{{ detailData.vehicle_id }}</el-descriptions-item>
          <el-descriptions-item label="手机号">{{ detailData.phone }}</el-descriptions-item>
          <el-descriptions-item label="类型">
            <el-tag size="small" :type="alarmTagType(detailData.type)">{{ detailData.type }}</el-tag>
          </el-descriptions-item>
          <el-descriptions-item label="来源">{{ detailData.source }}</el-descriptions-item>
          <el-descriptions-item label="级别">
            <el-tag :type="detailData.level >= 3 ? 'danger' : detailData.level >= 2 ? 'warning' : 'info'" size="small">
              {{ { 1: '一般', 2: '严重', 3: '紧急' }[detailData.level] || '一般' }}
            </el-tag>
          </el-descriptions-item>
          <el-descriptions-item label="位置">{{ detailData.latitude?.toFixed(6) || '-' }}, {{ detailData.longitude?.toFixed(6) || '-' }}</el-descriptions-item>
          <el-descriptions-item label="速度/方向">{{ detailData.speed?.toFixed(1) || '-' }} km/h / {{ detailData.direction || '-' }}°</el-descriptions-item>
          <el-descriptions-item label="接收时间" :span="2">{{ formatTime(detailData.received_at) }}</el-descriptions-item>
          <el-descriptions-item label="AI判定" :span="2">
            <span v-if="getAIFilter(detailData).filtered">
              {{ getAIFilter(detailData).isFalse ? '误报' : '真实报警' }} ({{ (getAIFilter(detailData).confidence * 100).toFixed(0) }}%)
              <span v-if="getAIFilter(detailData).reason" class="text-muted"> — {{ getAIFilter(detailData).reason }}</span>
            </span>
            <span v-else class="text-muted">未过滤</span>
          </el-descriptions-item>
        </el-descriptions>

        <!-- AUTO-FIX-2026-07-02: 多媒体附件预览 -->
        <div v-if="detailAttachments.length > 0" class="attachment-section">
          <h4>多媒体附件</h4>
          <div class="attachment-grid">
            <div v-for="(att, i) in detailAttachments" :key="i" class="attachment-item">
              <img v-if="att.type === 'image'" :src="att.url" @click="previewImage(att.url)" />
              <video v-else-if="att.type === 'video'" :src="att.url" controls />
              <span v-else class="attachment-file"><el-icon><Paperclip /></el-icon> {{ att.name || '附件' }}</span>
            </div>
          </div>
        </div>

        <!-- 处理流程时间线 -->
        <div v-if="handleHistory.length > 0" class="timeline-section">
          <h4>处理记录</h4>
          <el-timeline>
            <el-timeline-item
              v-for="(h, i) in handleHistory"
              :key="i"
              :timestamp="h.time"
              :type="h.type === 'close' ? 'success' : 'primary'"
            >
              <strong>{{ h.action }}</strong> — {{ h.operator }}
              <p v-if="h.description" class="text-muted">{{ h.description }}</p>
            </el-timeline-item>
          </el-timeline>
        </div>
      </div>

      <template #footer>
        <div style="display: flex; gap: 8px; justify-content: flex-end;">
          <el-button @click="detailVisible = false">关闭</el-button>
          <el-button v-if="detailData && getAlarmStatus(detailData) === 'pending'" type="primary" @click="openHandleDialog(detailData, 'ack')">确认报警</el-button>
          <el-button v-if="detailData && getAlarmStatus(detailData) === 'acked'" type="warning" @click="openHandleDialog(detailData, 'process')">派单处理</el-button>
          <el-button v-if="detailData && getAlarmStatus(detailData) === 'processing'" type="success" @click="openHandleDialog(detailData, 'close')">归档关闭</el-button>
          <el-button v-if="detailData" @click="reportToTerminal(detailData)">上报终端</el-button>
        </div>
      </template>
    </el-dialog>

    <!-- ======================== 处理弹窗 ======================== -->
    <el-dialog v-model="handleDialogVisible" :title="handleDialogTitle" width="480px">
      <el-form :model="handleForm" label-width="80px">
        <el-form-item v-if="handleType === 'ack'" label="操作人">
          <el-input v-model="handleForm.operator" placeholder="操作人姓名" />
        </el-form-item>
        <el-form-item v-if="handleType === 'ack'" label="备注">
          <el-input v-model="handleForm.remark" type="textarea" :rows="2" placeholder="确认备注" />
        </el-form-item>
        <el-form-item v-if="handleType === 'process'" label="操作人">
          <el-input v-model="handleForm.operator" placeholder="操作人姓名" />
        </el-form-item>
        <el-form-item v-if="handleType === 'process'" label="处理动作">
          <el-input v-model="handleForm.action" placeholder="如：派维修人员现场处理" />
        </el-form-item>
        <el-form-item v-if="handleType === 'process'" label="处理描述">
          <el-input v-model="handleForm.description" type="textarea" :rows="3" placeholder="详细处理描述" />
        </el-form-item>
        <el-form-item v-if="handleType === 'close'" label="操作人">
          <el-input v-model="handleForm.operator" placeholder="操作人姓名" />
        </el-form-item>
        <el-form-item v-if="handleType === 'close'" label="关闭原因">
          <el-input v-model="handleForm.reason" type="textarea" :rows="2" placeholder="归档关闭原因" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="handleDialogVisible = false">取消</el-button>
        <el-button type="primary" @click="submitHandle" :loading="handling">确认</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { Refresh } from '@element-plus/icons-vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { alarmApi } from '../api'

const activeTab = ref('realtime')
const alarms = ref([])
const loading = ref(false)
const filterSource = ref('')
const filterAI = ref('')
const filterStatus = ref('')
const alarmStats = ref({ total: 0, today: 0, jt808: 0, jt1045: 0 })

// 分页（历史报警）
const page = ref(1)
const pageSize = ref(50)
const total = ref(0)

// 详情弹窗
const detailVisible = ref(false)
const detailLoading = ref(false)
const detailData = ref(null)
const detailAttachments = ref([])
const handleHistory = ref([])

// 处理弹窗
const handleDialogVisible = ref(false)
const handleType = ref('ack')
const handleForm = ref({})
const handling = ref(false)
const currentAlarm = ref(null)

let ws = null
let wsReconnectTimer = null
let wsReconnectCount = 0 // FIXED: [WebSocket断连] 指数退避计数 [2026-07-17]
let manuallyClosed = false // FIXED: [WebSocket断连] 手动关闭标志 [2026-07-17]
let wsHeartbeatTimer = null // FIXED: [WebSocket心跳] 定时发送心跳保活 [2026-07-17]
let refreshTimer = null

function getAIFilter(row) {
  if (!row?.additional) return { filtered: false, isFalse: false, confidence: 0 }
  try {
    let raw = row.additional
    if (typeof raw === 'string') {
      // 尝试 base64 解码
      try { raw = atob(raw) } catch { /* not base64 */ }
    }
    const parsed = typeof raw === 'string' ? JSON.parse(raw) : raw
    if (parsed && parsed.source === 'ai') {
      return { filtered: true, isFalse: !!parsed.is_false_alarm, confidence: parsed.confidence || 0, reason: parsed.reason || '' }
    }
  } catch { /* ignore */ }
  return { filtered: false, isFalse: false, confidence: 0 }
}

// AUTO-FIX-2026-07-02: 从 Source 字段解析处理状态（后端将处理记录追加到 Source 字段）
function getAlarmStatus(row) {
  const src = row?.source || ''
  if (src.includes('|close:')) return 'closed'
  if (src.includes('|process:')) return 'processing'
  if (src.includes('|ack:')) return 'acked'
  return 'pending'
}

function statusTagType(status) {
  return { pending: 'danger', acked: 'warning', processing: '', closed: 'success' }[status] || 'info'
}

function statusLabel(status) {
  return { pending: '待处理', acked: '已确认', processing: '处理中', closed: '已归档' }[status] || status
}

// AUTO-FIX-2026-07-02: 从 Source 字段解析处理历史记录
function parseHandleHistory(row) {
  const src = row?.source || ''
  const records = []
  const parts = src.split('|')
  for (const p of parts) {
    if (p.startsWith('ack:')) {
      const segs = p.substring(4).split('|')
      records.push({ action: '确认报警', operator: segs[0] || '', description: segs[1] || '', time: row.received_at, type: 'ack' })
    } else if (p.startsWith('process:')) {
      const segs = p.substring(8).split('|')
      records.push({ action: '派单处理', operator: segs[0] || '', description: `${segs[1] || ''} ${segs[2] || ''}`.trim(), time: row.received_at, type: 'process' })
    } else if (p.startsWith('close:')) {
      const segs = p.substring(6).split('|')
      records.push({ action: '归档关闭', operator: segs[0] || '', description: segs[1] || '', time: row.received_at, type: 'close' })
    }
  }
  return records
}

const filteredAlarms = computed(() => {
  let result = alarms.value
  if (filterSource.value) result = result.filter(a => a.source?.startsWith(filterSource.value))
  if (filterAI.value) {
    result = result.filter(a => {
      const ai = getAIFilter(a)
      if (filterAI.value === 'false_alarm') return ai.filtered && ai.isFalse
      if (filterAI.value === 'real') return ai.filtered && !ai.isFalse
      if (filterAI.value === 'unfiltered') return !ai.filtered
      return true
    })
  }
  if (filterStatus.value) result = result.filter(a => getAlarmStatus(a) === filterStatus.value)
  return result
})

async function fetchAlarms() {
  loading.value = true
  try {
    const params = activeTab.value === 'history'
      ? { page: page.value, page_size: pageSize.value }
      : { page: 1, page_size: 100 }
    const data = await alarmApi.getList(params)
    // FIXED-2026-07-24: API 返回 {alarms:null} 时 data 是对象非数组，需 Array.isArray 兜底
const _raw = data.alarms || data.data || data
alarms.value = Array.isArray(_raw) ? _raw : []
    if (activeTab.value === 'history') {
      total.value = data.total || alarms.value.length
    }
  } catch { alarms.value = []; ElMessage.error('加载报警列表失败，请检查网络或稍后重试') }
  finally { loading.value = false }

  try {
    const stats = await alarmApi.getStats()
    alarmStats.value = stats || alarmStats.value
  } catch { /* silent */ }
}

// 详情
async function openDetail(row) {
  detailData.value = row
  detailAttachments.value = []
  handleHistory.value = parseHandleHistory(row)
  detailVisible.value = true
}

async function loadDetail() {
  if (!detailData.value?.id) return
  detailLoading.value = true
  try {
    const res = await alarmApi.getDetail(detailData.value.id)
    if (res.code === 0 && res.data) {
      detailData.value = res.data
      handleHistory.value = parseHandleHistory(res.data)
      // 解析多媒体附件
      parseAttachments(res.data)
    }
  } catch { /* silent */ }
  finally { detailLoading.value = false }
}

// AUTO-FIX-2026-07-02: 多媒体附件预览
function parseAttachments(alarm) {
  const attachments = []
  // additional 字段可能包含多媒体引用
  if (alarm.additional) {
    try {
      let raw = alarm.additional
      if (typeof raw === 'string') { try { raw = atob(raw) } catch {} }
      const parsed = typeof raw === 'string' ? JSON.parse(raw) : raw
      if (parsed?.attachments && Array.isArray(parsed.attachments)) {
        parsed.attachments.forEach(att => {
          attachments.push({ type: att.type || 'file', url: att.url || att.path || '', name: att.name || '' })
        })
      }
    } catch { /* ignore */ }
  }
  detailAttachments.value = attachments
}

// FIXED: [XSS] window.open 添加 noopener,noreferrer 防止反向钓鱼 [2026-07-17]
function previewImage(url) {
  window.open(url, '_blank', 'noopener,noreferrer')
}

// 处理流程
const handleDialogTitle = computed(() => {
  return { ack: '确认报警', process: '派单处理', close: '归档关闭' }[handleType.value] || ''
})

function openHandleDialog(row, type) {
  currentAlarm.value = row
  handleType.value = type
  handleForm.value = { operator: '', remark: '', action: '', description: '', reason: '' }
  handleDialogVisible.value = true
}

async function submitHandle() {
  if (!currentAlarm.value) return
  handling.value = true
  try {
    const id = currentAlarm.value.id
    let res
    if (handleType.value === 'ack') {
      res = await alarmApi.ackAlarm(id, { operator: handleForm.value.operator, remark: handleForm.value.remark })
    } else if (handleType.value === 'process') {
      res = await alarmApi.processAlarm(id, { operator: handleForm.value.operator, action: handleForm.value.action, description: handleForm.value.description })
    } else if (handleType.value === 'close') {
      res = await alarmApi.closeAlarm(id, { operator: handleForm.value.operator, reason: handleForm.value.reason })
    }
    if (res?.code === 0) {
      ElMessage.success('操作成功')
      handleDialogVisible.value = false
      fetchAlarms()
      if (detailVisible.value) loadDetail()
    } else {
      ElMessage.error(res?.message || '操作失败')
    }
  } catch (e) {
    ElMessage.error('操作失败')
  } finally {
    handling.value = false
  }
}

// AUTO-FIX-2026-07-02: 报警处理结果上报终端
async function reportToTerminal(alarm) {
  try {
    await ElMessageBox.confirm('确认将处理结果上报到终端设备？', '上报确认', { type: 'info' })
    const res = await alarmApi.reportToTerminal(alarm.id, {
      alarm_type: alarm.type,
      level: alarm.level || 1,
      content: `报警 ${alarm.id} 处理结果已归档`,
    })
    if (res?.code === 0) {
      ElMessage.success('处理结果已上报终端')
    } else {
      ElMessage.error(res?.message || '上报失败')
    }
  } catch { /* cancelled */ }
}

// FIXED: [WebSocket断连] 增加 token 鉴权 + 指数退避重连 + 手动关闭标志 [2026-07-17]
// WebSocket 实时推送
function connectWebSocket() {
  if (manuallyClosed) return

  const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  const token = localStorage.getItem('jte_token') || ''
  // FIXED: [P0] WebSocket 连接必须携带 JWT token，否则后端返回 401 [2026-07-17]
  const wsUrl = `${protocol}//${window.location.host}/ws/v1/stream?token=${encodeURIComponent(token)}`
  try { ws = new WebSocket(wsUrl) }
  catch { scheduleReconnect(); return }

  ws.onopen = () => {
    wsReconnectCount = 0
    if (ws?.readyState === WebSocket.OPEN) {
      ws.send(JSON.stringify({ action: 'subscribe', topics: ['alarm_event'] }))
      // FIXED: [WebSocket心跳] 每30秒发送心跳保活 [2026-07-17]
      wsHeartbeatTimer = setInterval(() => {
        if (ws && ws.readyState === WebSocket.OPEN) {
          ws.send(JSON.stringify({ action: 'ping' }))
        }
      }, 30000)
    }
  }
  ws.onmessage = (event) => {
    try {
      const msg = JSON.parse(event.data)
      if (msg.topic === 'alarm_event' && msg.data) {
        if (activeTab.value === 'realtime') {
          alarms.value = [msg.data, ...alarms.value].slice(0, 200)
        }
        alarmStats.value.today = (alarmStats.value.today || 0) + 1
        alarmStats.value.total = (alarmStats.value.total || 0) + 1
        if (msg.data.source === 'jt808') alarmStats.value.jt808 = (alarmStats.value.jt808 || 0) + 1
        else if (msg.data.source === 'jt1045') alarmStats.value.jt1045 = (alarmStats.value.jt1045 || 0) + 1
      }
    } catch { /* ignore */ }
  }
  ws.onclose = () => {
    if (manuallyClosed) return
    if (wsHeartbeatTimer) { clearInterval(wsHeartbeatTimer); wsHeartbeatTimer = null }
    scheduleReconnect()
  }
  ws.onerror = () => { if (ws) ws.close() }
}

function scheduleReconnect() {
  if (wsReconnectTimer) return
  // FIXED: [WebSocket断连] 指数退避重连（1s→2s→4s→...→30s） [2026-07-17]
  const delay = Math.min(1000 * Math.pow(2, wsReconnectCount), 30000)
  wsReconnectCount++
  wsReconnectTimer = setTimeout(() => { wsReconnectTimer = null; connectWebSocket() }, delay)
}

function alarmTagType(type) {
  if (type?.includes('dsm') || type?.includes('adas')) return 'danger'
  if (type?.includes('blind')) return 'warning'
  return 'info'
}

function formatTime(t) {
  if (!t) return '-'
  return new Date(t).toLocaleString('zh-CN', { hour12: false })
}

onMounted(() => {
  fetchAlarms()
  connectWebSocket()
  refreshTimer = setInterval(() => {
    if (activeTab.value === 'realtime') fetchAlarms()
  }, 30000)
})

onUnmounted(() => {
  if (refreshTimer) clearInterval(refreshTimer)
  // FIXED: [WebSocket断连] 清理重连定时器 + 心跳定时器 + 设置手动关闭标志 [2026-07-17]
  manuallyClosed = true
  if (wsReconnectTimer) clearTimeout(wsReconnectTimer)
  if (wsHeartbeatTimer) clearInterval(wsHeartbeatTimer)
  if (ws) { ws.onclose = null; ws.close() }
})
</script>

<style scoped>
.alarms-page { padding: 16px; }
.page-header { margin-bottom: 16px; }
.page-title { margin: 0; font-size: 20px; }
.header-desc { margin: 4px 0 0; color: var(--jte-text-muted); font-size: 13px; }
.stat-card { text-align: center; }
.stat-value { font-size: 28px; font-weight: 700; }
.stat-label { font-size: 12px; color: var(--jte-text-muted); margin-top: 4px; }
.card-header { display: flex; justify-content: space-between; align-items: center; flex-wrap: wrap; gap: 8px; }
.header-tabs { margin-bottom: -10px; }
.header-tabs :deep(.el-tabs__header) { margin: 0; }
.filter-bar { display: flex; gap: 8px; align-items: center; flex-wrap: wrap; }
.alarm-id { font-family: monospace; font-size: 12px; }
.text-muted { color: var(--jte-text-muted); font-size: 12px; }
.pagination-bar { margin-top: 12px; display: flex; justify-content: flex-end; }
.attachment-section { margin-top: 16px; }
.attachment-section h4 { margin: 0 0 8px; font-size: 14px; }
.attachment-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(120px, 1fr)); gap: 8px; }
.attachment-item { cursor: pointer; }
.attachment-item img { width: 100%; height: 80px; object-fit: cover; border-radius: 4px; }
.attachment-item video { width: 100%; height: 80px; border-radius: 4px; }
.attachment-file { display: block; padding: 20px; text-align: center; background: var(--jte-surface-2); border-radius: 4px; font-size: 12px; }
.timeline-section { margin-top: 16px; }
.timeline-section h4 { margin: 0 0 8px; font-size: 14px; }
/* 响应式布局 */
@media (max-width: 768px) {
  .alarms-page { padding: 8px; }
  .card-header { flex-direction: column; align-items: stretch; }
  .filter-bar { flex-direction: column; align-items: stretch; }
}
</style>
