<template>
  <div class="page-container">
    <div class="page-header">
      <h2>智能助手</h2>
      <div style="display: flex; gap: 8px;">
        <el-tag v-if="wsConnected" type="success" size="small">实时连接</el-tag>
        <el-tag v-else type="info" size="small">HTTP 模式</el-tag>
        <el-button size="small" @click="historyDrawer = true">
          <el-icon style="margin-right:4px"><Clock /></el-icon>历史记录
        </el-button>
        <el-button size="small" @click="clearHistory">清空历史</el-button>
      </div>
    </div>
    <el-card shadow="never" style="height:calc(100vh - 160px);display:flex;flex-direction:column">
      <div class="chat-messages" ref="messagesRef" style="flex:1;overflow-y:auto;padding:16px">
        <div v-for="msg in messages" :key="msg.id" :class="['chat-msg', msg.role]">
          <div class="msg-bubble">
            <!-- AUTO-FIX-2026-06-30 [P1-9]: 结构化表格展示 -->
            <template v-if="msg.contentType === 'table'">
              <div class="msg-result-text">{{ msg.content }}</div>
              <el-table
                :data="msg.tableData"
                size="small"
                border
                stripe
                :max-height="320"
                style="margin-top:8px;width:100%"
              >
                <el-table-column
                  v-for="col in msg.tableColumns"
                  :key="col.prop"
                  :prop="col.prop"
                  :label="col.label"
                  :min-width="col.minWidth || 110"
                  show-overflow-tooltip
                >
                  <template #default="{ row }">
                    <span>{{ formatCell(row[col.prop]) }}</span>
                  </template>
                </el-table-column>
              </el-table>
              <div v-if="msg.tableMeta" class="msg-table-meta">
                共 {{ msg.tableMeta.total }} 条 · 第 {{ msg.tableMeta.page }} 页 · 每页 {{ msg.tableMeta.size }} 条
              </div>
            </template>
            <!-- 计数类响应（online_count/alarm_count） -->
            <template v-else-if="msg.contentType === 'metric'">
              <div class="msg-result-text">{{ msg.content }}</div>
              <div class="metric-row">
                <div v-for="(v, k) in msg.metric" :key="k" class="metric-card">
                  <div class="metric-value">{{ formatNumber(v) }}</div>
                  <div class="metric-label">{{ metricLabel(k) }}</div>
                </div>
              </div>
            </template>
            <!-- JSON 展示 -->
            <pre v-else-if="msg.contentType === 'json'" class="msg-pre">{{ formatJson(msg.content) }}</pre>
            <!-- 纯文本 -->
            <span v-else>{{ msg.content }}</span>
          </div>
        </div>
        <div v-if="messages.length === 0" style="text-align:center;padding:40px;color:var(--jte-text-muted)">
          <el-icon :size="48"><ChatDotRound /></el-icon>
          <p style="margin-top:12px">我是 JTE 智能助手，可以帮您：</p>
          <div style="margin-top:16px;display:flex;flex-direction:column;gap:8px;align-items:center">
            <el-button v-for="p in quickPrompts" :key="p" size="small" @click="usePrompt(p)">{{ p }}</el-button>
          </div>
        </div>
        <div v-if="sending" class="chat-msg assistant">
          <div class="msg-bubble"><span class="typing">思考中...</span></div>
        </div>
      </div>
      <div style="display:flex;gap:8px;padding:16px;border-top:1px solid var(--jte-border)">
        <el-input v-model="input" placeholder="输入问题，例如：查询昨天超速的车辆" @keyup.enter="send" :disabled="sending" />
        <el-button type="primary" @click="send" :loading="sending">发送</el-button>
      </div>
    </el-card>

    <!-- AUTO-FIX-2026-06-30 [P1-9]: 历史对话记录抽屉 -->
    <el-drawer v-model="historyDrawer" title="历史对话记录" size="420px" direction="rtl">
      <div v-if="historyList.length === 0" style="text-align:center;color:var(--jte-text-muted);padding:24px">
        暂无历史记录
      </div>
      <el-timeline v-else>
        <el-timeline-item
          v-for="(h, idx) in historyList"
          :key="idx"
          :timestamp="h.time"
          :type="h.role === 'user' ? 'primary' : 'success'"
          placement="top"
        >
          <div class="history-role">{{ h.role === 'user' ? '我' : '助手' }}</div>
          <div class="history-content">{{ h.preview }}</div>
        </el-timeline-item>
      </el-timeline>
    </el-drawer>
  </div>
</template>
<script setup>
// AUTO-FIX-2026-06-26: 第六轮前端修复 - AI 助手增强（WebSocket 实时对话 + 历史记录）
// AUTO-FIX-2026-06-30 [P1-9]: NL2SQL 结果表格展示 + 历史对话抽屉 + 计数类指标卡片
import { ref, nextTick, onMounted, onUnmounted } from 'vue'
import { ChatDotRound, Clock } from '@element-plus/icons-vue'
import { aiApi, createAIChatSocket } from '../../api'

const STORAGE_KEY = 'jte_ai_chat_history'
const HISTORY_KEY = 'jte_ai_chat_history_list'
const MAX_HISTORY = 50

const input = ref('')
const sending = ref(false)
const messages = ref([])
const messagesRef = ref(null)
const wsConnected = ref(false)
const historyDrawer = ref(false)
const historyList = ref([])
let ws = null

const quickPrompts = [
  '查一下今天超速的车辆',
  '查询今天所有报警',
  '查询在线车辆数量',
  '查询当前在线车辆位置',
  '生成昨日行驶日报',
  '调试 808 注册流程',
]

function loadHistory() {
  try {
    const data = localStorage.getItem(STORAGE_KEY)
    if (data) {
      const parsed = JSON.parse(data)
      if (Array.isArray(parsed)) messages.value = parsed.slice(-MAX_HISTORY)
    }
    const hData = localStorage.getItem(HISTORY_KEY)
    if (hData) {
      const parsed = JSON.parse(hData)
      if (Array.isArray(parsed)) historyList.value = parsed.slice(-MAX_HISTORY * 2)
    }
  } catch (e) {
    console.warn('Load chat history failed:', e)
  }
}

function saveHistory() {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(messages.value.slice(-MAX_HISTORY)))
    localStorage.setItem(HISTORY_KEY, JSON.stringify(historyList.value.slice(-MAX_HISTORY * 2)))
  } catch (e) {
    console.warn('Save chat history failed:', e)
  }
}

function appendHistory(role, content, preview) {
  historyList.value.push({
    role,
    time: new Date().toLocaleString('zh-CN'),
    preview: preview || (typeof content === 'string' ? content.slice(0, 80) : String(content).slice(0, 80)),
  })
}

function clearHistory() {
  messages.value = []
  historyList.value = []
  localStorage.removeItem(STORAGE_KEY)
  localStorage.removeItem(HISTORY_KEY)
}

function usePrompt(text) {
  input.value = text
  send()
}

function detectContentType(text) {
  if (typeof text !== 'string') return 'json'
  const trimmed = text.trim()
  if ((trimmed.startsWith('{') && trimmed.endsWith('}')) ||
      (trimmed.startsWith('[') && trimmed.endsWith(']'))) {
    try { JSON.parse(trimmed); return 'json' } catch { /* not json */ }
  }
  return 'text'
}

function formatJson(text) {
  try { return JSON.stringify(JSON.parse(text), null, 2) } catch { return text }
}

// AUTO-FIX-2026-06-30 [P1-9]: 将 ChatResponse.data 解析为表格/指标/JSON
// data 可能形态：
//   1. ListResult: { items: [...], total, page, size }   → table
//   2. 数组（如 online_locations: []*LocationData）        → table（无分页元信息）
//   3. 计数对象: { online_count: N } / { alarm_count: N } → metric
//   4. 其他结构                                            → json
function parseStructuredData(resultText, data) {
  if (data == null) {
    return { contentType: 'text', content: resultText || '' }
  }

  // 形态 1: ListResult
  if (typeof data === 'object' && !Array.isArray(data) && Array.isArray(data.items)) {
    const items = data.items
    if (items.length === 0) {
      return { contentType: 'text', content: resultText || '未找到数据' }
    }
    const columns = extractColumns(items[0])
    if (columns.length === 0) {
      return { contentType: 'json', content: JSON.stringify(data) }
    }
    return {
      contentType: 'table',
      content: resultText || '',
      tableData: items,
      tableColumns: columns,
      tableMeta: {
        total: Number(data.total ?? items.length),
        page: Number(data.page ?? 1),
        size: Number(data.size ?? items.length),
      },
    }
  }

  // 形态 2: 数组
  if (Array.isArray(data)) {
    if (data.length === 0) {
      return { contentType: 'text', content: resultText || '未找到数据' }
    }
    const columns = extractColumns(data[0])
    if (columns.length === 0) {
      return { contentType: 'json', content: JSON.stringify(data) }
    }
    return {
      contentType: 'table',
      content: resultText || '',
      tableData: data,
      tableColumns: columns,
      tableMeta: null,
    }
  }

  // 形态 3: 计数对象（仅含数字字段）
  if (typeof data === 'object') {
    const entries = Object.entries(data)
    const numericEntries = entries.filter(([, v]) => typeof v === 'number')
    if (entries.length > 0 && numericEntries.length === entries.length) {
      return {
        contentType: 'metric',
        content: resultText || '',
        metric: data,
      }
    }
  }

  // 形态 4: 其他对象 → JSON
  return { contentType: 'json', content: JSON.stringify(data) }
}

// 从首行数据提取列定义（驼峰转中文标签 + 推荐列宽）
function extractColumns(rowObj) {
  if (rowObj == null || typeof rowObj !== 'object') return []
  const keys = Object.keys(rowObj).filter(k => {
    const v = rowObj[k]
    return v !== null && typeof v !== 'function'
  })
  return keys.map(k => ({
    prop: k,
    label: COLUMN_LABELS[k] || humanizeCamel(k),
    minWidth: COLUMN_WIDTHS[k] || 110,
  }))
}

const COLUMN_LABELS = {
  id: 'ID',
  phone: '手机号',
  plate_no: '车牌号',
  plate_color: '车牌颜色',
  protocol: '协议',
  terminal_id: '终端ID',
  online: '在线',
  registered_at: '注册时间',
  last_active: '最后活跃',
  vehicle_id: '车辆ID',
  latitude: '纬度',
  longitude: '经度',
  altitude: '海拔',
  speed: '速度(km/h)',
  direction: '方向',
  time: '时间',
  received_at: '接收时间',
  type: '类型',
  level: '级别',
  confidence: '置信度',
  ai_reason: 'AI原因',
  require_manual_review: '需人工复核',
  status: '状态',
  remote_addr: '远程地址',
  msg_type: '消息ID',
  msg_name: '消息名',
  direction_field: '方向',
  source: '来源',
  online_count: '在线数',
  alarm_count: '报警数',
}

const COLUMN_WIDTHS = {
  id: 180,
  phone: 130,
  terminal_id: 140,
  remote_addr: 140,
  registered_at: 160,
  last_active: 160,
  time: 160,
  received_at: 160,
  ai_reason: 180,
  type: 100,
  protocol: 100,
  status: 100,
  source: 100,
  latitude: 120,
  longitude: 120,
  speed: 110,
}

function humanizeCamel(s) {
  return s.replace(/_/g, ' ').replace(/\b\w/g, c => c.toUpperCase())
}

function formatCell(v) {
  if (v === null || v === undefined) return '-'
  if (typeof v === 'boolean') return v ? '是' : '否'
  if (typeof v === 'object') {
    try { return JSON.stringify(v) } catch { return String(v) }
  }
  // ISO 时间自动转可读格式
  if (typeof v === 'string' && /^\d{4}-\d{2}-\d{2}T/.test(v)) {
    const d = new Date(v)
    if (!isNaN(d.getTime())) return d.toLocaleString('zh-CN')
  }
  return String(v)
}

function formatNumber(v) {
  if (typeof v !== 'number') return String(v)
  return v.toLocaleString('zh-CN')
}

function metricLabel(k) {
  return COLUMN_LABELS[k] || humanizeCamel(k)
}

function connectWebSocket() {
  try {
    ws = createAIChatSocket()
    ws.onopen = () => { wsConnected.value = true }
    ws.onclose = () => { wsConnected.value = false }
    ws.onerror = () => { wsConnected.value = false }
    ws.onmessage = (event) => {
      try {
        const data = JSON.parse(event.data)
        if (data.type === 'token') {
          appendAssistantToken(data.content)
        } else if (data.type === 'done') {
          finalizeAssistantMessage()
        } else if (data.type === 'error') {
          finalizeAssistantMessage(data.message || '请求失败')
        }
      } catch (e) {
        console.error('WS message parse failed:', e)
      }
    }
  } catch (e) {
    console.warn('WebSocket connect failed, fallback to HTTP:', e)
    wsConnected.value = false
  }
}

let pendingAssistantId = null
let pendingContent = ''

function appendAssistantToken(token) {
  if (pendingAssistantId === null) {
    pendingAssistantId = Date.now() + 1
    pendingContent = token
    messages.value.push({ id: pendingAssistantId, role: 'assistant', content: pendingContent, contentType: 'text' })
  } else {
    pendingContent += token
    const msg = messages.value.find(m => m.id === pendingAssistantId)
    if (msg) {
      msg.content = pendingContent
      msg.contentType = detectContentType(pendingContent)
    }
  }
  scrollToEnd()
}

function finalizeAssistantMessage(fallbackContent) {
  if (fallbackContent && pendingContent === '') {
    const msg = messages.value.find(m => m.id === pendingAssistantId)
    if (msg) {
      msg.content = fallbackContent
      msg.contentType = detectContentType(fallbackContent)
    }
  }
  pendingAssistantId = null
  pendingContent = ''
  sending.value = false
  saveHistory()
}

function scrollToEnd() {
  nextTick(() => {
    if (messagesRef.value) messagesRef.value.scrollTop = messagesRef.value.scrollHeight
  })
}

async function send() {
  if (!input.value.trim() || sending.value) return
  const userText = input.value
  const userMsg = { id: Date.now(), role: 'user', content: userText, contentType: 'text' }
  messages.value.push(userMsg)
  appendHistory('user', userText)
  input.value = ''
  sending.value = true
  scrollToEnd()

  // 优先尝试 WebSocket 流式响应
  if (ws && wsConnected.value && ws.readyState === WebSocket.OPEN) {
    try {
      ws.send(JSON.stringify({ query: userText }))
      saveHistory()
      return
    } catch (e) {
      console.warn('WS send failed, fallback to HTTP:', e)
    }
  }

  // HTTP 回退
  try {
    const res = await aiApi.chat({ query: userText })
    let assistantMsg
    if (res && res.code === 0 && res.data) {
      // AUTO-FIX-2026-06-30 [P1-9]: 优先处理 ChatResponse 结构化数据
      // ChatResponse: { session_id, query, intent, result, data, time }
      if (typeof res.data === 'object' && (res.data.result !== undefined || res.data.intent)) {
        const parsed = parseStructuredData(res.data.result, res.data.data)
        assistantMsg = {
          id: Date.now() + 1,
          role: 'assistant',
          ...parsed,
        }
      } else if (typeof res.data === 'object' && res.data.response !== undefined) {
        // 兼容旧版返回 { response, model, session_id }
        const content = res.data.response || ''
        assistantMsg = {
          id: Date.now() + 1,
          role: 'assistant',
          content,
          contentType: detectContentType(content),
        }
      } else {
        const content = res.data.result || res.data.answer || res.data.content ||
          (typeof res.data === 'string' ? res.data : JSON.stringify(res.data))
        assistantMsg = {
          id: Date.now() + 1,
          role: 'assistant',
          content: typeof content === 'string' ? content : JSON.stringify(content),
          contentType: typeof content === 'string' ? detectContentType(content) : 'json',
        }
      }
    } else {
      assistantMsg = {
        id: Date.now() + 1,
        role: 'assistant',
        content: (res && res.message) || '暂时无法回答',
        contentType: 'text',
      }
    }
    messages.value.push(assistantMsg)
    appendHistory('assistant', assistantMsg.content, assistantMsg.tableMeta
      ? `${assistantMsg.content}（${assistantMsg.tableMeta.total} 条）`
      : undefined)
    saveHistory()
  } catch (e) {
    const errMsg = '请求失败：' + (e.message || '网络错误')
    messages.value.push({ id: Date.now() + 1, role: 'assistant', content: errMsg, contentType: 'text' })
    appendHistory('assistant', errMsg)
  } finally {
    sending.value = false
    scrollToEnd()
  }
}

onMounted(() => {
  loadHistory()
  connectWebSocket()
  scrollToEnd()
})

onUnmounted(() => {
  if (ws) {
    try { ws.close() } catch {}
  }
  saveHistory()
})
</script>
<style scoped>
.page-container { padding: 24px; }
.page-header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 20px; }
.page-header h2 { margin: 0; font-size: 18px; color: var(--jte-text); }
.chat-msg { margin-bottom: 12px; display: flex; }
.chat-msg.user { justify-content: flex-end; }
.chat-msg.assistant { justify-content: flex-start; }
.msg-bubble { max-width: 80%; padding: 10px 14px; border-radius: 12px; font-size: 14px; line-height: 1.5; word-break: break-word; }
.chat-msg.user .msg-bubble { background: var(--jte-accent); color: #fff; border-bottom-right-radius: 4px; }
.chat-msg.assistant .msg-bubble { background: var(--jte-surface-2); color: var(--jte-text); border-bottom-left-radius: 4px; }
.msg-pre { margin: 0; white-space: pre-wrap; font-family: 'Consolas', 'Monaco', monospace; font-size: 12px; }
.msg-result-text { font-weight: 500; margin-bottom: 4px; }
.msg-table-meta { margin-top: 6px; font-size: 12px; color: var(--jte-text-muted); text-align: right; }
.metric-row { display: flex; gap: 12px; margin-top: 8px; flex-wrap: wrap; }
.metric-card { background: var(--jte-surface); border: 1px solid var(--jte-border); border-radius: 8px; padding: 12px 16px; min-width: 120px; text-align: center; }
.metric-value { font-size: 22px; font-weight: 600; color: var(--jte-accent); }
.metric-label { font-size: 12px; color: var(--jte-text-muted); margin-top: 4px; }
.typing { color: var(--jte-text-muted); font-style: italic; }
.typing::after { content: '...'; animation: blink 1.2s infinite; }
@keyframes blink { 0%, 100% { opacity: 1; } 50% { opacity: 0.3; } }
.history-role { font-size: 12px; color: var(--jte-text-muted); margin-bottom: 4px; }
.history-content { font-size: 13px; color: var(--jte-text); word-break: break-word; }
</style>
