<template>
  <div class="page-container">
    <div class="page-header">
      <h2>{{ $t('common.nav.devices') }}</h2>
      <el-input v-model="searchPhone" :placeholder="$t('common.btn.search') + '手机号'" style="width: 240px" clearable @clear="fetchDevices" @keyup.enter="fetchDevices">
        <template #prefix><el-icon><Search /></el-icon></template>
      </el-input>
    </div>
    <el-table :data="devices" v-loading="loading" stripe>
      <el-table-column prop="phone" label="手机号" width="140" />
      <el-table-column prop="protocol" label="协议" width="100" />
      <el-table-column prop="plate_no" label="车牌号" width="140" />
      <el-table-column prop="terminal_id" label="终端ID" width="140" />
      <el-table-column prop="online" label="状态" width="80">
        <template #default="{ row }">
          <el-tag :type="row.online ? 'success' : 'info'" size="small">{{ row.online ? '在线' : '离线' }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="last_active" label="最后活跃" min-width="160" />
      <el-table-column label="操作" width="120" fixed="right">
        <template #default="{ row }">
          <el-button type="primary" link size="small" @click="sendCommand(row)">下发指令</el-button>
        </template>
      </el-table-column>
    </el-table>
    <div class="pagination-wrapper">
      <el-pagination v-model:current-page="page" v-model:page-size="pageSize" :total="total" layout="total, prev, pager, next" @current-change="fetchDevices" />
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { deviceApi } from '../api'
import { ElMessage } from 'element-plus'

const devices = ref([])
const loading = ref(false)
const searchPhone = ref('')
const page = ref(1)
const pageSize = ref(20)
const total = ref(0)

async function fetchDevices() {
  loading.value = true
  try {
    const res = await deviceApi.getList({ phone: searchPhone.value, page: page.value, page_size: pageSize.value })
if (res.code === 0 && res.data) {
// FIXED-2026-07-24: API 返回 items:null 时 fallback 到空数组，避免 reduce/forEach 崩溃
const items = res.data.items
devices.value = Array.isArray(items) ? items : (Array.isArray(res.data) ? res.data : [])
total.value = res.data.total || 0
}
  } catch (e) {
    console.error(e)
    ElMessage.error('加载设备列表失败，请检查网络或稍后重试')
  } finally {
    loading.value = false
  }
}

// FIXED: [功能缺失] sendCommand 从 console.log 桩改为路由跳转到指令下发页 [2026-07-17]
const router = useRouter()

function sendCommand(row) {
  router.push({ path: '/commands', query: { phone: row.phone } })
}

onMounted(fetchDevices)
</script>

<style scoped>
.page-container { padding: 24px; }
.page-header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 20px; }
.page-header h2 { margin: 0; font-size: 18px; color: var(--jte-text); }
.pagination-wrapper { display: flex; justify-content: flex-end; margin-top: 16px; }
</style>
