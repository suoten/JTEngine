<template>
  <div>
    <div class="page-header">
      <div style="display: flex; align-items: center; justify-content: space-between;">
        <div>
          <h1 class="page-title">系统概览</h1>
          <p style="color: var(--jte-text-muted); font-size: 13px; margin-top: 4px;">
            JTE JT Engine 实时监控仪表盘
          </p>
        </div>
        <div style="display: flex; align-items: center; gap: 8px;">
          <div class="live-indicator"></div>
          <span style="font-size: 12px; color: var(--jte-text-muted);">实时</span>
        </div>
      </div>
    </div>

    <div class="page-content">
      <el-row :gutter="20" style="margin-bottom: 24px;">
        <el-col :span="6" v-for="stat in statsCards" :key="stat.label">
          <el-card class="stat-card" shadow="never">
            <div style="display: flex; align-items: flex-start; justify-content: space-between;">
              <div>
                <div class="stat-value">{{ stat.value }}</div>
                <div class="stat-label">{{ stat.label }}</div>
              </div>
              <div class="stat-icon" :style="{ background: stat.bgColor }">
                <el-icon :size="20" :style="{ color: stat.color }">
                  <component :is="stat.icon" />
                </el-icon>
              </div>
            </div>
          </el-card>
        </el-col>
      </el-row>

      <el-row :gutter="20">
        <el-col :span="16">
          <el-card shadow="never">
            <template #header>
              <div style="display: flex; align-items: center; justify-content: space-between;">
                <span style="font-weight: 500; font-size: 14px;">最近会话</span>
                <router-link to="/sessions" style="font-size: 12px; color: var(--jte-accent); text-decoration: none;">
                  查看全部 →
                </router-link>
              </div>
            </template>
            <el-table :data="recentSessions" style="width: 100%" size="small" :header-cell-style="{ fontSize: '11px' }">
              <el-table-column prop="id" label="会话ID" width="200">
                <template #default="{ row }">
                  <span style="font-family: monospace; font-size: 12px;">{{ row.id?.substring(0, 20) }}...</span>
                </template>
              </el-table-column>
              <el-table-column prop="phone" label="终端手机号" width="140" />
              <el-table-column prop="protocol" label="协议" width="100">
                <template #default="{ row }">
                  <el-tag size="small" :type="protocolTagType(row.protocol)">{{ row.protocol?.toUpperCase() }}</el-tag>
                </template>
              </el-table-column>
              <el-table-column prop="status" label="状态" width="100">
                <template #default="{ row }">
                  <div style="display: flex; align-items: center; gap: 6px;">
                    <div :class="row.status === 'authenticated' ? 'live-indicator' : ''"
                         :style="row.status !== 'authenticated' ? { width: '8px', height: '8px', borderRadius: '50%', background: 'var(--jte-text-muted)' } : {}">
                    </div>
                    <span style="font-size: 12px;">{{ statusLabel(row.status) }}</span>
                  </div>
                </template>
              </el-table-column>
              <el-table-column prop="remote_addr" label="远程地址" />
            </el-table>
          </el-card>
        </el-col>

        <el-col :span="8">
          <el-card shadow="never">
            <template #header>
              <span style="font-weight: 500; font-size: 14px;">协议分布</span>
            </template>
            <div style="padding: 8px 0;">
              <div v-for="proto in protocolStats" :key="proto.name" style="margin-bottom: 16px;">
                <div style="display: flex; justify-content: space-between; margin-bottom: 6px;">
                  <span style="font-size: 13px;">{{ proto.name }}</span>
                  <span style="font-size: 13px; color: var(--jte-text-muted);">{{ proto.count }}</span>
                </div>
                <div style="height: 4px; background: var(--jte-surface-2); border-radius: 2px; overflow: hidden;">
                  <div :style="{
                    width: proto.percent + '%',
                    height: '100%',
                    background: proto.color,
                    borderRadius: '2px',
                    transition: 'width 0.5s ease'
                  }"></div>
                </div>
              </div>
            </div>
          </el-card>

          <el-card shadow="never" style="margin-top: 20px;">
            <template #header>
              <span style="font-weight: 500; font-size: 14px;">最近报警</span>
            </template>
            <div v-for="alarm in recentAlarms" :key="alarm.id" style="padding: 10px 0; border-bottom: 1px solid var(--jte-border);">
              <div style="display: flex; align-items: center; gap: 8px;">
                <el-icon :size="14" color="var(--jte-warning)"><Warning /></el-icon>
                <span style="font-size: 13px;">{{ alarm.type }}</span>
              </div>
              <div style="font-size: 11px; color: var(--jte-text-muted); margin-top: 4px; margin-left: 22px;">
                {{ alarm.vehicle_id }} · {{ formatTime(alarm.received_at) }}
              </div>
            </div>
            <div v-if="recentAlarms.length === 0" style="text-align: center; padding: 20px; color: var(--jte-text-muted); font-size: 13px;">
              暂无报警
            </div>
          </el-card>
        </el-col>
      </el-row>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted, onUnmounted } from 'vue'
import { statsApi, sessionApi, alarmApi } from '../api'

const statsCards = ref([
  { label: '在线设备', value: '0', icon: 'Monitor', color: '#6366f1', bgColor: 'rgba(99,102,241,0.12)' },
  { label: '总会话数', value: '0', icon: 'Connection', color: '#3b82f6', bgColor: 'rgba(59,130,246,0.12)' },
  { label: '今日报警', value: '0', icon: 'Warning', color: '#f59e0b', bgColor: 'rgba(245,158,11,0.12)' },
  { label: '协议类型', value: '7', icon: 'Cpu', color: '#22c55e', bgColor: 'rgba(34,197,94,0.12)' },
])

const recentSessions = ref([])
const recentAlarms = ref([])
const protocolStats = ref([
  { name: 'JT/T 808', count: 0, percent: 0, color: '#6366f1' },
  { name: 'JT/T 809', count: 0, percent: 0, color: '#3b82f6' },
  { name: 'JT/T 1078', count: 0, percent: 0, color: '#8b5cf6' },
  { name: 'JT/T 1045', count: 0, percent: 0, color: '#22c55e' },
  { name: 'JT/T 905', count: 0, percent: 0, color: '#f59e0b' },
  { name: 'JT/T 1253', count: 0, percent: 0, color: '#ef4444' },
  { name: 'GB/T 32960', count: 0, percent: 0, color: '#06b6d4' },
])

let timer = null
let ws = null
let wsReconnectTimer = null
let wsReconnectCount = 0 // FIXED: [WebSocket断连] 指数退避计数 [2026-07-17]
let manuallyClosed = false // FIXED: [WebSocket断连] 手动关闭标志 [2026-07-17]
let wsHeartbeatTimer = null // FIXED: [WebSocket心跳] 定时发送心跳保活 [2026-07-17]

async function fetchData() {
  try {
    const overview = await statsApi.getOverview().catch(() => null)
    if (overview) {
      statsCards.value[0].value = String(overview.online_count ?? 0)
      statsCards.value[1].value = String(overview.total_sessions ?? 0)
      statsCards.value[2].value = String(overview.alarm_count ?? 0)
    }

    const sessions = await sessionApi.getList({ page: 1, page_size: 5 }).catch(() => ({ sessions: [] }))
    // FIXED-2026-07-24: API 返回 {sessions:null} 时 sessions 是对象非数组，需 Array.isArray 兜底
const _sessions = sessions.sessions || sessions
recentSessions.value = Array.isArray(_sessions) ? _sessions : []

    const alarms = await alarmApi.getList({ page: 1, page_size: 5 }).catch(() => ({ alarms: [] }))
    // FIXED-2026-07-24: 同上，alarms 可能是 null 导致对象落入列表
const _alarms = alarms.alarms || alarms
recentAlarms.value = Array.isArray(_alarms) ? _alarms : []
  } catch (e) {
    // silent - 概览页数据加载失败不弹窗，避免页面初始加载时连续弹窗
    console.error('[Overview] fetchData error:', e)
  }
}

// FIXED: [WebSocket断连] 增加 token 鉴权 + 指数退避重连 + 手动关闭标志 [2026-07-17]
// WebSocket 实时推送：订阅报警与系统事件，避免纯轮询的延迟
function connectWebSocket() {
  if (manuallyClosed) return

  const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  const token = localStorage.getItem('jte_token') || ''
  // FIXED: [P0] WebSocket 连接必须携带 JWT token，否则后端返回 401 [2026-07-17]
  const wsUrl = `${protocol}//${window.location.host}/ws/v1/stream?token=${encodeURIComponent(token)}`

  try {
    ws = new WebSocket(wsUrl)
  } catch (e) {
    scheduleReconnect()
    return
  }

  ws.onopen = () => {
    wsReconnectCount = 0
    if (ws.readyState === WebSocket.OPEN) {
      ws.send(JSON.stringify({ action: 'subscribe', topics: ['alarm_event', 'system_event'] }))
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
        // 实时报警：插入到列表头部并更新计数
        const alarm = msg.data
        recentAlarms.value = [alarm, ...recentAlarms.value].slice(0, 5)
        const cur = parseInt(statsCards.value[2].value) || 0
        statsCards.value[2].value = String(cur + 1)
      } else if (msg.topic === 'system_event' && msg.data) {
        // 系统事件：根据类型刷新相关计数（会话上线/下线等）
        const evt = msg.data
        if (evt.type === 'session_connect' || evt.type === 'session_disconnect' ||
            evt.type === 'vehicle_login' || evt.type === 'vehicle_logout') {
          // 会话变动较频繁，节流刷新统计数据
          scheduleStatsRefresh()
        }
      }
    } catch (e) {
      // ignore parse errors
    }
  }

  ws.onclose = () => {
    if (manuallyClosed) return
    if (wsHeartbeatTimer) { clearInterval(wsHeartbeatTimer); wsHeartbeatTimer = null }
    scheduleReconnect()
  }

  ws.onerror = () => {
    if (ws) ws.close()
  }
}

function scheduleReconnect() {
  if (wsReconnectTimer) return
  // FIXED: [WebSocket断连] 指数退避重连（1s→2s→4s→...→30s） [2026-07-17]
  const delay = Math.min(1000 * Math.pow(2, wsReconnectCount), 30000)
  wsReconnectCount++
  wsReconnectTimer = setTimeout(() => {
    wsReconnectTimer = null
    connectWebSocket()
  }, delay)
}

let statsRefreshTimer = null
function scheduleStatsRefresh() {
  // 节流：合并 2 秒内的多次事件为一次刷新
  if (statsRefreshTimer) return
  statsRefreshTimer = setTimeout(() => {
    statsRefreshTimer = null
    refreshStatsOnly()
  }, 2000)
}

async function refreshStatsOnly() {
  const overview = await statsApi.getOverview().catch(() => null)
  if (overview) {
    statsCards.value[0].value = String(overview.online_count ?? 0)
    statsCards.value[1].value = String(overview.total_sessions ?? 0)
  }
}

function protocolTagType(proto) {
  const map = { jt808: '', jt809: 'success', jt1078: 'warning', jt1045: 'danger', jt905: 'info', gbt32960: '' }
  return map[proto] || 'info'
}

function statusLabel(status) {
  const map = { connected: '已连接', registered: '已注册', authenticated: '已认证' }
  return map[status] || status
}

function formatTime(t) {
  if (!t) return '-'
  return new Date(t).toLocaleString('zh-CN')
}

onMounted(() => {
  fetchData()
  // 轮询作为 WebSocket 的兜底：拉长到 30 秒，减少无谓请求
  timer = setInterval(fetchData, 30000)
  connectWebSocket()
})

onUnmounted(() => {
  manuallyClosed = true // FIXED: [WebSocket断连] 防止组件卸载后重连 [2026-07-17]
  if (timer) clearInterval(timer)
  if (statsRefreshTimer) clearTimeout(statsRefreshTimer)
  if (wsReconnectTimer) clearTimeout(wsReconnectTimer)
  if (wsHeartbeatTimer) clearInterval(wsHeartbeatTimer)
  if (ws) { ws.onclose = null; ws.close() }
})
</script>