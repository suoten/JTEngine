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
let refreshTimer = null
let ws = null
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
  markers = []
  if (mapInstance) {
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

  clearMarkers()

  const filtered = filterProtocol.value
    ? vehicles.value.filter(v => v.protocol === filterProtocol.value)
    : vehicles.value

  filtered.forEach(v => {
    if (v.latitude == null || v.longitude == null) return
    addMarker(v)
  })

  if (filtered.length > 0) {
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
      break
    }
    case 'baidu': {
      if (!window.BMap) return
      const point = new window.BMap.Point(longitude, latitude)
      const marker = new window.BMap.Marker(point)
      marker.addEventListener('click', () => { selectedVehicle.value = phone })
      mapInstance.map.addOverlay(marker)
      markers.push({ type: 'baidu', marker })
      break
    }
  }
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

function connectWebSocket() {
  const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  const wsUrl = `${protocol}//${window.location.host}/ws/v1/stream`
  
  ws = new WebSocket(wsUrl)
  
  ws.onopen = () => {
    console.log('WebSocket connected for real-time location updates')
    if (ws.readyState === WebSocket.OPEN) {
      ws.send(JSON.stringify({ action: 'subscribe', topics: ['location_update', 'alarm_event'] }))
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
        } else {
          vehicles.value.push(loc)
        }
        updateMarkers()
      }
    } catch (e) {
      // ignore parse errors
    }
  }
  
  ws.onclose = () => {
    setTimeout(connectWebSocket, 5000)
  }
  
  ws.onerror = () => {
    ws.close()
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
�͔�����𽕰�х��(��������������𽑥��(���������������؁ص��􉙥�ѕɕ��٥��̹����Ѡ���������屔�ѕ�е�����聍��ѕ��������������쁍�����مȠ���є�ѕ�е��ѕ��쁙��еͥ��������(�����������������r��^�������"g�V����4(��������������𽑥��(������������𽑥��(����������𽕰���ɐ�((����������񕰵��ɐ�͡����􉹕ٕȈ�ص���͕���ѕ��٥����(�������������ѕ����є���������(����������������������屔􉙽�еݕ��������쁙��еͥ���������������s��@������(�������������ѕ����є�(�������������؁�����􉑕٥�����х����(���������������؁�����􉑕х���ɽ܈�(���������������������������􉑕х�����������&��"�������(���������������������������􉑕х���م�Ք����͕���ѕ��٥�����������������(��������������𽑥��(���������������؁�����􉑕х���ɽ܈�(���������������������������􉑕х�����������>c���������(���������������������������􉑕х���م�Ք���쀡͕���ѕ��٥����ɽѽ�����������ѽU����
�͔�����������(��������������𽑥��(���������������؁�����􉑕х���ɽ܈�(���������������������������􉑕х�������������v ������(���������������������������􉑕х���م�Ք����͕���ѕ��٥���������Ց���ѽ�ᕐ�ؤ����������������(��������������𽑥��(���������������؁�����􉑕х���ɽ܈�(���������������������������􉑕х����������������������(���������������������������􉑕х���م�Ք����͕���ѕ��٥�����ѥ�Ց���ѽ�ᕐ�ؤ����������������(��������������𽑥��(���������������؁�����􉑕х���ɽ܈�(���������������������������􉑕х������������c�Z�������(���������������������������􉑕х���م�Ք����͕���ѕ��٥�����������������􁭴��������(��������������𽑥��(���������������؁�����􉑕х���ɽ܈�(���������������������������􉑕х�����������Z��<������(���������������������������􉑕х���م�Ք����͕���ѕ��٥�����ɕ�ѥ��������������������(��������������𽑥��(���������������؁�����􉑕х���ɽ܈�(���������������������������􉑕х������������O��c�B4������(���������������������������􉑕х���م�Ք���쁙�ɵ��Q����͕���ѕ��٥�������}��ѥٔ����������(��������������𽑥��(������������𽑥��(����������𽕰���ɐ�(��������𽕰�����(������𽕰�ɽ��(����𽑥��(��𽑥��(�ѕ����є�((�͍ɥ�Ё͕����)�����Ё�ɕ��������ѕ�����5�չѕ�����U���չѕ�������Q�����ɽ����Ք�)�����Ё�ٕ���������ɽ����������)�����Ё��͕��Mѽɔ��ɽ�������ѽɕ̽����()����Ё���Mѽɔ���͕��Mѽɔ��)����Ё���
��х���Ȁ�ɕ���ձ��)����Ё���1�������ɕ�����͔�)����Ё��٥��̀�ɕ��mt�)����Ё͕���ѕ��٥����ɕ���ձ��)����Ё���ѕ�Aɽѽ�����ɕ�����)����Ё���Q�����ɕ�����Mѽɔ����Q����)��Ё�����ձ�)��Ё��ɭ��̀�mt)��Ёѥ��Ȁ�ձ�)��Ё���ɕ��5��Q����􁵅�Q����م�Ք()����Ё���ѕɕ��٥��̀􁍽���ѕ���������(����������ѕ�Aɽѽ����م�Ք��ɕ��ɸ���٥��̹م�Ք(��ɕ��ɸ���٥��̹م�Ք����ѕȡ��������ɽѽ������􁙥�ѕ�Aɽѽ����م�Ք�)��()��幌��չ�ѥ�������Q������Ԡ���(������Ё������Mѽɔ�ѥ������-��م�Ք���������й��ф���عY%Q}Q%9%QU}-d(���������䤁�(�������1������م�Ք�􁙅�͔(����ɕ��ɸ����͔(���((��ɕ��ɸ���܁Aɽ��͔��ɕͽ�ٔ������(��������Ё͍ɥ�Ѐ􁑽�յ��й�ɕ�ѕ�����Р�͍ɥ�М�(����͍ɥ�й�Ɍ�􁁡����輽й�����Թ��ع�����)����и�������������(����͍ɥ�й�������􀠤�����(������ɕͽ�ٔ���Ք�(�����(����͍ɥ�й����ɽȀ􀠤����ɕͽ�ٔ����͔�(�������յ��й�����������
�����͍ɥ�Ф(����)�()��幌��չ�ѥ�������5������(������Ё������Mѽɔ�����-��م�Ք���������й��ф���عY%Q}5A}-d(���������䤁�(�������1������م�Ք�􁙅�͔(����ɕ��ɸ����͔(���((��ɕ��ɸ���܁Aɽ��͔��ɕͽ�ٔ������(��������Ё͍ɥ�Ѐ􁑽�յ��й�ɕ�ѕ�����Р�͍ɥ�М�(����͍ɥ�й�Ɍ�􁁡����輽ݕ���������������������ȸ�������������(����͍ɥ�й�������􀠤�����(������ɕͽ�ٔ���Ք�(�����(����͍ɥ�й����ɽȀ􀠤����ɕͽ�ٔ����͔�(�������յ��й�����������
�����͍ɥ�Ф(����)�()��幌��չ�ѥ�������Q�������5������(������Ё��������݅�Ё����Q������Ԡ�(������������������ݥ���ܹP��ɕ��ɸ(((���݅�Ё����Q�����(������􁹕܁ܹ5���(�������
��х���ȹم�Ք�(�����(���������ѕ��l�Է3�䰀��и������t�(������齽��԰(�����(���(���������=ٕɱ�䡹�܁P�5��Q���Q������(�����1������م�Ք����Ք)�()��幌��չ�ѥ�������5��5������(������Ё��������݅�Ё����5����(������������������ݥ���ܹ5����ɕ��ɸ((���݅�Ё����Q�����(������􁹕܁5���5������
��х���ȹم�Ք���(����齽��԰(�������ѕ��l��и�����԰��Ը������t�(�������M�屔耝����輽��展̽��ɬ��(����٥��5���耜���(����(�����1������م�Ք����Ք)�()��幌��չ�ѥ�������5������(���������Q����م�Ք����ѥ�����Ԝ���(�����݅�Ё����Q�������5����(��􁕱͔��(�����݅�Ё����5��5����(���(�����ɕ��5��Q����􁵅�Q����م�Ք)�()��幌��չ�ѥ����ݥэ�5������(������������(������������ɽ䠤(���������ձ�(�������1������م�Ք�􁙅�͔(���(����ɭ��̀�mt(�����Mѽɔ�͕�5��Q�������Q����م�Ք�(���݅�Ё����5����(������ѕ5�ɭ��̠�)�()�չ�ѥ�������ѕ5�ɭ��̠���(������������ɕ��ɸ((����ɭ��̹�������������(����������͕�5������͕�5����ձ��(������͔���������ɕ��ٔ������ɕ��ٔ���(����(����ɭ��̀�mt((�����ѕɕ��٥��̹م�Ք����������٥�������(����������٥�����ѥ�Ց�������٥���������Ց����(��������Ё��ɭ��(�������������Q����م�Ք����ѥ�����Ԝ����ݥ���ܹP���(����������ɭ�Ȁ􁹕܁P�5�ɭ�Ƞ(������������܁P�1��1�С��ѥ�Ց���������Ց���(���������(����������ɭ�ȹ���%���]����ܡ�(����������ѥѱ�聑�٥���������(�������������ѕ��聀�؁��屔����������������홽�еͥ���������푕٥���������𽑥����(����������(������􁕱͔�����ݥ���ܹ5�����(����������ɭ�Ȁ􁹕܁5���5�ɭ�ȡ�(������������ͥѥ���m��٥���������Ց�����٥�����ѥ�Ց�t�(����������ѥѱ�聑�٥���������(�����������������(���������������ѕ��聀�؁��屔􉉅���ɽչ��ɝ�������Ȱ��İ���퍽���荙������������������퉽ɑ�ȵɅ��������홽�еͥ���������푕٥���������𽑥����(��������������ɕ�ѥ��耝ѽ���(������������(����������(����������ɭ�ȹ������������������͕�����٥�����٥����(�������(������������ɭ�Ȥ��(�������������������������������ɭ�Ȥ(����������ɭ��̹��͠���ɭ�Ȥ(�������(�����(����((��������ɭ��̹����Ѡ������������͕���Y��ܤ��(��������͕���Y��ܡ��ɭ��̤(���)�()�չ�ѥ���͕�����٥�����٥�����(��͕���ѕ��٥���م�Ք�􁑕٥��(���������������٥�����ѥ�Ց�������٥���������Ց����(�����������Q����م�Ք����ѥ�����Ԝ����ݥ���ܹP���(�������������Q����܁P�1��1�С��٥�����ѥ�Ց�����٥���������Ց���(����������͕�i�����Ф(����􁕱͔�����ݥ���ܹ5�����(����������͕�
��ѕȡm��٥���������Ց�����٥�����ѥ�Ց�t�(����������͕�i�����Ф(�����(���)�()��幌��չ�ѥ�����э�1���ѥ��̠���(������(��������Ё��ф��݅�Ёٕ�����������1���ѥ��̠����э����������ٕ�������mt����(������٥��̹م�Ք�􁑅ф�ٕ�����́�����ф����mt(��������ѕ5�ɭ��̠�(��􁍅э�������(������٥��̹م�Ք��mt(���)�()�չ�ѥ�����ɵ��Q����Ф��(�������Ф�ɕ��ɸ����(��ɕ��ɸ���܁�є�Ф�ѽ1�����M�ɥ����頵
8��)�()��5�չѕ����幌��������(���݅�Ё����5����(���݅�Ё��э�1���ѥ��̠�(��ѥ��Ȁ�͕�%�ѕ�م����э�1���ѥ��̰�������)��()��U���չѕ���������(������ѥ��Ȥ������%�ѕ�م��ѥ��Ȥ(������������(����������������ɽ䤁��������ɽ䠤(������͔���������ɕ��ٔ������ɕ��ٔ��(���)��(�͍ɥ���((���屔�͍�����(�������ɐ��鑕���������ɑ}}���䤁�(������������������х���)�((��������х���ȁ�(��ݥ�Ѡ������(��������聍�������٠���������(�������������������(�������ɽչ��مȠ���є���ə����Ȥ�(����ɑ�ȵɅ����������(���ٕə���聡������)�((���������������ȁ�(���������聙����(�����൑�ɕ�ѥ��聍��յ��(���������ѕ��聍��ѕ��(�����ѥ�䵍��ѕ��聍��ѕ��(��������������)�((���٥������Ё�(����ൡ������������(���ٕə��ܵ�聅�Ѽ�)�((���٥����ѕ���(���������聙����(���������ѕ��聍��ѕ��(�����ѥ�䵍��ѕ������������ݕ���(������������������(����ɑ�ȵ���ѽ������ͽ����مȠ���є���ɑ�Ȥ�(�����ͽ������ѕ��(���Ʌ�ͥѥ��聉����ɽչ��������)�((���٥����ѕ�顽ٕȁ�(�������ɽչ��مȠ���є���ə����Ȥ�)�((���٥����ѕ����ѥٔ��(�������ɽչ��ɝ����䰀��Ȱ���İ���Ĥ�)�((���٥�����х����(���������聙����(�����൑�ɕ�ѥ��聍��յ��(�����������)�((���х���ɽ܁�(���������聙����(�����ѥ�䵍��ѕ������������ݕ���(���������ѕ��聍��ѕ��)�((���х����������(�����еͥ�������(��������مȠ���є�ѕ�е��ѕ���)�((���х���م�Ք��(�����еͥ�������(�����е������聵���������)�(���屔�