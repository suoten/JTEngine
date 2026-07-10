<template>
  <div>
    <div class="page-header">
      <div style="display: flex; align-items: center; justify-content: space-between;">
        <div>
          <h1 class="page-title">存储分层管理</h1>
          <p style="color: var(--jte-text-muted); font-size: 13px; margin-top: 4px;">
            关系库 / 时序库 / 缓存 / 对象存储 的使用情况、TTL 与归档任务
          </p>
        </div>
        <el-button size="small" @click="fetchAll">
          <el-icon><Refresh /></el-icon>
        </el-button>
      </div>
    </div>

    <div class="page-content">
      <!-- 存储统计卡片 -->
      <el-row :gutter="20" style="margin-bottom: 24px;">
        <el-col :span="6" v-for="s in storageCards" :key="s.key">
          <el-card class="stat-card" shadow="never">
            <div style="display: flex; align-items: flex-start; justify-content: space-between;">
              <div>
                <div class="stat-value" style="font-size: 24px;">{{ s.used }}</div>
                <div class="stat-label">{{ s.label }}</div>
              </div>
              <div class="stat-icon" :style="{ background: s.bgColor }">
                <el-icon :size="18" :style="{ color: s.color }">
                  <component :is="s.icon" />
                </el-icon>
              </div>
            </div>
            <div style="margin-top: 14px; display: flex; flex-direction: column; gap: 6px; font-size: 12px;">
              <div style="display: flex; justify-content: space-between;">
                <span style="color: var(--jte-text-muted);">容量</span>
                <span>{{ s.used }} / {{ s.total }}</span>
              </div>
              <div style="display: flex; justify-content: space-between;">
                <span style="color: var(--jte-text-muted);">写入 QPS</span>
                <span>{{ s.qps }}</span>
              </div>
              <div style="display: flex; justify-content: space-between;">
                <span style="color: var(--jte-text-muted);">命中率</span>
                <span style="color: var(--jte-success);">{{ s.hitRate }}</span>
              </div>
            </div>
          </el-card>
        </el-col>
      </el-row>

      <el-row :gutter="20">
        <!-- AUTO-FIX-2026-06-30 [P1-9]: TDengine 集群状态 -->
        <el-col :span="12" style="margin-bottom: 20px;">
          <el-card shadow="never" style="height: 100%;">
            <template #header>
              <span style="font-weight: 500; font-size: 14px;">TDengine 集群状态</span>
            </template>
            <el-descriptions :column="2" border size="small" v-loading="clusterLoading">
              <el-descriptions-item label="集群状态">
                <el-tag size="small" :type="cluster.ready ? 'success' : 'danger'">{{ cluster.ready ? '正常' : '异常' }}</el-tag>
              </el-descriptions-item>
              <el-descriptions-item label="副本数 (Replica)">{{ cluster.replica ?? '-' }}</el-descriptions-item>
              <el-descriptions-item label="VGroups">
                <span :class="{ 'quality-warn': cluster.vgroups_ready < cluster.vgroups_total }">
                  {{ cluster.vgroups_ready ?? 0 }} / {{ cluster.vgroups_total ?? 0 }}
                </span>
              </el-descriptions-item>
              <el-descriptions-item label="dNodes">
                <span :class="{ 'quality-warn': cluster.dnodes_online < cluster.dnodes_total }">
                  {{ cluster.dnodes_online ?? 0 }} / {{ cluster.dnodes_total ?? 0 }}
                </span>
              </el-descriptions-item>
              <el-descriptions-item label="数据库" :span="2">
                <span style="font-family: monospace; font-size: 12px;">{{ cluster.database || '-' }}</span>
              </el-descriptions-item>
            </el-descriptions>
            <div v-if="cluster.dnodes && cluster.dnodes.length" style="margin-top: 12px;">
              <div style="font-size:12px;color:var(--jte-text-muted);margin-bottom:6px;">数据节点</div>
              <div v-for="dn in cluster.dnodes" :key="dn.id || dn.host" class="dnode-row">
                <span class="dnode-dot" :class="{ online: dn.online }"></span>
                <span style="font-family:monospace;font-size:12px;">{{ dn.host }}</span>
                <span style="margin-left:auto;font-size:11px;color:var(--jte-text-muted);">{{ dn.status || (dn.online ? 'ready' : 'offline') }}</span>
              </div>
            </div>
            <div v-else-if="!clusterLoading" style="text-align:center;padding:16px;color:var(--jte-text-muted);font-size:12px;">
              集群状态接口未就绪，占位显示
            </div>
          </el-card>
        </el-col>

        <!-- AUTO-FIX-2026-06-30 [P1-9]: 冷热分层数据量监控 -->
        <el-col :span="12" style="margin-bottom: 20px;">
          <el-card shadow="never" style="height: 100%;">
            <template #header>
              <span style="font-weight: 500; font-size: 14px;">冷热分层数据量</span>
            </template>
            <el-row :gutter="12" style="margin-bottom: 12px;">
              <el-col :span="8">
                <div class="tier-stat"><div class="tier-value" style="color:#ef4444;">{{ tier.hotSize }}</div><div class="tier-label">热数据</div></div>
              </el-col>
              <el-col :span="8">
                <div class="tier-stat"><div class="tier-value" style="color:#3b82f6;">{{ tier.coldSize }}</div><div class="tier-label">冷数据</div></div>
              </el-col>
              <el-col :span="8">
                <div class="tier-stat"><div class="tier-value">{{ tier.totalSize }}</div><div class="tier-label">总量</div></div>
              </el-col>
            </el-row>
            <div ref="tierChartRef" style="width: 100%; height: 200px;"></div>
            <div v-if="!tierChartReady" style="text-align:center;padding:12px;color:var(--jte-text-muted);font-size:12px;">
              暂无分层统计数据
            </div>
          </el-card>
        </el-col>

        <!-- TTL 配置表 -->
        <el-col :span="14">
          <el-card shadow="never">
            <template #header>
              <span style="font-weight: 500; font-size: 14px;">TTL 保留期配置</span>
            </template>
            <el-table :data="ttlRows" style="width: 100%" size="small" v-loading="ttlLoading">
              <el-table-column prop="table" label="超级表" width="180">
                <template #default="{ row }">
                  <span style="font-family: monospace; font-size: 12px;">{{ row.table }}</span>
                </template>
              </el-table-column>
              <el-table-column prop="description" label="说明" min-width="160" />
              <el-table-column label="保留期" width="180">
                <template #default="{ row }">
                  <el-input-number
                    v-if="row.editing"
                    v-model="row.retention_days"
                    :min="1"
                    :step="1"
                    size="small"
                    controls-position="right"
                    style="width: 120px;"
                  />
                  <span v-else>{{ row.retention_days }} 天</span>
                </template>
              </el-table-column>
              <el-table-column label="操作" width="140" fixed="right">
                <template #default="{ row }">
                  <template v-if="row.editing">
                    <el-button type="primary" link size="small" @click="saveTtl(row)">保存</el-button>
                    <el-button link size="small" @click="cancelTtl(row)">取消</el-button>
                  </template>
                  <el-button v-else type="primary" link size="small" @click="editTtl(row)">编辑</el-button>
                </template>
              </el-table-column>
            </el-table>
          </el-card>
        </el-col>

        <!-- 归档任务状态 -->
        <el-col :span="10">
          <el-card shadow="never" style="margin-bottom: 20px;">
            <template #header>
              <div style="display: flex; align-items: center; justify-content: space-between;">
                <span style="font-weight: 500; font-size: 14px;">归档任务状态</span>
                <el-button type="primary" size="small" :loading="triggering" @click="triggerArchive">
                  手动触发
                </el-button>
              </div>
            </template>
            <el-descriptions :column="1" border size="small">
              <el-descriptions-item label="最近归档时间">{{ formatTime(archive.last_time) }}</el-descriptions-item>
              <el-descriptions-item label="归档状态">
                <el-tag size="small" :type="archiveStatusType(archive.status)">{{ archiveStatusLabel(archive.status) }}</el-tag>
              </el-descriptions-item>
              <el-descriptions-item label="归档进度">
                <el-progress :percentage="archive.progress || 0" :status="archive.progress >= 100 ? 'success' : ''" />
              </el-descriptions-item>
              <el-descriptions-item label="已归档记录">{{ archive.archived_count ?? 0 }} 条</el-descriptions-item>
              <el-descriptions-item label="下次计划">{{ formatTime(archive.next_schedule) }}</el-descriptions-item>
            </el-descriptions>
          </el-card>

          <!-- 缓存命中率图表 -->
          <el-card shadow="never">
            <template #header>
              <span style="font-weight: 500; font-size: 14px;">缓存命中率趋势</span>
            </template>
            <div ref="chartRef" style="width: 100%; height: 240px;"></div>
            <div v-if="!chartReady" style="text-align: center; padding: 20px; color: var(--jte-text-muted); font-size: 13px;">
              暂无数据
            </div>
          </el-card>
        </el-col>
      </el-row>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted, onUnmounted, nextTick } from 'vue'
import * as echarts from 'echarts'
import { ElMessage } from 'element-plus'
import { storageApi } from '../../api'

// 存储统计卡片：关系库 / 时序库 / 缓存 / 对象存储
const storageCards = ref([
  { key: 'relational', label: '关系库', icon: 'Coin', color: '#6366f1', bgColor: 'rgba(99,102,241,0.12)', used: '-', total: '-', qps: '-', hitRate: '-' },
  { key: 'timeseries', label: '时序库', icon: 'DataLine', color: '#3b82f6', bgColor: 'rgba(59,130,246,0.12)', used: '-', total: '-', qps: '-', hitRate: '-' },
  { key: 'cache', label: '缓存', icon: 'Lightning', color: '#f59e0b', bgColor: 'rgba(245,158,11,0.12)', used: '-', total: '-', qps: '-', hitRate: '-' },
  { key: 'object', label: '对象存储', icon: 'Files', color: '#22c55e', bgColor: 'rgba(34,197,94,0.12)', used: '-', total: '-', qps: '-', hitRate: '-' },
])

const ttlRows = ref([])
const ttlLoading = ref(false)
const archive = ref({})
const triggering = ref(false)

const chartRef = ref(null)
const chartReady = ref(false)
let chartInstance = null
let resizeHandler = null

// AUTO-FIX-2026-06-30 [P1-9]: TDengine 集群状态 + 冷热分层
const cluster = ref({})
const clusterLoading = ref(false)
const tier = ref({ hotSize: '-', coldSize: '-', totalSize: '-', hotRecords: 0, coldRecords: 0 })
const tierChartRef = ref(null)
const tierChartReady = ref(false)
let tierChartInstance = null

async function fetchStats() {
  try {
    const res = await storageApi.getStats()
    const data = res?.data || res || {}
    storageCards.value.forEach(card => {
      const item = data[card.key] || {}
      card.used = formatSize(item.used)
      card.total = formatSize(item.total)
      card.qps = item.qps ?? '-'
      card.hitRate = item.hit_rate != null ? (item.hit_rate * 100).toFixed(1) + '%' : '-'
    })
  } catch (e) {
    // 静默失败，保留占位
  }
}

async function fetchTtl() {
  ttlLoading.value = true
  try {
    const res = await storageApi.getTtl()
    const data = res?.data || res || []
    const list = Array.isArray(data) ? data : (data.items || [])
    // 兜底默认超级表，保证页面有内容
    const defaults = [
      { table: 'vehicle_location', description: '车辆位置心跳数据', retention_days: 30 },
      { table: 'vehicle_alarm', description: '终端报警事件', retention_days: 90 },
      { table: 'vehicle_can', description: 'CAN 总线数据', retention_days: 7 },
    ]
    ttlRows.value = (list.length ? list : defaults).map(r => ({
      table: r.table || r.name,
      description: r.description || '',
      retention_days: r.retention_days ?? r.days ?? 30,
      _origin: r.retention_days ?? r.days ?? 30,
      editing: false,
    }))
  } catch (e) {
    ttlRows.value = []
  } finally {
    ttlLoading.value = false
  }
}

function editTtl(row) {
  row._origin = row.retention_days
  row.editing = true
}

function cancelTtl(row) {
  row.retention_days = row._origin
  row.editing = false
}

async function saveTtl(row) {
  try {
    await storageApi.updateTtl({ table: row.table, retention_days: row.retention_days })
    row.editing = false
    row._origin = row.retention_days
    ElMessage.success(`${row.table} 保留期已更新为 ${row.retention_days} 天`)
  } catch (e) {
    ElMessage.error('TTL 更新失败')
  }
}

async function fetchArchive() {
  try {
    const res = await storageApi.getArchiveStatus()
    archive.value = res?.data || res || {}
  } catch (e) {
    archive.value = {}
  }
}

async function triggerArchive() {
  triggering.value = true
  try {
    await storageApi.triggerArchive({})
    ElMessage.success('归档任务已触发')
    await fetchArchive()
  } catch (e) {
    ElMessage.error('归档触发失败')
  } finally {
    triggering.value = false
  }
}

async function fetchCacheHitrate() {
  try {
    const res = await storageApi.getCacheHitrate({ points: 30 })
    const data = res?.data || res || {}
    renderChart(data)
  } catch (e) {
    renderChart(null)
  }
}

function renderChart(data) {
  if (!chartRef.value) return
  const times = data?.times || data?.timestamps || []
  const hitrates = data?.hit_rates || data?.hitrate || []
  if (!times.length) {
    chartReady.value = false
    return
  }
  chartReady.value = true
  if (!chartInstance) {
    chartInstance = echarts.init(chartRef.value)
  }
  chartInstance.setOption({
    tooltip: { trigger: 'axis' },
    grid: { left: 40, right: 20, top: 20, bottom: 30 },
    xAxis: {
      type: 'category',
      data: times,
      axisLabel: { fontSize: 10, color: 'var(--jte-text-muted)' },
      axisLine: { lineStyle: { color: 'var(--jte-border)' } },
    },
    yAxis: {
      type: 'value',
      max: 100,
      axisLabel: { fontSize: 10, color: 'var(--jte-text-muted)', formatter: '{value}%' },
      splitLine: { lineStyle: { color: 'var(--jte-border)' } },
    },
    series: [{
      name: '命中率',
      type: 'line',
      smooth: true,
      showSymbol: false,
      data: hitrates.map(v => (v != null ? Number((v * 100).toFixed(2)) : null)),
      areaStyle: { color: 'rgba(99,102,241,0.18)' },
      lineStyle: { color: '#6366f1', width: 2 },
    }],
  })
}

function formatSize(bytes) {
  if (bytes == null) return '-'
  const n = Number(bytes)
  if (isNaN(n)) return String(bytes)
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let i = 0
  let v = n
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i++ }
  return v.toFixed(1) + ' ' + units[i]
}

function formatTime(t) {
  if (!t) return '-'
  return new Date(t).toLocaleString('zh-CN')
}

function archiveStatusType(s) {
  return { running: 'warning', success: 'success', failed: 'danger', idle: 'info' }[s] || 'info'
}

function archiveStatusLabel(s) {
  return { running: '运行中', success: '成功', failed: '失败', idle: '空闲' }[s] || (s || '空闲')
}

// AUTO-FIX-2026-06-30 [P1-9]: TDengine 集群状态
async function fetchClusterStatus() {
  clusterLoading.value = true
  try {
    const res = await storageApi.getClusterStatus()
    const data = res?.data || res || {}
    cluster.value = {
      ready: data.ready ?? data.healthy ?? true,
      replica: data.replica ?? data.replica_count,
      vgroups_total: data.vgroups_total ?? data.vgroup_total ?? 0,
      vgroups_ready: data.vgroups_ready ?? data.vgroup_ready ?? 0,
      dnodes_total: data.dnodes_total ?? data.dnode_total ?? 0,
      dnodes_online: data.dnodes_online ?? data.dnode_online ?? 0,
      database: data.database || data.db_name || '',
      dnodes: data.dnodes || data.dnode_list || [],
    }
  } catch (e) {
    // 接口未就绪时给占位，避免页面空白
    cluster.value = { ready: false, replica: '-', vgroups_total: 0, vgroups_ready: 0, dnodes_total: 0, dnodes_online: 0, database: '', dnodes: [] }
  } finally {
    clusterLoading.value = false
  }
}

// AUTO-FIX-2026-06-30 [P1-9]: 冷热分层数据量
async function fetchTierStats() {
  try {
    const res = await storageApi.getTierStats()
    const data = res?.data || res || {}
    const hot = Number(data.hot_size ?? data.hot_bytes ?? 0)
    const cold = Number(data.cold_size ?? data.cold_bytes ?? 0)
    const total = hot + cold
    tier.value = {
      hotSize: formatSize(hot),
      coldSize: formatSize(cold),
      totalSize: formatSize(total),
      hotRecords: data.hot_records ?? 0,
      coldRecords: data.cold_records ?? 0,
      hotPct: total > 0 ? Math.round(hot / total * 100) : 0,
      coldPct: total > 0 ? Math.round(cold / total * 100) : 0,
    }
    renderTierChart(hot, cold)
  } catch (e) {
    renderTierChart(0, 0)
  }
}

function renderTierChart(hot, cold) {
  if (!tierChartRef.value) return
  if (!hot && !cold) {
    tierChartReady.value = false
    return
  }
  tierChartReady.value = true
  if (!tierChartInstance) {
    tierChartInstance = echarts.init(tierChartRef.value)
  }
  tierChartInstance.setOption({
    tooltip: { trigger: 'item', formatter: '{b}: {c} ({d}%)' },
    legend: { bottom: 0, icon: 'circle' },
    series: [{
      type: 'pie',
      radius: ['45%', '70%'],
      center: ['50%', '42%'],
      avoidLabelOverlap: true,
      itemStyle: { borderRadius: 6, borderColor: '#fff', borderWidth: 2 },
      label: { show: true, formatter: '{b}\n{d}%' },
      data: [
        { name: '热数据', value: hot, itemStyle: { color: '#ef4444' } },
        { name: '冷数据', value: cold, itemStyle: { color: '#3b82f6' } },
      ],
    }],
  }, true)
}

async function fetchAll() {
  await Promise.all([fetchStats(), fetchTtl(), fetchArchive(), fetchCacheHitrate(), fetchClusterStatus(), fetchTierStats()])
}

onMounted(async () => {
  await fetchAll()
  await nextTick()
  fetchCacheHitrate()
  resizeHandler = () => {
    if (chartInstance) chartInstance.resize()
    if (tierChartInstance) tierChartInstance.resize()
  }
  window.addEventListener('resize', resizeHandler)
})

onUnmounted(() => {
  if (resizeHandler) window.removeEventListener('resize', resizeHandler)
  if (chartInstance) { chartInstance.dispose(); chartInstance = null }
  if (tierChartInstance) { tierChartInstance.dispose(); tierChartInstance = null }
})
</script>

<style scoped>
/* AUTO-FIX-2026-06-30 [P1-9]: 集群节点与冷热分层样式 */
.dnode-row { display: flex; align-items: center; gap: 8px; padding: 4px 0; border-bottom: 1px dashed var(--jte-border); }
.dnode-row:last-child { border-bottom: none; }
.dnode-dot { width: 8px; height: 8px; border-radius: 50%; background: #c0c4cc; flex-shrink: 0; }
.dnode-dot.online { background: #67c23a; box-shadow: 0 0 4px rgba(103,194,58,0.6); }
.tier-stat { text-align: center; padding: 8px 0; background: var(--jte-surface-2, #f5f7fa); border-radius: 6px; }
.tier-value { font-size: 18px; font-weight: 700; }
.tier-label { font-size: 11px; color: var(--jte-text-muted); margin-top: 2px; }
.quality-warn { color: var(--jte-warning, #f59e0b); font-weight: 600; }
</style>
