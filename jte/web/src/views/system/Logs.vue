<template>
  <div>
    <div class="page-header">
      <h1 class="page-title">系统日志</h1>
      <p style="color: var(--jte-text-muted); font-size: 13px; margin-top: 4px;">审计日志与操作记录</p>
    </div>
    <div class="page-content">
      <div style="display: flex; gap: 12px; margin-bottom: 16px; flex-wrap: wrap;">
        <el-input
          v-model="searchUser"
          placeholder="搜索用户"
          size="small"
          style="width: 200px;"
          clearable
          @keyup.enter="fetchLogs"
        />
        <el-select v-model="filterAction" size="small" style="width: 150px;" clearable placeholder="操作类型">
          <el-option label="登录" value="login" />
          <el-option label="登出" value="logout" />
          <el-option label="配置修改" value="config_update" />
          <el-option label="用户管理" value="user_manage" />
          <el-option label="角色管理" value="role_manage" />
          <el-option label="授权操作" value="license" />
          <el-option label="指令下发" value="command" />
          <el-option label="模块加载" value="module_load" />
        </el-select>
        <el-date-picker
          v-model="dateRange"
          type="daterange"
          size="small"
          range-separator="至"
          start-placeholder="开始日期"
          end-placeholder="结束日期"
          value-format="YYYY-MM-DD"
        />
        <el-button size="small" type="primary" @click="fetchLogs" :loading="loading">
          <el-icon><Refresh /></el-icon> 查询
        </el-button>
        <el-button size="small" @click="resetFilters">重置</el-button>
      </div>
      <el-table :data="logs" stripe v-loading="loading">
        <el-table-column prop="time" label="时间" width="180">
          <template #default="{ row }">{{ formatTime(row.time || row.created_at) }}</template>
        </el-table-column>
        <el-table-column prop="user" label="用户" width="120">
          <template #default="{ row }">{{ row.user || row.username || '-' }}</template>
        </el-table-column>
        <el-table-column prop="action" label="操作" width="150">
          <template #default="{ row }">
            <el-tag size="small" :type="actionTagType(row.action)">{{ actionLabel(row.action) }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="detail" label="详情" min-width="300">
          <template #default="{ row }">{{ row.detail || row.description || row.message || '-' }}</template>
        </el-table-column>
        <el-table-column prop="ip" label="IP地址" width="140">
          <template #default="{ row }">{{ row.ip || row.client_ip || '-' }}</template>
        </el-table-column>
        <el-table-column label="结果" width="80">
          <template #default="{ row }">
            <el-tag v-if="row.success === false || row.result === 'failed'" size="small" type="danger">失败</el-tag>
            <el-tag v-else size="small" type="success">成功</el-tag>
          </template>
        </el-table-column>
      </el-table>
      <div style="margin-top: 16px; display: flex; justify-content: flex-end;">
        <el-pagination
          v-model:current-page="page"
          v-model:page-size="pageSize"
          :total="total"
          :page-sizes="[20, 50, 100]"
          layout="total, sizes, prev, pager, next"
          @current-change="fetchLogs"
          @size-change="fetchLogs"
        />
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { Refresh } from '@element-plus/icons-vue'
import { systemApi } from '../../api'
import { ElMessage } from 'element-plus'

const searchUser = ref('')
const filterAction = ref('')
const dateRange = ref(null)
const logs = ref([])
const loading = ref(false)
const page = ref(1)
const pageSize = ref(20)
const total = ref(0)

const actionLabels = {
  login: '登录', logout: '登出', config_update: '配置修改',
  user_manage: '用户管理', role_manage: '角色管理', license: '授权操作',
  command: '指令下发', module_load: '模块加载',
}

function actionLabel(action) {
  return actionLabels[action] || action || '-'
}

function actionTagType(action) {
  const map = {
    login: 'success', logout: 'info', config_update: 'warning',
    user_manage: '', role_manage: '', license: 'danger',
    command: 'warning', module_load: '',
  }
  return map[action] || 'info'
}

function formatTime(t) {
  if (!t) return '-'
  return new Date(t).toLocaleString('zh-CN', { hour12: false })
}

async function fetchLogs() {
  loading.value = true
  try {
    const params = {
      page: page.value,
      page_size: pageSize.value,
    }
    if (searchUser.value) params.user = searchUser.value
    if (filterAction.value) params.action = filterAction.value
    if (dateRange.value && dateRange.value.length === 2) {
      params.start_date = dateRange.value[0]
      params.end_date = dateRange.value[1]
    }
    const res = await systemApi.getLogs(params)
    if (res.code === 0 && res.data) {
      const data = res.data
      logs.value = data.logs || data.items || data.list || []
      total.value = data.total || logs.value.length
    } else {
      logs.value = []
      total.value = 0
    }
  } catch (e) {
    console.error('Failed to fetch logs:', e)
    logs.value = []
    total.value = 0
    ElMessage.error('加载审计日志失败，请检查网络或稍后重试')
  } finally {
    loading.value = false
  }
}

function resetFilters() {
  searchUser.value = ''
  filterAction.value = ''
  dateRange.value = null
  page.value = 1
  fetchLogs()
}

onMounted(fetchLogs)
</script>
