<template>
  <div>
    <div class="page-header">
      <h1 class="page-title">协议日志</h1>
      <p style="color: var(--jte-text-muted); font-size: 13px; margin-top: 4px;">
        原始协议报文Hex查看与调试
      </p>
    </div>

    <div class="page-content">
      <el-card shadow="never">
        <template #header>
          <div style="display: flex; align-items: center; justify-content: space-between;">
            <span style="font-weight: 500; font-size: 14px;">报文记录</span>
            <div style="display: flex; gap: 8px;">
              <el-input v-model="searchPhone" :placeholder="$t('common.btn.search') + '手机号'" size="small" style="width: 180px;" clearable>
                <template #prefix><el-icon><Search /></el-icon></template>
              </el-input>
              <el-select v-model="filterProtocol" :placeholder="$t('device.protocol_filter')" size="small" style="width: 130px;" clearable>
                <el-option label="JT/T 808" value="jt808" />
                <el-option label="JT/T 809" value="jt809" />
                <el-option label="JT/T 1078" value="jt1078" />
                <el-option label="JT/T 1045" value="jt1045" />
                <el-option label="GB/T 32960" value="gbt32960" />
              </el-select>
              <el-select v-model="filterDirection" placeholder="方向" size="small" style="width: 100px;" clearable>
                <el-option label="上行" value="up" />
                <el-option label="下行" value="down" />
              </el-select>
              <el-switch v-model="autoRefresh" active-text="自动" size="small" />
              <el-button size="small" @click="fetchLogs">
                <el-icon><Refresh /></el-icon>
              </el-button>
            </div>
          </div>
        </template>

        <el-table :data="filteredLogs" style="width: 100%" size="small" v-loading="loading"
                  @row-click="showDetail" highlight-current-row>
          <el-table-column prop="received_at" label="时间" width="180">
            <template #default="{ row }">
              <span style="font-size: 12px;">{{ formatTime(row.received_at) }}</span>
            </template>
          </el-table-column>
          <el-table-column prop="direction" label="方向" width="70">
            <template #default="{ row }">
              <el-tag size="small" :type="row.direction === 'up' ? 'success' : 'warning'" style="font-size: 10px;">
                {{ row.direction === 'up' ? '↑上行' : '↓下行' }}
              </el-tag>
            </template>
          </el-table-column>
          <el-table-column prop="protocol" label="协议" width="100">
            <template #default="{ row }">
              <el-tag size="small" :type="protocolTagType(row.protocol)">{{ (row.protocol || '').toUpperCase() }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column prop="msg_type" label="消息类型" width="120">
            <template #default="{ row }">
              <span style="font-family: monospace; font-size: 12px;">0x{{ (row.msg_type || 0).toString(16).toUpperCase().padStart(4, '0') }}</span>
            </template>
          </el-table-column>
          <el-table-column prop="msg_name" label="消息名称" width="160">
            <template #default="{ row }">
              <span style="font-size: 12px;">{{ row.msg_name || '-' }}</span>
            </template>
          </el-table-column>
          <el-table-column prop="phone" label="手机号" width="130" />
          <el-table-column prop="raw_hex" label="原始报文" min-width="300">
            <template #default="{ row }">
              <span class="hex-text">{{ truncateHex(row.raw_hex) }}</span>
            </template>
          </el-table-column>
          <el-table-column prop="length" label="长度" width="70">
            <template #default="{ row }">
              <span style="font-size: 12px; color: var(--jte-text-muted);">{{ row.length || 0 }}B</span>
            </template>
          </el-table-column>
        </el-table>

        <div style="display: flex; justify-content: flex-end; margin-top: 16px;">
          <el-pagination
            v-model:current-page="currentPage"
            :page-size="pageSize"
            :total="totalLogs"
            layout="total, prev, pager, next"
            size="small"
            @current-change="fetchLogs"
          />
        </div>
      </el-card>

      <el-dialog v-model="detailVisible" title="报文详情" width="700px" :append-to-body="true">
        <div v-if="currentLog" class="log-detail">
          <el-descriptions :column="2" border size="small">
            <el-descriptions-item label="接收时间">{{ formatTime(currentLog.received_at) }}</el-descriptions-item>
            <el-descriptions-item label="方向">{{ currentLog.direction === 'up' ? '上行（终端→平台）' : '下行（平台→终端）' }}</el-descriptions-item>
            <el-descriptions-item label="协议">{{ (currentLog.protocol || '').toUpperCase() }}</el-descriptions-item>
            <el-descriptions-item label="消息类型">0x{{ (currentLog.msg_type || 0).toString(16).toUpperCase().padStart(4, '0') }}</el-descriptions-item>
            <el-descriptions-item label="消息名称">{{ currentLog.msg_name || '-' }}</el-descriptions-item>
            <el-descriptions-item label="手机号">{{ currentLog.phone || '-' }}</el-descriptions-item>
            <el-descriptions-item label="报文长度">{{ currentLog.length || 0 }} 字节</el-descriptions-item>
            <el-descriptions-item label="会话ID">{{ currentLog.session_id || '-' }}</el-descriptions-item>
          </el-descriptions>

          <div style="margin-top: 16px;">
            <div style="display: flex; align-items: center; justify-content: space-between; margin-bottom: 8px;">
              <span style="font-size: 13px; font-weight: 500;">原始报文 (Hex)</span>
              <el-button size="small" @click="copyHex">
                <el-icon><CopyDocument /></el-icon> 复制
              </el-button>
            </div>
            <div class="hex-detail-box">
              <div class="hex-row" v-for="(line, idx) in hexLines" :key="idx">
                <span class="hex-offset">{{ (idx * 16).toString(16).toUpperCase().padStart(8, '0') }}</span>
                <span class="hex-bytes">
                  <span v-for="(byte, bidx) in line" :key="bidx" class="hex-byte"
                        :class="{ 'hex-first': bidx === 0, 'hex-separator': bidx === 8 }">
                    {{ byte }}
                  </span>
                </span>
                <span class="hex-ascii">{{ lineToAscii(line) }}</span>
              </div>
            </div>
          </div>
        </div>
      </el-dialog>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted, watch } from 'vue'
import { logApi } from '../api'
import { ElMessage } from 'element-plus'

const logs = ref([])
const loading = ref(false)
const searchPhone = ref('')
const filterProtocol = ref('')
const filterDirection = ref('')
const autoRefresh = ref(true)
const currentPage = ref(1)
const pageSize = 50
const totalLogs = ref(0)
const detailVisible = ref(false)
const currentLog = ref(null)

const filteredLogs = computed(() => {
  return logs.value.filter(l => {
    if (searchPhone.value && !l.phone?.includes(searchPhone.value)) return false
    if (filterProtocol.value && l.protocol !== filterProtocol.value) return false
    if (filterDirection.value && l.direction !== filterDirection.value) return false
    return true
  })
})

const hexLines = computed(() => {
  if (!currentLog.value?.raw_hex) return []
  const hex = currentLog.value.raw_hex.replace(/\s/g, '')
  const lines = []
  for (let i = 0; i < hex.length; i += 32) {
    const chunk = hex.slice(i, i + 32)
    const bytes = []
    for (let j = 0; j < chunk.length; j += 2) {
      bytes.push(chunk.slice(j, j + 2).toUpperCase())
    }
    lines.push(bytes)
  }
  return lines
})

let timer = null

async function fetchLogs() {
  loading.value = true
  try {
    const data = await logApi.getList({
      page: currentPage.value,
      page_size: pageSize,
    })
    // FIXED-2026-07-24: API 返回 {logs:null} 时 data 是对象非数组，需 Array.isArray 兜底
const _raw = data.logs || data
logs.value = Array.isArray(_raw) ? _raw : []
    totalLogs.value = data.total || logs.value.length
  } catch (e) {
    logs.value = []
    ElMessage.error('加载协议日志失败，请检查网络或稍后重试')
  } finally {
    loading.value = false
  }
}

function showDetail(row) {
  currentLog.value = row
  detailVisible.value = true
}

function truncateHex(hex) {
  if (!hex) return '-'
  const clean = hex.replace(/\s/g, '').toUpperCase()
  if (clean.length > 48) return clean.slice(0, 48) + '...'
  return clean
}

function lineToAscii(bytes) {
  return bytes.map(b => {
    const code = parseInt(b, 16)
    return code >= 32 && code <= 126 ? String.fromCharCode(code) : '.'
  }).join('')
}

function copyHex() {
  if (!currentLog.value?.raw_hex) return
  navigator.clipboard.writeText(currentLog.value.raw_hex.replace(/\s/g, '').toUpperCase())
  ElMessage.success('已复制到剪贴板')
}

function protocolTagType(proto) {
  const map = { jt808: '', jt809: 'success', jt1078: 'warning', jt1045: 'danger', jt905: 'info', gbt32960: '' }
  return map[proto] || 'info'
}

function formatTime(t) {
  if (!t) return '-'
  return new Date(t).toLocaleString('zh-CN', { hour12: false })
}

watch(autoRefresh, (val) => {
  if (val) {
    timer = setInterval(fetchLogs, 5000)
  } else {
    if (timer) clearInterval(timer)
    timer = null
  }
})

onMounted(() => {
  fetchLogs()
  if (autoRefresh.value) {
    timer = setInterval(fetchLogs, 5000)
  }
})

onUnmounted(() => {
  if (timer) clearInterval(timer)
})
</script>

<style scoped>
.hex-text {
  font-family: 'JetBrains Mono', 'Fira Code', 'Consolas', monospace;
  font-size: 11px;
  color: var(--jte-accent);
  letter-spacing: 0.03em;
}

.hex-detail-box {
  background: var(--jte-bg);
  border: 1px solid var(--jte-border);
  border-radius: 8px;
  padding: 12px;
  max-height: 400px;
  overflow-y: auto;
  font-family: 'JetBrains Mono', 'Fira Code', 'Consolas', monospace;
  font-size: 12px;
}

.hex-row {
  display: flex;
  gap: 16px;
  line-height: 1.8;
}

.hex-offset {
  color: var(--jte-text-muted);
  min-width: 72px;
}

.hex-bytes {
  display: flex;
  gap: 4px;
  min-width: 340px;
}

.hex-byte {
  color: var(--jte-text);
}

.hex-separator {
  margin-left: 8px;
}

.hex-ascii {
  color: var(--jte-success);
  min-width: 120px;
}

.log-detail :deep(.el-descriptions__label) {
  font-size: 12px;
  color: var(--jte-text-muted);
}

.log-detail :deep(.el-descriptions__content) {
  font-size: 13px;
}
</style>