<template>
  <div class="page-container">
    <div class="page-header">
      <h2>{{ $t('common.nav.tracks') }}</h2>
      <div style="display:flex;gap:12px;align-items:center">
        <el-input v-model="vehicleId" placeholder="车辆ID或终端号" style="width:200px" />
        <el-date-picker v-model="timeRange" type="datetimerange" range-separator="至" start-placeholder="开始时间" end-placeholder="结束时间" />
        <el-button type="primary" @click="fetchTrack">查询</el-button>
      </div>
    </div>
    <el-card shadow="never" style="margin-bottom:16px">
      <div ref="trackMapContainer" style="height:500px;width:100%;background:#1a1f2e;border-radius:8px;overflow:hidden;position:relative">
        <div v-if="!mapReady" style="display:flex;flex-direction:column;align-items:center;justify-content:center;height:100%;color:var(--jte-text-muted)">
          <el-icon :size="48"><Location /></el-icon>
          <p style="margin-top:12px">{{ $t('common.msg.loading') }}</p>
        </div>
      </div>
      <div v-if="trackPoints.length > 0" style="margin-top:12px;display:flex;align-items:center;gap:16px">
        <el-button size="small" @click="playTrack" :disabled="isPlaying">
          <el-icon><VideoPlay /></el-icon> 播放
        </el-button>
        <el-button size="small" @click="pauseTrack" :disabled="!isPlaying">
          <el-icon><VideoPause /></el-icon> 暂停
        </el-button>
        <el-slider v-model="playSpeed" :min="1" :max="20" :step="1" style="width:200px" />
        <span style="font-size:12px;color:var(--jte-text-muted)">{{ playSpeed }}x 速度</span>
        <span style="font-size:12px;color:var(--jte-text-muted);margin-left:auto">
          {{ currentPointIndex + 1 }} / {{ trackPoints.length }} 点
        </span>
      </div>
    </el-card>
    <el-table :data="trackPoints" stripe size="small" max-height="300">
      <el-table-column prop="latitude" label="纬度" width="120" />
      <el-table-column prop="longitude" label="经度" width="120" />
      <el-table-column prop="speed" label="速度(km/h)" width="100" />
      <el-table-column prop="direction" label="方向" width="80" />
      <el-table-column prop="received_at" label="时间" min-width="160" />
    </el-table>
  </div>
</template>

<script setup>
import { ref, onMounted, onUnmounted, watch } from 'vue'
import { Location, VideoPlay, VideoPause } from '@element-plus/icons-vue'
import { trackApi, configApi } from '../api'
import { ElMessage } from 'element-plus'

const vehicleId = ref('')
const timeRange = ref([])
const trackPoints = ref([])
const trackMapContainer = ref(null)
const mapReady = ref(false)
const isPlaying = ref(false)
const playSpeed = ref(5)
const currentPointIndex = ref(0)

let mapInstance = null
let polyline = null
let movingMarker = null
let playTimer = null

async function initMap() {
  if (!trackMapContainer.value) return

  // 从后端获取地图API Key配置
  let mapConfig = { provider: 'tianditu', tianditu_key: '', amap_key: '', baidu_key: '' }
  try {
    mapConfig = await configApi.getMapConfig()
  } catch (e) { /* 使用默认值 */ }

  try {
    if (window.AMap) {
      mapInstance = { type: 'amap', map: new window.AMap.Map(trackMapContainer.value, { zoom: 6, center: [116.40, 39.90] }) }
      mapReady.value = true
      return
    }
  } catch (e) { /* ignore */ }

  try {
    if (window.T) {
      const map = new window.T.Map(trackMapContainer.value)
      map.centerAndZoom(new window.T.LngLat(116.40, 39.90), 6)
      mapInstance = { type: 'tianditu', map }
      mapReady.value = true
      return
    }
  } catch (e) { /* ignore */ }

  // 根据配置动态加载地图SDK
  const provider = mapConfig.provider || 'tianditu'
  const script = document.createElement('script')
  if (provider === 'tianditu' && mapConfig.tianditu_key) {
    script.src = `https://api.tianditu.gov.cn/api?v=4.0&tk=${mapConfig.tianditu_key}`
  } else if (provider === 'amap' && mapConfig.amap_key) {
    script.src = `https://webapi.amap.com/maps?v=2.0&key=${mapConfig.amap_key}`
  } else if (provider === 'baidu' && mapConfig.baidu_key) {
    script.src = `https://api.map.baidu.com/api?v=3.0&ak=${mapConfig.baidu_key}&callback=_baiduMapInit`
  } else {
    // 无Key时使用天地图（政务场景下内网部署可自行替换）
    mapReady.value = true
    return
  }
  script.onload = () => {
    if (window.T) {
      const map = new window.T.Map(trackMapContainer.value)
      map.centerAndZoom(new window.T.LngLat(116.40, 39.90), 6)
      mapInstance = { type: 'tianditu', map }
    } else if (window.AMap) {
      mapInstance = { type: 'amap', map: new window.AMap.Map(trackMapContainer.value, { zoom: 6, center: [116.40, 39.90] }) }
    }
    mapReady.value = true
  }
  document.head.appendChild(script)
}

function drawTrack() {
  if (!mapInstance || trackPoints.value.length === 0) return

  clearTrack()

  const points = trackPoints.value.filter(p => p.latitude && p.longitude)
  if (points.length === 0) return

  if (mapInstance.type === 'amap' && window.AMap) {
    const path = points.map(p => [p.longitude, p.latitude])
    polyline = new window.AMap.Polyline({ path, strokeColor: '#3b82f6', strokeWeight: 4, strokeOpacity: 0.8 })
    mapInstance.map.add(polyline)
    movingMarker = new window.AMap.Marker({ position: path[0], map: mapInstance.map })
    mapInstance.map.setFitView([polyline])
  } else if (mapInstance.type === 'tianditu' && window.T) {
    const points2 = points.map(p => new window.T.LngLat(p.longitude, p.latitude))
    polyline = new window.T.Polyline(points2, { color: '#3b82f6', weight: 4, opacity: 0.8 })
    mapInstance.map.addOverLay(polyline)
    movingMarker = new window.T.Marker(points2[0])
    mapInstance.map.addOverLay(movingMarker)
    if (points2.length > 1) {
      mapInstance.map.setViewport(points2)
    }
  }
}

function clearTrack() {
  if (!mapInstance) return
  if (mapInstance.type === 'amap') {
    if (polyline) mapInstance.map.remove(polyline)
    if (movingMarker) mapInstance.map.remove(movingMarker)
  } else if (mapInstance.type === 'tianditu') {
    if (polyline) mapInstance.map.removeOverLay(polyline)
    if (movingMarker) mapInstance.map.removeOverLay(movingMarker)
  }
  polyline = null
  movingMarker = null
}

function playTrack() {
  if (trackPoints.value.length === 0 || isPlaying.value) return
  isPlaying.value = true
  currentPointIndex.value = 0
  startPlayTimer()
}

// FIXED: [轨迹回放] 速度变化时重启定时器，使新速度立即生效 [2026-07-17]
function startPlayTimer() {
  if (playTimer) { clearInterval(playTimer); playTimer = null }
  if (!isPlaying.value) return
  playTimer = setInterval(() => {
    if (currentPointIndex.value >= trackPoints.value.length - 1) {
      pauseTrack()
      return
    }
    currentPointIndex.value++
    updateMovingMarker()
  }, 1000 / playSpeed.value)
}

// FIXED: [轨迹回放] 监听速度变化，实时调整播放速度 [2026-07-17]
watch(playSpeed, () => {
  if (isPlaying.value) {
    startPlayTimer()
  }
})

function pauseTrack() {
  isPlaying.value = false
  if (playTimer) { clearInterval(playTimer); playTimer = null }
}

function updateMovingMarker() {
  const point = trackPoints.value[currentPointIndex.value]
  if (!point || !mapInstance || !movingMarker) return

  if (mapInstance.type === 'amap' && window.AMap) {
    movingMarker.setPosition([point.longitude, point.latitude])
  } else if (mapInstance.type === 'tianditu' && window.T) {
    movingMarker.setLngLat(new window.T.LngLat(point.longitude, point.latitude))
  }
}

async function fetchTrack() {
  if (!vehicleId.value) {
    ElMessage.warning('请输入车辆ID或终端号')
    return
  }
  pauseTrack()
  try {
    const params = { phone: vehicleId.value }
    if (timeRange.value && timeRange.value.length === 2) {
      params.start_time = timeRange.value[0].toISOString()
      params.end_time = timeRange.value[1].toISOString()
    }
    const res = await trackApi.getTrack(params)
    // 后端统一返回 {track: [...], total: N}（无 code/data 包装）
    trackPoints.value = res.track || res.data?.items || []
    if (trackPoints.value.length > 0) drawTrack()
    else ElMessage.info('未查询到轨迹数据')
  } catch (e) {
    console.error(e)
    ElMessage.error('查询轨迹失败，请检查网络或稍后重试')
  }
}

onMounted(() => { initMap() })
onUnmounted(() => {
  pauseTrack()
  clearTrack()
  mapInstance = null
})
</script>

<style scoped>
.page-container { padding: 24px; }
.page-header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 20px; }
.page-header h2 { margin: 0; font-size: 18px; color: var(--jte-text); }
</style>
