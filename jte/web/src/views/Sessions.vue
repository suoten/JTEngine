<template>
  <div>
    <div class="page-header">
      <h1 class="page-title">会话管理</h1>
      <p style="color: var(--jte-text-muted); font-size: 13px; margin-top: 4px;">
        当前活跃终端连接会话
      </p>
    </div>

    <div class="page-content">
      <el-card shadow="never">
        <template #header>
          <div style="display: flex; align-items: center; justify-content: space-between;">
            <span style="font-weight: 500; font-size: 14px;">会话列表</span>
            <div style="display: flex; gap: 8px;">
              <el-input v-model="searchPhone" placeholder="{{ $t('common.btn.search') }}手机号" size="small" style="width: 200px;" clearable>
                <template #prefix><el-icon><Search /></el-icon></template>
              </el-input>
              <el-select v-model="filterProtocol" placeholder="{{ $t('device.protocol_filter') }}" size="small" style="width: 140px;" clearable>
                <el-option label="JT/T 808" value="jt808" />
                <el-option label="JT/T 809" value="jt809" />
                <el-option label="JT/T 1078" value="jt1078" />
                <el-option label="JT/T 1045" value="jt1045" />
                <el-option label="JT/T 905" value="jt905" />
                <el-option label="GB/T 32960" value="gbt32960" />
              </el-select>
              <el-button size="small" @click="fetchSessions">
                <el-icon><Refresh /></el-icon>
              </el-button>
            </div>
          </div>
        </template>

        <el-table :data="filteredSessions" style="width: 100%" size="small" v-loading="loading">
          <el-table-column prop="id" label="会话ID" width="240">
            <template #default="{ row }">
              <span style="font-family: monospace; font-size: 12px;">{{ row.id }}</span>
            </template>
          </el-table-column>
          <el-table-column prop="phone" label="终端手机号" width="140" />
          <el-table-column prop="protocol" label="协议" width="120">
            <template #default="{ row }">
              <el-tag size="small" :type="protocolTagType(row.protocol)">{{ (row.protocol || '').toUpperCase() }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column prop="status" label="状态" width="110">
            <template #default="{ row }">
              <div style="display: flex; align-items: center; gap: 6px;">
                <div :class="row.status === 'authenticated' ? 'live-indicator' : ''"
                     :style="row.status !== 'authenticated' ? { width: '8px', height: '8px', borderRadius: '50%', background: 'var(--jte-text-muted)' } : {}">
                </div>
                <span style="font-size: 12px;">{{ statusLabel(row.status) }}</span>
              </div>
            </template>
          </el-table-column>
          <el-table-column prop="remote_addr" label="远程地址" width="180" />
          <el-table-column prop="last_active" label="最后活跃" min-width="180">
            <template #default="{ row }">
              {{ formatTime(row.last_active) }}
            </template>
          </el-table-column>
        </el-table>
      </el-card>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted } from 'vue'
import { sessionApi } from '../api'

const sessions = ref([])
const loading = ref(false)
const searchPhone = ref('')
const filterProtocol = ref('')

const filteredSessions = computed(() => {
  return sessions.value.filter(s => {
    if (searchPhone.value && !s.phone?.includes(searchPhone.value)) return false
    if (filterProtocol.value && s.protocol !== filterProtocol.value) return false
    return true
  })
})

async function fetchSessions() {
  loading.value = true
  try {
    const data = await sessionApi.getList({ limit: 100 })
    sessions.value = data.sessions || data || []
  } catch (e) {
    sessions.value = []
  } finally {
    loading.value = false
  }
}

function protocolTagType(proto) {
  const map = { jt808: '', jt809: 'success', jt1078: 'warning', jt1045: 'danger', jt905: 'info' }
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

onMounted(fetchSessions)
</script>