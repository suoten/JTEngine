<template>
  <div class="taxi-page">
    <div class="page-header">
      <div>
        <h1 class="page-title">出租车营运监控（JT/T 905）</h1>
        <p class="header-desc">
          营运状态监控（空车/载客/电召/包车）、计价器数据展示、调度指令下发
        </p>
      </div>
      <div class="header-actions">
        <el-button @click="refreshAll" :icon="Refresh" :loading="loading">刷新</el-button>
        <el-button @click="showDispatchDialog = true" type="primary" :icon="Promotion">
          调度指令
        </el-button>
      </div>
    </div>

    <div class="page-content">
      <!-- 营运状态统计 -->
      <el-row :gutter="16" style="margin-bottom: 16px;">
        <el-col :xs="12" :sm="6">
          <el-card shadow="never" class="stat-card">
            <div class="stat-value">{{ fleetStats.total }}</div>
            <div class="stat-label">车辆总数</div>
          </el-card>
        </el-col>
        <el-col :xs="12" :sm="6">
          <el-card shadow="never" class="stat-card">
            <div class="stat-value" style="color: #22c55e;">{{ fleetStats.loaded }}</div>
            <div class="stat-label">载客中</div>
          </el-card>
        </el-col>
        <el-col :xs="12" :sm="6">
          <el-card shadow="never" class="stat-card">
            <div class="stat-value" style="color: #f59e0b;">{{ fleetStats.empty }}</div>
            <div class="stat-label">空车</div>
          </el-card>
        </el-col>
        <el-col :xs="12" :sm="6">
          <el-card shadow="never" class="stat-card">
            <div class="stat-value" style="color: var(--jte-accent);">
              {{ fleetStats.online }}
            </div>
            <div class="stat-label">在线车辆</div>
          </el-card>
        </el-col>
      </el-row>

      <!-- 筛选 -->
      <el-card shadow="never" style="margin-bottom: 16px;">
        <el-form :inline="true" :model="filter" class="filter-form">
          <el-form-item label="车牌号">
            <el-input
              v-model="filter.plate"
              placeholder="车牌号"
              clearable
              style="width: 140px;"
              @keyup.enter="fetchFleet"
            />
          </el-form-item>
          <el-form-item label="营运状态">
            <el-select v-model="filter.status" placeholder="全部" clearable style="width: 140px;">
              <el-option label="空车" value="empty" />
              <el-option label="载客" value="loaded" />
              <el-option label="电召" value="call" />
              <el-option label="包车" value="charter" />
              <el-option label="离线" value="offline" />
            </el-select>
          </el-form-item>
          <el-form-item label="终端号">
            <el-input
              v-model="filter.phone"
              placeholder="终端手机号"
              clearable
              style="width: 160px;"
              @keyup.enter="fetchFleet"
            />
          </el-form-item>
          <el-form-item>
            <el-button type="primary" @click="fetchFleet" :loading="loading" :icon="Search">
              查询
            </el-button>
            <el-button @click="resetFilter">重置</el-button>
          </el-form-item>
        </el-form>
      </el-card>

      <!-- 车辆列表 -->
      <el-card shadow="never">
        <template #header>
          <div class="card-header">
            <span>车队营运状态</span>
            <el-tag size="small" type="info">共 {{ fleet.length }} 辆</el-tag>
          </div>
        </template>
        <el-table
          :data="fleet"
          stripe
          size="small"
          v-loading="loading"
          @row-click="openVehicleDetail"
        >
          <el-table-column prop="plate" label="车牌号" min-width="100">
            <template #default="{ row }">{{ row.plate || row.vehicle_id || '-' }}</template>
          </el-table-column>
          <el-table-column prop="phone" label="终端号" min-width="120">
            <template #default="{ row }">{{ row.phone || row.device_id || '-' }}</template>
          </el-table-column>
          <el-table-column label="营运状态" width="100">
            <template #default="{ row }">
              <el-tag :type="operationalTagType(row)" size="small">
                {{ operationalLabel(row) }}
              </el-tag>
            </template>
          </el-table-column>
          <el-table-column label="在线状态" width="90">
            <template #default="{ row }">
              <span :class="['status-dot', isOnline(row) ? 'online' : 'offline']">
                {{ isOnline(row) ? '在线' : '离线' }}
              </span>
            </template>
          </el-table-column>
          <el-table-column label="计价器" width="100">
            <template #default="{ row }">
              <el-tag
                v-if="getMeter(row).enabled"
                :type="getMeter(row).state === '载客' ? 'success' : 'info'"
                size="small"
              >
                {{ getMeter(row).state }}
              </el-tag>
              <span v-else class="text-muted">-</span>
            </template>
          </el-table-column>
          <el-table-column label="单价(元/km)" width="110">
            <template #default="{ row }">
              {{ getMeter(row).unit_price?.toFixed(2) ?? '-' }}
            </template>
          </el-table-column>
          <el-table-column label="本班营收(元)" width="120">
            <template #default="{ row }">
              {{ getMeter(row).revenue?.toFixed(2) ?? '-' }}
            </template>
          </el-table-column>
          <el-table-column label="最后上报" min-width="160">
            <template #default="{ row }">{{ formatTime(row.last_report || row.received_at) }}</template>
          </el-table-column>
          <el-table-column label="操作" width="160" fixed="right">
            <template #default="{ row }">
              <el-button size="small" text @click.stop="openVehicleDetail(row)">详情</el-button>
              <el-button
                size="small"
                text
                type="primary"
                @click.stop="openDispatchForVehicle(row)"
              >
                调度
              </el-button>
            </template>
          </el-table-column>
        </el-table>

        <el-empty
          v-if="!loading && fleet.length === 0"
          description="暂无车辆数据，请确认已接入 JT/T 905 终端"
        />
      </el-card>
    </div>

    <!-- ======================== 车辆详情弹窗 ======================== -->
    <el-dialog v-model="detailVisible" title="车辆详情" width="640px">
      <div v-loading="detailLoading">
        <el-descriptions :column="2" border size="small" v-if="detailData">
          <el-descriptions-item label="车牌号">
            {{ detailData.plate || detailData.vehicle_id || '-' }}
          </el-descriptions-item>
          <el-descriptions-item label="终端号">
            {{ detailData.phone || detailData.device_id || '-' }}
          </el-descriptions-item>
          <el-descriptions-item label="驾驶员">
            {{ detailData.driver_name || '-' }}
          </el-descriptions-item>
          <el-descriptions-item label="所属公司">
            {{ detailData.company || '-' }}
          </el-descriptions-item>
          <el-descriptions-item label="营运状态">
            <el-tag :type="operationalTagType(detailData)" size="small">
              {{ operationalLabel(detailData) }}
            </el-tag>
          </el-descriptions-item>
          <el-descriptions-item label="在线状态">
            <span :class="['status-dot', isOnline(detailData) ? 'online' : 'offline']">
              {{ isOnline(detailData) ? '在线' : '离线' }}
            </span>
          </el-descriptions-item>
          <el-descriptions-item label="最后位置" :span="2">
            {{ detailData.latitude?.toFixed(6) ?? '-' }}, {{ detailData.longitude?.toFixed(6) ?? '-' }}
          </el-descriptions-item>
          <el-descriptions-item label="速度" :span="2">
            {{ detailData.speed?.toFixed(1) ?? '-' }} km/h
          </el-descriptions-item>
          <el-descriptions-item label="最后上报" :span="2">
            {{ formatTime(detailData.last_report || detailData.received_at) }}
          </el-descriptions-item>
        </el-descriptions>

        <!-- 计价器数据 -->
        <div class="detail-section">
          <h4>计价器数据</h4>
          <el-descriptions :column="2" border size="small" v-if="detailData">
            <el-descriptions-item label="计价器状态">
              <el-tag
                v-if="getMeter(detailData).enabled"
                :type="getMeter(detailData).state === '载客' ? 'success' : 'info'"
                size="small"
              >
                {{ getMeter(detailData).state }}
              </el-tag>
              <span v-else class="text-muted">未启用</span>
            </el-descriptions-item>
            <el-descriptions-item label="单价">
              {{ getMeter(detailData).unit_price?.toFixed(2) ?? '-' }} 元/km
            </el-descriptions-item>
            <el-descriptions-item label="当前里程">
              {{ getMeter(detailData).distance?.toFixed(2) ?? '-' }} km
            </el-descriptions-item>
            <el-descriptions-item label="当前金额">
              {{ getMeter(detailData).fare?.toFixed(2) ?? '-' }} 元
            </el-descriptions-item>
            <el-descriptions-item label="本班营收">
              {{ getMeter(detailData).revenue?.toFixed(2) ?? '-' }} 元
            </el-descriptions-item>
            <el-descriptions-item label="本班载客次数">
              {{ getMeter(detailData).trip_count ?? '-' }}
            </el-descriptions-item>
            <el-descriptions-item label="上车时间" :span="2">
              {{ formatTime(getMeter(detailData).pickup_time) }}
            </el-descriptions-item>
          </el-descriptions>
        </div>

        <!-- 最近调度记录 -->
        <div class="detail-section">
          <h4>最近调度记录</h4>
          <el-empty
            v-if="detailDispatches.length === 0"
            description="暂无调度记录"
            :image-size="60"
          />
          <el-timeline v-else>
            <el-timeline-item
              v-for="(d, i) in detailDispatches"
              :key="i"
              :timestamp="formatTime(d.time)"
              :type="d.result === 'success' ? 'success' : 'warning'"
            >
              <strong>{{ d.content }}</strong>
              <p v-if="d.result" class="text-muted">结果：{{ d.result }}</p>
            </el-timeline-item>
          </el-timeline>
        </div>
      </div>
      <template #footer>
        <el-button @click="detailVisible = false">关闭</el-button>
        <el-button
          v-if="detailData"
          type="primary"
          @click="openDispatchForVehicle(detailData)"
        >
          发送调度指令
        </el-button>
      </template>
    </el-dialog>

    <!-- ======================== 调度指令弹窗 ======================== -->
    <el-dialog v-model="showDispatchDialog" title="调度指令下发" width="500px">
      <el-form :model="dispatchForm" label-width="90px">
        <el-form-item label="目标终端" required>
          <el-input
            v-model="dispatchForm.phone"
            placeholder="终端手机号（点击列表/详情中的调度按钮自动填入）"
          />
        </el-form-item>
        <el-form-item label="指令类型" required>
          <el-select v-model="dispatchForm.cmd_type" style="width: 100%;">
            <el-option label="文本调度（0x8300 文本信息下发）" value="text" />
            <el-option label="电召派单（推荐上车地点）" value="call_assign" />
            <el-option label="包车任务" value="charter_task" />
            <el-option label="紧急调度（高优先级）" value="urgent" />
          </el-select>
        </el-form-item>
        <el-form-item v-if="dispatchForm.cmd_type === 'text'" label="文本内容" required>
          <el-input
            v-model="dispatchForm.content"
            type="textarea"
            :rows="3"
            placeholder="调度文本内容（最长 1024 字符）"
            maxlength="1024"
            show-word-limit
          />
        </el-form-item>
        <el-form-item v-if="dispatchForm.cmd_type === 'call_assign'" label="上车地点" required>
          <el-input v-model="dispatchForm.pickup_location" placeholder="如：北京西站北广场" />
        </el-form-item>
        <el-form-item v-if="dispatchForm.cmd_type === 'call_assign'" label="联系电话">
          <el-input v-model="dispatchForm.contact_phone" placeholder="乘客联系电话" />
        </el-form-item>
        <el-form-item v-if="dispatchForm.cmd_type === 'charter_task'" label="包车目的地" required>
          <el-input v-model="dispatchForm.destination" placeholder="如：首都机场 T3" />
        </el-form-item>
        <el-form-item v-if="dispatchForm.cmd_type === 'charter_task'" label="用车时间" required>
          <el-date-picker
            v-model="dispatchForm.departure_time"
            type="datetime"
            placeholder="选择用车时间"
            value-format="YYYYMMDDHHmmss"
            style="width: 100%;"
          />
        </el-form-item>
        <el-form-item v-if="dispatchForm.cmd_type === 'urgent'" label="紧急内容" required>
          <el-input
            v-model="dispatchForm.content"
            type="textarea"
            :rows="2"
            placeholder="紧急调度说明"
          />
        </el-form-item>
        <el-form-item label="有效期(min)">
          <el-input-number v-model="dispatchForm.ttl" :min="1" :max="1440" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="showDispatchDialog = false">取消</el-button>
        <el-button
          type="primary"
          @click="sendDispatch"
          :loading="dispatching"
        >
          发送
        </el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
// AUTO-FIX-2026-07-02: JT/T 905 出租车专用界面
// - 营运状态来源：车辆 additional.operational_status（empty/loaded/call/charter）
// - 计价器数据来源：车辆 additional.meter（state/unit_price/distance/fare/revenue/trip_count/pickup_time）
// - 调度指令通过 deviceApi.sendCommand（0x8103 文本指令 / 0x8300 广告下发）下发
// - 在线状态依据 last_report 时间戳（>5min 视为离线）
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { Refresh, Promotion, Search } from '@element-plus/icons-vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { taxiApi } from '../api'

const loading = ref(false)
const fleet = ref([])
const filter = ref({
  plate: '',
  status: '',
  phone: '',
})

// 详情弹窗
const detailVisible = ref(false)
const detailLoading = ref(false)
const detailData = ref(null)
const detailDispatches = ref([])

// 调度弹窗
const showDispatchDialog = ref(false)
const dispatching = ref(false)
const dispatchForm = ref({
  phone: '',
  cmd_type: 'text',
  content: '',
  pickup_location: '',
  contact_phone: '',
  destination: '',
  departure_time: '',
  ttl: 30,
})

let refreshTimer = null
const ONLINE_THRESHOLD_MS = 5 * 60 * 1000 // 5 分钟无上报视为离线

const fleetStats = computed(() => {
  const stats = { total: fleet.value.length, loaded: 0, empty: 0, call: 0, charter: 0, online: 0 }
  for (const v of fleet.value) {
    const status = getOperationalStatus(v)
    if (status === 'loaded') stats.loaded++
    else if (status === 'empty') stats.empty++
    else if (status === 'call') stats.call++
    else if (status === 'charter') stats.charter++
    if (isOnline(v)) stats.online++
  }
  return stats
})

function getAdditional(row) {
  if (!row) return {}
  // additional 字段可能是对象、JSON 字符串或 base64
  let add = row.additional
  if (!add) return {}
  if (typeof add === 'string') {
    try {
      // 尝试 base64 解码
      let decoded = add
      try { decoded = atob(add) } catch { /* not base64 */ }
      add = JSON.parse(decoded)
    } catch {
      return {}
    }
  }
  return add
}

function getOperationalStatus(row) {
  const add = getAdditional(row)
  const status = add.operational_status || add.operation_status || add.status
  if (!status) {
    // 无明确状态时根据计价器状态推断
    const meter = add.meter || {}
    if (meter.state === '载客' || meter.state === 'loaded') return 'loaded'
    if (meter.state === '空车' || meter.state === 'empty') return 'empty'
    return isOnline(row) ? 'empty' : 'offline'
  }
  return String(status).toLowerCase()
}

function operationalLabel(row) {
  const status = getOperationalStatus(row)
  return {
    empty: '空车',
    loaded: '载客',
    call: '电召',
    charter: '包车',
    offline: '离线',
  }[status] || '未知'
}

function operationalTagType(row) {
  const status = getOperationalStatus(row)
  return {
    empty: 'warning',
    loaded: 'success',
    call: 'primary',
    charter: 'info',
    offline: 'info',
  }[status] || 'info'
}

function isOnline(row) {
  if (!row) return false
  const ts = row.last_report || row.received_at || row.last_seen
  if (!ts) return false
  const t = new Date(ts).getTime()
  if (isNaN(t)) return false
  return Date.now() - t < ONLINE_THRESHOLD_MS
}

function getMeter(row) {
  const add = getAdditional(row)
  const meter = add.meter || {}
  if (!meter || Object.keys(meter).length === 0) {
    return { enabled: false, state: '', unit_price: null, distance: null, fare: null, revenue: null, trip_count: null, pickup_time: null }
  }
  return {
    enabled: true,
    state: meter.state || (meter.loaded ? '载客' : '空车'),
    unit_price: meter.unit_price ?? meter.price ?? null,
    distance: meter.distance ?? null,
    fare: meter.fare ?? meter.current_fare ?? null,
    revenue: meter.revenue ?? meter.total_fare ?? null,
    trip_count: meter.trip_count ?? null,
    pickup_time: meter.pickup_time ?? null,
  }
}

function formatTime(t) {
  if (!t) return '-'
  try {
    const d = new Date(t)
    if (isNaN(d.getTime())) return t
    return d.toLocaleString('zh-CN', { hour12: false })
  } catch { return t }
}

async function fetchFleet() {
  loading.value = true
  try {
    const params = { protocol: '905' }
    if (filter.value.plate) params.plate = filter.value.plate
    if (filter.value.phone) params.phone = filter.value.phone
    // 营运状态过滤在前端做（后端通用车辆接口不一定支持）
    const res = await taxiApi.getFleetStatus(params)
    let list = res.data?.items || res.items || res.data || []
    if (!Array.isArray(list)) list = []
    // 营运状态前端过滤
    if (filter.value.status) {
      list = list.filter(v => getOperationalStatus(v) === filter.value.status)
    }
    fleet.value = list
  } catch (e) {
    console.error('Fetch fleet failed:', e)
    ElMessage.error('车队状态获取失败')
  } finally {
    loading.value = false
  }
}

function resetFilter() {
  filter.value = { plate: '', status: '', phone: '' }
  fetchFleet()
}

async function openVehicleDetail(row) {
  detailVisible.value = true
  detailLoading.value = true
  detailData.value = row
  detailDispatches.value = []
  try {
    const id = row.id || row.vehicle_id || row.phone
    if (!id) return
    const res = await taxiApi.getVehicleDetail(id)
    const data = res.data || res
    if (data && typeof data === 'object') {
      detailData.value = { ...row, ...data }
    }
    // 调度记录可能附加在 detail 里
    detailDispatches.value = data?.dispatches || data?.recent_dispatches || []
  } catch (e) {
    console.error('Fetch vehicle detail failed:', e)
    // 详情失败时保留列表行数据，不报错打断用户
  } finally {
    detailLoading.value = false
  }
}

function openDispatchForVehicle(row) {
  dispatchForm.value = {
    phone: row.phone || row.device_id || '',
    cmd_type: 'text',
    content: '',
    pickup_location: '',
    contact_phone: '',
    destination: '',
    departure_time: '',
    ttl: 30,
  }
  showDispatchDialog.value = true
}

async function sendDispatch() {
  if (!dispatchForm.value.phone) {
    ElMessage.warning('请输入目标终端号')
    return
  }
  const cmd = dispatchForm.value
  // 构造调度指令内容（根据类型组合文本）
  let content = ''
  if (cmd.cmd_type === 'text' || cmd.cmd_type === 'urgent') {
    if (!cmd.content) {
      ElMessage.warning('请输入文本内容')
      return
    }
    content = cmd.content
  } else if (cmd.cmd_type === 'call_assign') {
    if (!cmd.pickup_location) {
      ElMessage.warning('请输入上车地点')
      return
    }
    content = `【电召派单】上车地点：${cmd.pickup_location}`
    if (cmd.contact_phone) content += `；乘客电话：${cmd.contact_phone}`
  } else if (cmd.cmd_type === 'charter_task') {
    if (!cmd.destination || !cmd.departure_time) {
      ElMessage.warning('请输入包车目的地和用车时间')
      return
    }
    content = `【包车任务】目的地：${cmd.destination}；用车时间：${cmd.departure_time}`
  }

  const priority = cmd.cmd_type === 'urgent' ? 1 : 0

  // 确认发送
  try {
    await ElMessageBox.confirm(
      `向终端 ${cmd.phone} 下发调度指令？\n内容：${content}`,
      '确认下发',
      { type: 'warning' }
    )
  } catch { return }

  dispatching.value = true
  try {
    // 通过 0x8103/0x8300 指令下发链路（deviceApi.sendCommand）
    await taxiApi.sendDispatch({
      phone: cmd.phone,
      command: '0x8300',
      content,
      priority,
      ttl: cmd.ttl,
    })
    ElMessage.success('调度指令已下发')
    showDispatchDialog.value = false
    // 刷新详情（如果开着）
    if (detailVisible.value && detailData.value && (detailData.value.phone === cmd.phone)) {
      openVehicleDetail(detailData.value)
    }
  } catch (e) {
    console.error('Send dispatch failed:', e)
    ElMessage.error('调度指令下发失败')
  } finally {
    dispatching.value = false
  }
}

function refreshAll() {
  fetchFleet()
}

onMounted(() => {
  fetchFleet()
  // 30 秒轮询车队状态（实时性 + 低开销平衡）
  refreshTimer = setInterval(fetchFleet, 30000)
})

onUnmounted(() => {
  if (refreshTimer) {
    clearInterval(refreshTimer)
    refreshTimer = null
  }
})
</script>

<style scoped>
.taxi-page { padding: 24px; }
.page-header {
  display: flex; justify-content: space-between; align-items: flex-start;
  margin-bottom: 20px; flex-wrap: wrap; gap: 12px;
}
.page-title { margin: 0; font-size: 20px; color: var(--jte-text); font-weight: 600; }
.header-desc {
  margin: 4px 0 0; font-size: 13px; color: var(--jte-text-muted);
  max-width: 600px;
}
.header-actions { display: flex; gap: 8px; flex-wrap: wrap; }
.page-content { display: flex; flex-direction: column; }
.stat-card { text-align: center; }
.stat-value { font-size: 28px; font-weight: 700; color: var(--jte-text); }
.stat-label { font-size: 12px; color: var(--jte-text-muted); margin-top: 4px; }
.filter-form { display: flex; flex-wrap: wrap; gap: 8px; }
.card-header {
  display: flex; justify-content: space-between; align-items: center;
  font-weight: 500;
}
.status-dot {
  display: inline-flex; align-items: center; gap: 4px;
  font-size: 12px;
}
.status-dot::before {
  content: ''; width: 8px; height: 8px; border-radius: 50%; display: inline-block;
}
.status-dot.online::before { background: #22c55e; }
.status-dot.offline::before { background: #9ca3af; }
.text-muted { color: var(--jte-text-muted); font-size: 12px; }
.detail-section { margin-top: 20px; }
.detail-section h4 {
  margin: 0 0 12px; font-size: 14px; color: var(--jte-text);
  font-weight: 600; border-left: 3px solid var(--jte-accent);
  padding-left: 8px;
}

/* 响应式：移动端适配 */
@media (max-width: 768px) {
  .taxi-page { padding: 12px; }
  .page-header { flex-direction: column; align-items: stretch; }
  .header-actions { width: 100%; }
  .header-actions .el-button { flex: 1; }
  .filter-form { width: 100%; }
  .filter-form .el-form-item { width: 100%; margin-right: 0; }
  .filter-form .el-form-item :deep(.el-input),
  .filter-form .el-form-item :deep(.el-select) { width: 100% !important; }
}
</style>
