<template>
  <div>
    <div class="page-header">
      <h1 class="page-title">车辆管理</h1>
      <p style="color: var(--jte-text-muted); font-size: 13px; margin-top: 4px;">
        已注册终端车辆信息
      </p>
    </div>

    <div class="page-content">
      <el-card shadow="never">
        <template #header>
          <div style="display: flex; align-items: center; justify-content: space-between;">
            <span style="font-weight: 500; font-size: 14px;">车辆列表</span>
            <div style="display: flex; gap: 8px;">
              <el-input v-model="searchKeyword" placeholder="{{ $t('common.btn.search') }}手机号/车牌" size="small" style="width: 200px;" clearable>
                <template #prefix><el-icon><Search /></el-icon></template>
              </el-input>
              <el-button size="small" @click="fetchVehicles">
                <el-icon><Refresh /></el-icon>
              </el-button>
            </div>
          </div>
        </template>

        <el-table :data="filteredVehicles" style="width: 100%" size="small" v-loading="loading">
          <el-table-column prop="id" label="ID" width="200">
            <template #default="{ row }">
              <span style="font-family: monospace; font-size: 12px;">{{ row.id?.substring(0, 20) }}...</span>
            </template>
          </el-table-column>
          <el-table-column prop="phone" label="手机号" width="140" />
          <el-table-column prop="protocol" label="协议" width="110">
            <template #default="{ row }">
              <el-tag size="small" :type="protocolTagType(row.protocol)">{{ (row.protocol || '').toUpperCase() }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column prop="manufacturer" label="制造商" width="120" />
          <el-table-column prop="terminal_type" label="终端型号" width="140" />
          <el-table-column prop="online" label="在线状态" width="100">
            <template #default="{ row }">
              <el-tag size="small" :type="row.online ? 'success' : 'info'">
                {{ row.online ? '在线' : '离线' }}
              </el-tag>
            </template>
          </el-table-column>
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
import { vehicleApi } from '../api'

const vehicles = ref([])
const loading = ref(false)
const searchKeyword = ref('')

const filteredVehicles = computed(() => {
  if (!searchKeyword.value) return vehicles.value
  const kw = searchKeyword.value.toLowerCase()
  return vehicles.value.filter(v =>
    v.phone?.toLowerCase().includes(kw) ||
    v.manufacturer?.toLowerCase().includes(kw) ||
    v.terminal_type?.toLowerCase().includes(kw)
  )
})

async function fetchVehicles() {
  loading.value = true
  try {
    const data = await vehicleApi.getList({ limit: 100 })
    vehicles.value = data.vehicles || data || []
  } catch (e) {
    vehicles.value = []
  } finally {
    loading.value = false
  }
}

function protocolTagType(proto) {
  const map = { jt808: '', jt809: 'success', jt1078: 'warning', jt1045: 'danger', jt905: 'info' }
  return map[proto] || 'info'
}

function formatTime(t) {
  if (!t) return '-'
  return new Date(t).toLocaleString('zh-CN')
}

onMounted(fetchVehicles)
</script>