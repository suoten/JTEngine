<template>
  <div class="archive-page">
    <div class="page-header">
      <div>
        <h1 class="page-title">归档数据查询</h1>
        <p class="header-desc">
          按设备+时间段查询历史轨迹，自动联合归档数据（MinIO）与实时数据（TDengine），
          结果按来源标记
        </p>
      </div>
      <div class="header-actions">
        <el-button @click="fetchArchiveProgress" :icon="Refresh" :loading="progressLoading">
          刷新任务状态
        </el-button>
        <el-button
          type="primary"
          @click="triggerArchive"
          :icon="Promotion"
          :loading="triggering"
          :disabled="!archiveProgress.archiver_loaded"
        >
          手动触发归档
        </el-button>
      </div>
    </div>

    <div class="page-content">
      <!-- 归档任务状态面板 -->
      <el-card shadow="never" class="progress-card">
        <template #header>
          <div class="card-header">
            <span>归档任务状态</span>
            <el-tag
              :type="archiveProgress.running ? 'success' : 'info'"
              size="small"
            >
              {{ archiveProgress.running ? '运行中' : '空闲' }}
            </el-tag>
          </div>
        </template>
        <el-row :gutter="16">
          <el-col :xs="12" :sm="6">
            <div class="metric">
              <div class="metric-label">归档模块</div>
              <div class="metric-value">
                <el-tag :type="archiveProgress.archiver_loaded ? 'success' : 'danger'" size="small">
                  {{ archiveProgress.archiver_loaded ? '已加载' : '未加载' }}
                </el-tag>
              </div>
            </div>
          </el-col>
          <el-col :xs="12" :sm="6">
            <div class="metric">
              <div class="metric-label">运行状态</div>
              <div class="metric-value">
                {{ archiveProgress.running ? '执行中' : '空闲' }}
              </div>
            </div>
          </el-col>
          <el-col :xs="12" :sm="6">
            <div class="metric">
              <div class="metric-label">进度</div>
              <div class="metric-value">
                <el-progress
                  v-if="archiveProgress.progress != null"
                  :percentage="Math.round((archiveProgress.progress || 0) * 100)"
                  :status="archiveProgress.last_result === 'failed' ? 'exception' : 'success'"
                  style="width: 120px;"
                />
                <span v-else>-</span>
              </div>
            </div>
          </el-col>
          <el-col :xs="12" :sm="6">
            <div class="metric">
              <div class="metric-label">上次结果</div>
              <div class="metric-value">
                <el-tag
                  v-if="archiveProgress.last_result"
                  :type="resultTagType(archiveProgress.last_result)"
                  size="small"
                >
                  {{ archiveProgress.last_result }}
                </el-tag>
                <span v-else>-</span>
              </div>
            </div>
          </el-col>
        </el-row>
      </el-card>

      <!-- 查询表单 -->
      <el-card shadow="never" style="margin-bottom: 16px;">
        <el-form :inline="true" :model="queryForm" class="query-form">
          <el-form-item label="终端号">
            <el-input
              v-model="queryForm.phone"
              placeholder="输入终端手机号/设备ID"
              clearable
              style="width: 220px;"
              @keyup.enter="queryTrack"
            />
          </el-form-item>
          <el-form-item label="时间段">
            <el-date-picker
              v-model="queryForm.time_range"
              type="datetimerange"
              range-separator="至"
              start-placeholder="开始时间"
              end-placeholder="结束时间"
              value-format="YYYY-MM-DDTHH:mm:ss"
              style="width: 360px;"
            />
          </el-form-item>
          <el-form-item>
            <el-button
              type="primary"
              @click="queryTrack"
              :loading="queryLoading"
              :icon="Search"
            >
              查询
            </el-button>
            <el-button @click="resetQuery">重置</el-button>
          </el-form-item>
        </el-form>
      </el-card>

      <!-- 来源统计 -->
      <el-row v-if="trackPoints.length > 0" :gutter="16" style="margin-bottom: 16px;">
        <el-col :xs="24" :sm="8">
          <el-card shadow="never" class="stat-card">
            <div class="stat-value">{{ trackPoints.length }}</div>
            <div class="stat-label">总点数</div>
          </el-card>
        </el-col>
        <el-col :xs="12" :sm="8">
          <el-card shadow="never" class="stat-card">
            <div class="stat-value" style="color: var(--jte-accent);">
              {{ realtimeCount }}
            </div>
            <div class="stat-label">实时数据点</div>
          </el-card>
        </el-col>
        <el-col :xs="12" :sm="8">
          <el-card shadow="never" class="stat-card">
            <div class="stat-value" style="color: var(--jte-warning, #f59e0b);">
              {{ archiveCount }}
            </div>
            <div class="stat-label">归档数据点</div>
          </el-card>
        </el-col>
      </el-row>

      <!-- 地图轨迹 -->
      <el-card shadow="never" style="margin-bottom: 16px;">
        <template #header>
          <div class="card-header">
            <span>轨迹地图（蓝=实时 · 橙=归档）</span>
            <div v-if="trackPoints.length > 0" class="map-controls">
              <el-button size="small" @click="playTrack" :disabled="isPlaying" :icon="VideoPlay">
                播放
              </el-button>
              <el-button size="small" @click="pauseTrack" :disabled="!isPlaying" :icon="VideoPause">
                暂停
              </el-button>
              <el-slider v-model="playSpeed" :min="1" :max="20" :step="1" style="width: 120px;" />
              <span class="speed-label">{{ playSpeed }}x</span>
              <span class="point-label">
                {{ currentPointIndex + 1 }} / {{ trackPoints.length }}
              </span>
            </div>
          </div>
        </template>
        <div
          ref="mapContainer"
          class="map-container"
        >
          <div v-if="!mapReady" class="map-placeholder">
            <el-icon :size="48"><Location /></el-icon>
            <p>地图加载中...</p>
          </div>
        </div>
      </el-card>

      <!-- 轨迹数据表 -->
      <el-card shadow="never">
        <template #header>
          <div class="card-header">
            <span>轨迹数据明细</span>
            <el-tag v-if="trackPoints.length > 0" size="small" type="info">
              共 {{ trackPoints.length }} 条
            </el-tag>
          </div>
        </template>
        <el-table
          :data="trackPoints"
          stripe
          size="small"
          max-height="400"
          v-loading="queryLoading"
          @row-click="jumpToPoint"
        >
          <el-table-column label="#" width="60" type="index" />
          <el-table-column prop="received_at" label="时间" min-width="160">
            <template #default="{ row }">{{ formatTime(row.received_at) }}</template>
          </el-table-column>
          <el-table-column prop="latitude" label="纬度" width="120">
            <template #default="{ row }">{{ row.latitude?.toFixed(6) ?? '-' }}</template>
          </el-table-column>
          <el-table-column prop="longitude" label="经度" width="120">
            <template #default="{ row }">{{ row.longitude?.toFixed(6) ?? '-' }}</template>
          </el-table-column>
          <el-table-column prop="speed" label="速度(km/h)" width="110">
            <template #default="{ row }">{{ row.speed != null ? row.speed.toFixed(1) : '-' }}</template>
          </el-table-column>
          <el-table-column prop="direction" label="方向" width="80">
            <template #default="{ row }">{{ row.direction ?? '-' }}°</template>
          </el-table-column>
          <el-table-column label="数据来源" width="100" fixed="right">
            <template #default="{ row }">
              <el-tag
                :type="getSourceType(row) === 'archive' ? 'warning' : 'primary'"
                size="small"
              >
                {{ getSourceType(row) === 'archive' ? '归档' : '实时' }}
              </el-tag>
            </template>
          </el-table-column>
        </el-table>
        <el-empty
          v-if="!queryLoading && trackPoints.length === 0"
          description="输入终端号和时间段查询轨迹，归档数据将自动联合展示"
        />
      </el-card>
    </div>
  </div>
</template>

<script setup>
// AUTO-FIX-2026-07-02: 归档数据查询界面
// - 按设备+时间段查询轨迹，后端 /tracks 接口已实现归档 fallback
//   归档数据会标记 Source="archive" 字段
// - 归档任务状态监控：GET /storage/archive/progress 返回
//   {archiver_loaded, running, progress, last_result}
// - 手动触发归档：POST /storage/archive/trigger
import { ref, computed, onMounted, onUnmounted } from 'vue'
import {
  Refresh, Promotion, Search, Location,
  VideoPlay, VideoPause,
} from '@element-plus/icons-vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { trackApi, storageApi, configApi } from '../api'

const queryForm = ref({
  phone: '',
  time_range: [],
})
const queryLoading = ref(false)
const trackPoints = ref([])

// 归档任务状态
const archiveProgress = ref({
  archiver_loaded: false,
  running: false,
  progress: null,
  last_result: '',
})
const progressLoading = ref(false)
const triggering = ref(false)

// 地图
const mapContainer = ref(null)
const mapReady = ref(false)
let mapInstance = null
let realtimePolyline = null
let archivePolyline = null
let movingMarker = null

// 播放
const isPlaying = ref(false)
const playSpeed = ref(5)
const currentPointIndex = ref(0)
let playTimer = null

const realtimeCount = computed(() =>
  trackPoints.value.filter(p => getSourceType(p) !== 'archive').length
)
const archiveCount = computed(() =>
  trackPoints.value.filter(p => getSourceType(p) === 'archive').length
)

function getSourceType(row) {
  // 后端归档数据标记 Source="archive"；实时数据可能无 Source 或 Source="realtime"
  if (!row) return 'realtime'
  const src = row.source || row.Source
  if (src && String(src).toLowerCase() === 'archive') return 'archive'
  return 'realtime'
}

function resultTagType(result) {
  if (!result) return 'info'
  const r = String(result).toLowerCase()
  if (r === 'success' || r === 'ok' || r === 'completed') return 'success'
  if (r === 'failed' || r === 'error') return 'danger'
  if (r === 'running') return 'warning'
  return 'info'
}

function formatTime(t) {
  if (!t) return '-'
  try {
    const d = new Date(t)
    if (isNaN(d.getTime())) return t
    return d.toLocaleString('zh-CN', { hour12: false })
  } catch { return t }
}

// ============ 归档任务状态 ============
async function fetchArchiveProgress() {
  progressLoading.value = true
  try {
    const res = await storageApi.getArchiveProgress()
    // 兼容 {code, data} 包装和裸对象
    const data = res.data || res
    archiveProgress.value = {
      archiver_loaded: !!data.archiver_loaded,
      running: !!data.running,
      progress: data.progress ?? null,
      last_result: data.last_result || data.LastResult || '',
    }
  } catch (e) {
    console.error('Fetch archive progress failed:', e)
    ElMessage.error('归档任务状态获取失败')
  } finally {
    progressLoading.value = false
  }
}

async function triggerArchive() {
  if (!archiveProgress.value.archiver_loaded) {
    ElMessage.warning('归档模块未加载，无法触发')
    return
  }
  if (archiveProgress.value.running) {
    ElMessage.warning('归档任务正在运行中，请等待完成')
    return
  }
  try {
    await ElMessageBox.confirm(
      '手动触发归档任务？将立即执行历史轨迹归档（默认每天凌晨 2 点自动执行）。',
      '确认',
      { type: 'warning' }
    )
  } catch { return }

  triggering.value = true
  try {
    await storageApi.triggerArchive({})
    ElMessage.success('归档任务已触发，正在后台执行')
    // 触发后立即刷新一次状态，并启动轮询
    fetchArchiveProgress()
    startProgressPolling()
  } catch (e) {
    console.error('Trigger archive failed:', e)
    ElMessage.error('归档任务触发失败')
  } finally {
    triggering.value = false
  }
}

let progressPollTimer = null
function startProgressPolling() {
  if (progressPollTimer) return
  progressPollTimer = setInterval(async () => {
    await fetchArchiveProgress()
    // 任务完成后停止轮询
    if (!archiveProgress.value.running) {
      stopProgressPolling()
    }
  }, 5000)
}
function stopProgressPolling() {
  if (progressPollTimer) {
    clearInterval(progressPollTimer)
    progressPollTimer = null
  }
}

// ============ 轨迹查询 ============
async function queryTrack() {
  if (!queryForm.value.phone) {
    ElMessage.warning('请输入终端号')
    return
  }
  pauseTrack()
  queryLoading.value = true
  try {
    const params = { phone: queryForm.value.phone }
    if (queryForm.value.time_range && queryForm.value.time_range.length === 2) {
      params.start_time = queryForm.value.time_range[0]
      params.end_time = queryForm.value.time_range[1]
    }
    const res = await trackApi.getTrack(params)
    // 后端统一返回 {track: [...], total: N}（无 code/data 包装）
    let list = res.track || res.data?.items || res.data?.track || []
    if (!Array.isArray(list)) list = []
    // 按时间升序排序（归档+实时联合需保证时间顺序）
    list.sort((a, b) => {
      const ta = new Date(a.received_at).getTime() || 0
      const tb = new Date(b.received_at).getTime() || 0
      return ta - tb
    })
    trackPoints.value = list
    if (list.length > 0) {
      await drawTrack()
      ElMessage.success(
        `查询到 ${list.length} 个轨迹点（实时 ${realtimeCount.value} · 归档 ${archiveCount.value}）`
      )
    } else {
      ElMessage.info('未查询到轨迹数据')
    }
  } catch (e) {
    console.error('Query track failed:', e)
    ElMessage.error('轨迹查询失败')
  } finally {
    queryLoading.value = false
  }
}

function resetQuery() {
  queryForm.value = { phone: '', time_range: [] }
  trackPoints.value = []
  clearTrack()
}

// ============ 地图 ============
async function initMap() {
  if (!mapContainer.value) return

  let mapConfig = { provider: 'tianditu', tianditu_key: '', amap_key: '', baidu_key: '' }
  try {
    mapConfig = await configApi.getMapConfig()
  } catch { /* 默认值 */ }

  try {
    if (window.AMap) {
      mapInstance = {
        type: 'amap',
        map: new window.AMap.Map(mapContainer.value, { zoom: 6, center: [116.40, 39.90] }),
      }
      mapReady.value = true
      return
    }
  } catch { /* ignore */ }

  try {
    if (window.T) {
      const map = new window.T.Map(mapContainer.value)
      map.centerAndZoom(new window.T.LngLat(116.40, 39.90), 6)
      mapInstance = { type: 'tianditu', map }
      mapReady.value = true
      return
    }
  } catch { /* ignore */ }

  const provider = mapConfig.provider || 'tianditu'
  const script = document.createElement('script')
  if (provider === 'tianditu' && mapConfig.tianditu_key) {
    script.src = `https://api.tianditu.gov.cn/api?v=4.0&tk=${mapConfig.tianditu_key}`
  } else if (provider === 'amap' && mapConfig.amap_key) {
    script.src = `https://webapi.amap.com/maps?v=2.0&key=${mapConfig.amap_key}`
  } else if (provider === 'baidu' && mapConfig.baidu_key) {
    script.src = `https://api.map.baidu.com/api?v=3.0&ak=${mapConfig.baidu_key}&callback=_baiduMapInit`
  } else {
    mapReady.value = true
    return
  }
  script.onload = () => {
    if (window.T) {
      const map = new window.T.Map(mapContainer.value)
      map.centerAndZoom(new window.T.LngLat(116.40, 39.90), 6)
      mapInstance = { type: 'tianditu', map }
    } else if (window.AMap) {
      mapInstance = {
        type: 'amap',
        map: new window.AMap.Map(mapContainer.value, { zoom: 6, center: [116.40, 39.90] }),
      }
    }
    mapReady.value = true
  }
  document.head.appendChild(script)
}

async function drawTrack() {
  // 等待地图就绪
  let tries = 0
  while (!mapReady.value && tries < 20) {
    await new Promise(r => setTimeout(r, 100))
    tries++
  }
  if (!mapInstance || trackPoints.value.length === 0) return

  clearTrack()

  const points = trackPoints.value.filter(p => p.latitude && p.longitude)
  if (points.length === 0) return

  const realtimePoints = points.filter(p => getSourceType(p) !== 'archive')
  const archivePoints = points.filter(p => getSourceType(p) === 'archive')

  if (mapInstance.type === 'amap' && window.AMap) {
    drawAmap(realtimePoints, archivePoints)
  } else if (mapInstance.type === 'tianditu' && window.T) {
    drawTianditu(realtimePoints, archivePoints)
  }
}

function drawAmap(realtimePoints, archivePoints) {
  const allPaths = []

  if (realtimePoints.length > 0) {
    const path = realtimePoints.map(p => [p.longitude, p.latitude])
    realtimePolyline = new window.AMap.Polyline({
      path, strokeColor: '#3b82f6', strokeWeight: 4, strokeOpacity: 0.85,
    })
    mapInstance.map.add(realtimePolyline)
    allPaths.push(...path)
  }

  if (archivePoints.length > 0) {
    const path = archivePoints.map(p => [p.longitude, p.latitude])
    archivePolyline = new window.AMap.Polyline({
      path, strokeColor: '#f59e0b', strokeWeight: 4, strokeOpacity: 0.85,
      strokeStyle: 'dashed',
    })
    mapInstance.map.add(archivePolyline)
    allPaths.push(...path)
  }

  if (allPaths.length > 0) {
    movingMarker = new window.AMap.Marker({ position: allPaths[0], map: mapInstance.map })
    mapInstance.map.setFitView()
  }
}

function drawTianditu(realtimePoints, archivePoints) {
  const allPoints = []

  if (realtimePoints.length > 0) {
    const pts = realtimePoints.map(p => new window.T.LngLat(p.longitude, p.latitude))
    realtimePolyline = new window.T.Polyline(pts, {
      color: '#3b82f6', weight: 4, opacity: 0.85,
    })
    mapInstance.map.addOverLay(realtimePolyline)
    allPoints.push(...pts)
  }

  if (archivePoints.length > 0) {
    const pts = archivePoints.map(p => new window.T.LngLat(p.longitude, p.latitude))
    archivePolyline = new window.T.Polyline(pts, {
      color: '#f59e0b', weight: 4, opacity: 0.85,
    })
    mapInstance.map.addOverLay(archivePolyline)
    allPoints.push(...pts)
  }

  if (allPoints.length > 0) {
    movingMarker = new window.T.Marker(allPoints[0])
    mapInstance.map.addOverLay(movingMarker)
    if (allPoints.length > 1) {
      mapInstance.map.setViewport(allPoints)
    }
  }
}

function clearTrack() {
  if (!mapInstance) return
  if (mapInstance.type === 'amap') {
    if (realtimePolyline) mapInstance.map.remove(realtimePolyline)
    if (archivePolyline) mapInstance.map.remove(archivePolyline)
    if (movingMarker) mapInstance.map.remove(movingMarker)
  } else if (mapInstance.type === 'tianditu') {
    if (realtimePolyline) mapInstance.map.removeOverLay(realtimePolyline)
    if (archivePolyline) mapInstance.map.removeOverLay(archivePolyline)
    if (movingMarker) mapInstance.map.removeOverLay(movingMarker)
  }
  realtimePolyline = null
  archivePolyline = null
  movingMarker = null
}

function playTrack() {
  if (trackPoints.value.length === 0 || isPlaying.value) return
  isPlaying.value = true
  currentPointIndex.value = 0
  playTimer = setInterval(() => {
    if (currentPointIndex.value >= trackPoints.value.length - 1) {
      pauseTrack()
      return
    }
    currentPointIndex.value++
    updateMovingMarker()
  }, 1000 / playSpeed.value)
}

function pauseTrack() {
  isPlaying.value = false
  if (playTimer) { clearInterval(playTimer); playTimer = null }
}

function updateMovingMarker() {
  const point = trackPoints.value[currentPointIndex.value]
  if (!point || !mapInstance || !movingMarker) return
  if (!point.latitude || !point.longitude) return

  if (mapInstance.type === 'amap' && window.AMap) {
    movingMarker.setPosition([point.longitude, point.latitude])
  } else if (mapInstance.type === 'tianditu' && window.T) {
    movingMarker.setLngLat(new window.T.LngLat(point.longitude, point.latitude))
  }
}

function jumpToPoint(row) {
  // 点击表格行跳转到对应轨迹点
  const idx = trackPoints.value.indexOf(row)
  if (idx >= 0) {
    currentPointIndex.value = idx
    updateMovingMarker()
  }
}

onMounted(() => {
  initMap()
  fetchArchiveProgress()
})

onUnmounted(() => {
  pauseTrack()
  clearTrack()
  stopProgressPolling()
  mapInstance = null
})
</script>

<style scoped>
.archive-page { padding: 24px; }
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
.page-content { display: flex; flex-direction: column; gap: 0; }
.progress-card { margin-bottom: 16px; }
.card-header {
  display: flex; justify-content: space-between; align-items: center;
  font-weight: 500;
}
.metric { text-align: center; padding: 8px 0; }
.metric-label { font-size: 12px; color: var(--jte-text-muted); margin-bottom: 6px; }
.metric-value { font-size: 16px; font-weight: 600; color: var(--jte-text); }
.query-form { display: flex; flex-wrap: wrap; gap: 8px; }
.stat-card { text-align: center; }
.stat-value { font-size: 24px; font-weight: 700; color: var(--jte-text); }
.stat-label { font-size: 12px; color: var(--jte-text-muted); margin-top: 4px; }
.map-container {
  height: 480px; width: 100%; background: #1a1f2e;
  border-radius: 8px; overflow: hidden; position: relative;
}
.map-placeholder {
  display: flex; flex-direction: column; align-items: center; justify-content: center;
  height: 100%; color: var(--jte-text-muted);
}
.map-controls {
  display: flex; align-items: center; gap: 8px; flex-wrap: wrap;
}
.speed-label { font-size: 12px; color: var(--jte-text-muted); }
.point-label { font-size: 12px; color: var(--jte-text-muted); margin-left: 8px; }

/* 响应式：移动端适配 */
@media (max-width: 768px) {
  .archive-page { padding: 12px; }
  .page-header { flex-direction: column; align-items: stretch; }
  .header-actions { width: 100%; }
  .header-actions .el-button { flex: 1; }
  .map-container { height: 320px; }
  .map-controls { width: 100%; }
  .query-form { width: 100%; }
  .query-form .el-form-item { width: 100%; margin-right: 0; }
  .query-form .el-form-item :deep(.el-date-editor) { width: 100% !important; }
}
</style>
