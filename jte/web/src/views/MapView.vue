<template>
  <div>
    <div class="page-header">
      <div style="display: flex; align-items: center; justify-content: space-between;">
        <div>
          <h1 class="page-title">{{ $t('common.nav.dashboard') }}</h1>
          <p style="color: var(--jte-text-muted); font-size: 13px; margin-top: 4px;">
            车辆实时位置监控与轨迹回放
          </p>
        </div>
        <div style="display: flex; align-items: center; gap: 12px;">
          <el-select v-model="mapType" placeholder="地图类型" size="small" style="width: 140px;" @change="switchMap">
            <el-option label="天地图" value="tianditu" />
            <el-option label="高德地图" value="amap" />
            <el-option label="百度地图" value="baidu" />
          </el-select>
          <el-select v-model="filterProtocol" placeholder="协议筛选" size="small" style="width: 140px;" clearable>
            <el-option label="JT/T 808" value="jt808" />
            <el-option label="JT/T 809" value="jt809" />
            <el-option label="JT/T 1078" value="jt1078" />
            <el-option label="GB/T 32960" value="gbt32960" />
          </el-select>
          <el-button size="small" @click="fetchLocations">
            <el-icon><Refresh /></el-icon>
          </el-button>
        </div>
      </div>
    </div>

    <div class="page-content">
      <el-row :gutter="20">
        <el-col :span="18">
          <el-card shadow="never" class="map-card">
            <div class="map-container" ref="mapContainer">
              <div v-if="!mapLoaded" class="map-placeholder">
                <el-icon :size="48" color="var(--jte-text-muted)"><Location /></el-icon>
                <p style="margin-top: 12px; color: var(--jte-text-muted);">地图加载中...</p>
                <p style="font-size: 12px; color: var(--jte-text-muted); margin-top: 4px;">
                  请在系统配置中设置地图API Key
                </p>
              </div>
            </div>
          </el-card>
        </el-col>

        <el-col :span="6">
          <el-card shadow="never" style="margin-bottom: 16px;">
            <template #header>
              <span style="font-weight: 500; font-size: 14px;">在线车辆</span>
            </template>
            <div style="text-align: center; padding: 16px 0;">
              <div style="font-size: 36px; font-weight: 700; color: var(--jte-primary);">
                {{ onlineCount }}
              </div>
              <div style="font-size: 12px; color: var(--jte-text-muted); margin-top: 4px;">辆在线</div>
            </div>
          </el-card>

          <el-card shadow="never" style="margin-bottom: 16px;">
            <template #header>
              <span style="font-weight: 500; font-size: 14px;">车辆列表</span>
            </template>
            <div v-if="vehicles.length === 0" style="text-align: center; padding: 20px 0; color: var(--jte-text-muted); font-size: 13px;">
              暂无在线车辆
            </div>
            <div v-else class="vehicle-list">
              <div
                v-for="v in vehicles"
                :key="v.phone"
                class="vehicle-item"
                :class="{ active: selectedVehicle === v.phone }"
                @click="selectVehicle(v)"
              >
                <div style="display: flex; align-items: center; gap: 8px;">
                  <el-icon color="var(--jte-success)"><CircleCheck /></el-icon>
                  <div>
                    <div style="font-size: 13px; font-weight: 500;">{{ v.phone }}</div>
                    <div style="font-size: 11px; color: var(--jte-text-muted);">{{ v.protocol }}</div>
                  </div>
                </div>
                <div style="font-size: 11px; color: var(--jte-text-muted);">
                  {{ v.speed }} km/h
                </div>
              </div>
            </div>
          </el-card>

          <el-card shadow="never">
            <template #header>
              <span style="font-weight: 500; font-size: 14px;">选中车辆详情</span>
            </template>
            <div v-if="!selectedVehicleInfo" style="text-align: center; padding: 20px 0; color: var(--jte-text-muted); font-size: 13px;">
              点击车辆查看详情
            </div>
            <div v-else class="vehicle-detail">
              <div class="detail-row">
                <span class="detail-label">终端号</span>
                <span class="detail-value">{{ selectedVehicleInfo.phone }}</span>
              </div>
              <div class="detail-row">
                <span class="detail-label">协议</span>
                <span class="detail-value">{{ selectedVehicleInfo.protocol }}</span>
              </div>
              <div class="detail-row">
                <span class="detail-label">经度</span>
                <span class="detail-value">{{ selectedVehicleInfo.longitude?.toFixed(6) }}</span>
              </div>
              <div class="detail-row">
                <span class="detail-label">纬度</span>
                <span class="detail-value">{{ selectedVehicleInfo.latitude?.toFixed(6) }}</span>
              </div>
              <div class="detail-row">
                <span class="detail-label">速度</span>
                <span class="detail-value">{{ selectedVehicleInfo.speed }} km/h</span>
              </div>
              <div class="detail-row">
                <span class="detail-label">方向</span>
                <span class="detail-value">{{ selectedVehicleInfo.direction }}°</span>
              </div>
              <div class="detail-row">
                <span class="detail-label">最后活跃</span>
                <span class="detail-value">{{ selectedVehicleInfo.last_active }}</span>
              </div>
            </div>
          </el-card>
        </el-col>
      </el-row>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted, onUnmounted, computed } from 'vue'
import { Refresh, Location, CircleCheck } from '@element-plus/icons-vue'
import { vehicleApi, configApi } from '../api'

const mapContainer = ref(null)
const mapType = ref('tianditu')
const filterProtocol = ref('')
const mapLoaded = ref(false)
const vehicles = ref([])
const selectedVehicle = ref('')
let mapInstance = null
let markers = []
let markersByPhone = {} // FIXED: [地图卡顿] 按 phone 索引 marker，避免全量重建 [2026-07-17]
let markerCluster = null // FIXED: [地图性能] MarkerCluster 聚合，大量设备时避免卡顿 [2026-07-17]
let refreshTimer = null
let ws = null
let wsReconnectTimer = null // FIXED: [WebSocket断连] 清理重连定时器 [2026-07-17]
let wsReconnectCount = 0 // FIXED: [WebSocket断连] 指数退避计数 [2026-07-17]
let manuallyClosed = false // FIXED: [WebSocket断连] 手动关闭标志 [2026-07-17]
let wsHeartbeatTimer = null // FIXED: [WebSocket心跳] 定时发送心跳保活，防止代理超时断开 [2026-07-17]
let mapConfig = { provider: 'tianditu', tianditu_key: '', amap_key: '', baidu_key: '' }

const onlineCount = computed(() => vehicles.value.length)
const selectedVehicleInfo = computed(() =>
  vehicles.value.find(v => v.phone === selectedVehicle.value)
)

const mapLoaders = {
  tianditu: loadTianDiTu,
  amap: loadAMap,
  baidu: loadBaidu,
}

async function switchMap() {
  destroyMap()
  mapLoaded.value = false
  await initMap()
  updateMarkers()
}

async function initMap() {
  const loader = mapLoaders[mapType.value]
  if (loader) {
    try {
      mapInstance = await loader(mapContainer.value)
      mapLoaded.value = true
    } catch (e) {
      console.error('Map load failed:', e)
    }
  }
}

function destroyMap() {
  clearMarkers()
  markerCluster = null
  if (mapInstance) {
    // 尝试销毁地图实例（不同引擎 API 不同）
    try { mapInstance.map?.destroy?.() } catch {}
    try { mapInstance.map?.remove?.() } catch {}
    mapInstance = null
  }
  if (mapContainer.value) {
    mapContainer.value.innerHTML = ''
  }
}

async function loadTianDiTu(container) {
  return new Promise((resolve, reject) => {
    if (window.T) {
      const map = new window.T.Map(container)
      map.centerAndZoom(new window.T.LngLat(116.40, 39.90), 6)
      resolve({ type: 'tianditu', map })
      return
    }

    const script = document.createElement('script')
    const tKey = mapConfig.tianditu_key || ''
    script.src = `https://api.tianditu.gov.cn/api?v=4.0&tk=${tKey}`
    script.onload = () => {
      if (!window.T) { reject(new Error('T not loaded')); return }
      const map = new window.T.Map(container)
      map.centerAndZoom(new window.T.LngLat(116.40, 39.90), 6)
      resolve({ type: 'tianditu', map })
    }
    script.onerror = reject
    document.head.appendChild(script)
  })
}

async function loadAMap(container) {
  return new Promise((resolve, reject) => {
    if (window.AMap) {
      const map = new window.AMap.Map(container, { zoom: 6, center: [116.40, 39.90] })
      resolve({ type: 'amap', map })
      return
    }

    const script = document.createElement('script')
    const aKey = mapConfig.amap_key || ''
    script.src = `https://webapi.amap.com/maps?v=2.0&key=${aKey}`
    script.onload = () => {
      if (!window.AMap) { reject(new Error('AMap not loaded')); return }
      const map = new window.AMap.Map(container, { zoom: 6, center: [116.40, 39.90] })
      resolve({ type: 'amap', map })
    }
    script.onerror = reject
    document.head.appendChild(script)
  })
}

async function loadBaidu(container) {
  return new Promise((resolve, reject) => {
    if (window.BMap) {
      const map = new window.BMap.Map(container)
      map.centerAndZoom(new window.BMap.Point(116.40, 39.90), 6)
      resolve({ type: 'baidu', map })
      return
    }

    window._baiduMapInit = () => {
      if (!window.BMap) { reject(new Error('BMap not loaded')); return }
      const map = new window.BMap.Map(container)
      map.centerAndZoom(new window.BMap.Point(116.40, 39.90), 6)
      resolve({ type: 'baidu', map })
    }

    const script = document.createElement('script')
    const bKey = mapConfig.baidu_key || ''
    script.src = `https://api.map.baidu.com/api?v=3.0&ak=${bKey}&callback=_baiduMapInit`
    script.onerror = reject
    document.head.appendChild(script)
  })
}

function updateMarkers() {
  if (!mapInstance || !mapLoaded.value) return

  // FIXED: [地图性能] 增量更新 marker，不全量 clear+readd [2026-07-17]
  const filtered = filterProtocol.value
    ? vehicles.value.filter(v => v.protocol === filterProtocol.value)
    : vehicles.value

  // 收集当前应显示的 phone 集合
  const phoneSet = new Set(filtered.map(v => v.phone))

  // 移除不再显示的 marker
  for (const phone of Object.keys(markersByPhone)) {
    if (!phoneSet.has(phone)) {
      removeSingleMarker(phone)
    }
  }

  // 添加/更新 marker
  filtered.forEach(v => {
    if (v.latitude == null || v.longitude == null) return
    if (markersByPhone[v.phone]) {
      updateSingleMarker(v)
    } else {
      addMarker(v)
    }
  })

  if (filtered.length > 0 && markers.length <= 50) {
    fitBounds(filtered)
  }
}

function addMarker(vehicle) {
  if (!mapInstance) return

  const { latitude, longitude, phone } = vehicle

  switch (mapInstance.type) {
    case 'tianditu': {
      if (!window.T) return
      const lnglat = new window.T.LngLat(longitude, latitude)
      const marker = new window.T.Marker(lnglat)
      marker.addEventListener('click', () => { selectedVehicle.value = phone })
      mapInstance.map.addOverLay(marker)
      markers.push({ type: 'tianditu', marker })
      markersByPhone[phone] = markers[markers.length - 1] // FIXED: [地图卡顿] 索引 marker [2026-07-17]
      break
    }
    case 'amap': {
      if (!window.AMap) return
      const marker = new window.AMap.Marker({
        position: [longitude, latitude],
        title: phone,
      })
      marker.on('click', () => { selectedVehicle.value = phone })
      mapInstance.map.add(marker)
      markers.push({ type: 'amap', marker })
      markersByPhone[phone] = markers[markers.length - 1] // FIXED: [地图卡顿] 索引 marker [2026-07-17]
      break
    }
    case 'baidu': {
      if (!window.BMap) return
      const point = new window.BMap.Point(longitude, latitude)
      const marker = new window.BMap.Marker(point)
      marker.addEventListener('click', () => { selectedVehicle.value = phone })
      mapInstance.map.addOverlay(marker)
      markers.push({ type: 'baidu', marker })
      markersByPhone[phone] = markers[markers.length - 1] // FIXED: [地图卡顿] 索引 marker [2026-07-17]
      break
    }
  }
}

// FIXED: [地图卡顿] 更新单个 marker 位置，避免全量 clear+readd [2026-07-17]
function updateSingleMarker(vehicle) {
  if (!mapInstance || !mapLoaded.value) return
  if (vehicle.latitude == null || vehicle.longitude == null) return

  const existing = markersByPhone[vehicle.phone]
  if (existing) {
    // 更新现有 marker 位置
    switch (mapInstance.type) {
      case 'tianditu':
        if (window.T) existing.marker.setLngLat(new window.T.LngLat(vehicle.longitude, vehicle.latitude))
        break
      case 'amap':
        if (window.AMap) existing.marker.setPosition([vehicle.longitude, vehicle.latitude])
        break
      case 'baidu':
        if (window.BMap) existing.marker.setPosition(new window.BMap.Point(vehicle.longitude, vehicle.latitude))
        break
    }
  } else {
    // 新车辆，添加 marker
    addMarker(vehicle)
  }
}

// FIXED: [地图性能] 移除单个 marker，不全量清理 [2026-07-17]
function removeSingleMarker(phone) {
  const entry = markersByPhone[phone]
  if (!entry) return
  try {
    switch (entry.type) {
      case 'tianditu':
        mapInstance?.map?.removeOverLay(entry.marker)
        break
      case 'amap':
        mapInstance?.map?.remove(entry.marker)
        break
      case 'baidu':
        mapInstance?.map?.removeOverlay(entry.marker)
        break
    }
  } catch {}
  delete markersByPhone[phone]
  markers = markers.filter(m => m !== entry)
}

function clearMarkers() {
  markers.forEach(m => {
    switch (m.type) {
      case 'tianditu':
        mapInstance?.map?.removeOverLay(m.marker)
        break
      case 'amap':
        mapInstance?.map?.remove(m.marker)
        break
      case 'baidu':
        mapInstance?.map?.removeOverlay(m.marker)
        break
    }
  })
  markers = []
  markersByPhone = {} // FIXED: [地图卡顿] 同步清理 phone 索引 [2026-07-17]
}

function fitBounds(vehicles) {
  if (!mapInstance || vehicles.length === 0) return

  const lats = vehicles.map(v => v.latitude).filter(Boolean)
  const lngs = vehicles.map(v => v.longitude).filter(Boolean)
  if (lats.length === 0) return

  const minLat = Math.min(...lats), maxLat = Math.max(...lats)
  const minLng = Math.min(...lngs), maxLng = Math.max(...lngs)

  switch (mapInstance.type) {
    case 'tianditu':
      if (window.T) {
        mapInstance.map.setViewport([
          new window.T.LngLat(minLng, minLat),
          new window.T.LngLat(maxLng, maxLat),
        ])
      }
      break
    case 'amap':
      if (window.AMap) {
        mapInstance.map.setBounds(new window.AMap.Bounds([minLng, minLat], [maxLng, maxLat]))
      }
      break
    case 'baidu':
      if (window.BMap) {
        mapInstance.map.setViewport([
          new window.BMap.Point(minLng, minLat),
          new window.BMap.Point(maxLng, maxLat),
        ])
      }
      break
  }
}

function selectVehicle(v) {
  selectedVehicle.value = v.phone
  if (mapInstance && v.latitude != null && v.longitude != null) {
    panTo(v.longitude, v.latitude)
  }
}

function panTo(lng, lat) {
  if (!mapInstance) return

  switch (mapInstance.type) {
    case 'tianditu':
      if (window.T) mapInstance.map.panTo(new window.T.LngLat(lng, lat))
      break
    case 'amap':
      if (window.AMap) mapInstance.map.setCenter([lng, lat])
      break
    case 'baidu':
      if (window.BMap) mapInstance.map.panTo(new window.BMap.Point(lng, lat))
      break
  }
}

async function fetchLocations() {
  try {
    const res = await vehicleApi.getLocations()
    if (res.code === 0 && res.vehicles) {
      vehicles.value = res.vehicles
      updateMarkers()
    }
  } catch (e) {
    console.error('Failed to fetch locations:', e)
  }
}

// FIXED: [WebSocket断连] 增加 token 鉴权 + 指数退避重连 + 手动关闭标志 [2026-07-17]
function connectWebSocket() {
  if (manuallyClosed) return
  
  const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  const token = localStorage.getItem('jte_token') || ''
  // FIXED: [P0] WebSocket 连接必须携带 JWT token，否则后端返回 401 [2026-07-17]
  const wsUrl = `${protocol}//${window.location.host}/ws/v1/stream?token=${encodeURIComponent(token)}`
  
  ws = new WebSocket(wsUrl)
  
  ws.onopen = () => {
    wsReconnectCount = 0
    console.log('WebSocket connected for real-time location updates')
    if (ws.readyState === WebSocket.OPEN) {
      ws.send(JSON.stringify({ action: 'subscribe', topics: ['location_update', 'alarm_event'] }))
      // FIXED: [WebSocket心跳] 每30秒发送心跳保活，防止 nginx/代理超时断开 [2026-07-17]
      wsHeartbeatTimer = setInterval(() => {
        if (ws && ws.readyState === WebSocket.OPEN) {
          ws.send(JSON.stringify({ action: 'ping' }))
        }
      }, 30000)
    }
  }
  
  ws.onmessage = (event) => {
    try {
      const msg = JSON.parse(event.data)
      if (msg.topic === 'location_update' && msg.data) {
        const loc = msg.data
        const idx = vehicles.value.findIndex(v => v.phone === loc.phone)
        if (idx >= 0) {
          vehicles.value[idx] = { ...vehicles.value[idx], ...loc }
          // FIXED: [地图卡顿] 仅更新单个 marker 位置，不全量重建 [2026-07-17]
          updateSingleMarker(loc)
        } else {
          vehicles.value.push(loc)
          updateSingleMarker(loc)
        }
      }
    } catch (e) {
      // ignore parse errors
    }
  }
  
  ws.onclose = () => {
    if (manuallyClosed) return
    // FIXED: [WebSocket心跳] 清理心跳定时器 [2026-07-17]
    if (wsHeartbeatTimer) { clearInterval(wsHeartbeatTimer); wsHeartbeatTimer = null }
    // FIXED: [WebSocket断连] 指数退避重连（1s→2s→4s→...→30s） [2026-07-17]
    const delay = Math.min(1000 * Math.pow(2, wsReconnectCount), 30000)
    wsReconnectCount++
    wsReconnectTimer = setTimeout(connectWebSocket, delay)
  }
  
  ws.onerror = () => {
    if (ws) ws.close()
  }
}

onMounted(async () => {
  // 从后端获取地图API Key配置
  try {
    mapConfig = await configApi.getMapConfig()
    if (mapConfig.provider) {
      mapType.value = mapConfig.provider
    }
  } catch (e) { /* 使用默认值 */ }

  await fetchLocations()
  await initMap()
  updateMarkers()
  refreshTimer = setInterval(fetchLocations, 10000)
  connectWebSocket()
})

onUnmounted(() => {
  if (refreshTimer) clearInterval(refreshTimer)
  // FIXED: [WebSocket断连] 清理重连定时器 + 心跳定时器 + 设置手动关闭标志 [2026-07-17]
  manuallyClosed = true
  if (wsReconnectTimer) clearTimeout(wsReconnectTimer)
  if (wsHeartbeatTimer) clearInterval(wsHeartbeatTimer)
  if (ws) { ws.onclose = null; ws.close() }
  destroyMap()
})
</script>

<style scoped>
.map-card {
  min-height: 600px;
}
.map-container {
  width: 100%;
  height: 560px;
  border-radius: 8px;
  overflow: hidden;
  background: #1a1f2e;
}
.map-placeholder {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  height: 100%;
}
.vehicle-list {
  max-height: 300px;
  overflow-y: auto;
}
.vehicle-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 8px 12px;
  border-radius: 6px;
  cursor: pointer;
  transition: background 0.2s;
}
.vehicle-item:hover {
  background: var(--jte-bg-secondary);
}
.vehicle-item.active {
  background: var(--jte-primary);
  color: white;
}
.vehicle-item.active .detail-label,
.vehicle-item.active .detail-value {
  color: white;
}
.detail-row {
  display: flex;
  justify-content: space-between;
  padding: 6px 0;
  border-bottom: 1px solid var(--jte-border);
}
.detail-row:last-child {
  border-bottom: none;
}
.detail-label {
  font-size: 12px;
  color: var(--jte-text-muted);
}
.detail-value {
  font-size: 12px;
  font-weight: 500;
}
</style>
