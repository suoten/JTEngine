<template>
  <div>
    <div class="page-header">
      <div style="display: flex; align-items: center; justify-content: space-between;">
        <div>
          <h1 class="page-title">电子围栏管理</h1>
          <p style="color: var(--jte-text-muted); font-size: 13px; margin-top: 4px;">
            管理圆形 / 矩形 / 多边形围栏，支持地图预览
          </p>
        </div>
        <div style="display: flex; gap: 8px;">
          <el-input v-model="searchKeyword" placeholder="搜索名称" size="small" style="width: 180px;" clearable>
            <template #prefix><el-icon><Search /></el-icon></template>
          </el-input>
          <el-button size="small" @click="fetchList">
            <el-icon><Refresh /></el-icon>
          </el-button>
          <el-button type="primary" size="small" @click="openAdd">
            <el-icon><Plus /></el-icon><span style="margin-left: 4px;">新增围栏</span>
          </el-button>
        </div>
      </div>
    </div>

    <div class="page-content">
      <el-card shadow="never">
        <template #header>
          <span style="font-weight: 500; font-size: 14px;">围栏列表</span>
        </template>
        <el-table :data="filteredList" style="width: 100%" size="small" v-loading="loading">
          <el-table-column prop="name" label="名称" min-width="160" />
          <el-table-column label="类型" width="100">
            <template #default="{ row }">
              <el-tag size="small" :type="typeTagType(row.type)">{{ typeLabel(row.type) }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column label="参数" min-width="220">
            <template #default="{ row }">
              <span style="font-size: 12px; color: var(--jte-text-muted);">{{ describeParams(row) }}</span>
            </template>
          </el-table-column>
          <el-table-column label="生效时间" min-width="280">
            <template #default="{ row }">
              <span style="font-size: 12px;">{{ formatTime(row.effective_from) }} 至 {{ formatTime(row.effective_to) }}</span>
            </template>
          </el-table-column>
          <el-table-column label="状态" width="90">
            <template #default="{ row }">
              <el-tag size="small" :type="row.enabled ? 'success' : 'info'">{{ row.enabled ? '启用' : '停用' }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column label="操作" width="160" fixed="right">
            <template #default="{ row }">
              <el-button type="primary" link size="small" @click="openEdit(row)">编辑</el-button>
              <el-button type="danger" link size="small" @click="removeFence(row)">删除</el-button>
            </template>
          </el-table-column>
        </el-table>
      </el-card>
    </div>

    <!-- 新增/编辑弹窗 -->
    <el-dialog v-model="showDialog" :title="editing ? '编辑围栏' : '新增围栏'" width="640px" @closed="onDialogClosed">
      <el-form :model="form" label-width="90px" size="default">
        <el-form-item label="名称">
          <el-input v-model="form.name" placeholder="请输入围栏名称" />
        </el-form-item>
        <el-form-item label="类型">
          <el-radio-group v-model="form.type" @change="onTypeChange">
            <el-radio-button value="circle">圆形</el-radio-button>
            <el-radio-button value="rectangle">矩形</el-radio-button>
            <el-radio-button value="polygon">多边形</el-radio-button>
          </el-radio-group>
        </el-form-item>

        <!-- 圆形：中心点 + 半径 -->
        <template v-if="form.type === 'circle'">
          <el-form-item label="中心经度">
            <el-input-number v-model="form.params.center.lng" :precision="6" :step="0.0001" :min="-180" :max="180" controls-position="right" style="width: 220px;" />
          </el-form-item>
          <el-form-item label="中心纬度">
            <el-input-number v-model="form.params.center.lat" :precision="6" :step="0.0001" :min="-90" :max="90" controls-position="right" style="width: 220px;" />
          </el-form-item>
          <el-form-item label="半径(米)">
            <el-input-number v-model="form.params.radius" :min="1" :step="100" controls-position="right" style="width: 220px;" />
          </el-form-item>
        </template>

        <!-- 矩形：西南角 + 东北角 -->
        <template v-if="form.type === 'rectangle'">
          <el-form-item label="西南角">
            <div style="display: flex; gap: 8px;">
              <el-input-number v-model="form.params.southwest.lng" :precision="6" :step="0.0001" :min="-180" :max="180" controls-position="right" placeholder="经度" style="width: 160px;" />
              <el-input-number v-model="form.params.southwest.lat" :precision="6" :step="0.0001" :min="-90" :max="90" controls-position="right" placeholder="纬度" style="width: 160px;" />
            </div>
          </el-form-item>
          <el-form-item label="东北角">
            <div style="display: flex; gap: 8px;">
              <el-input-number v-model="form.params.northeast.lng" :precision="6" :step="0.0001" :min="-180" :max="180" controls-position="right" placeholder="经度" style="width: 160px;" />
              <el-input-number v-model="form.params.northeast.lat" :precision="6" :step="0.0001" :min="-90" :max="90" controls-position="right" placeholder="纬度" style="width: 160px;" />
            </div>
          </el-form-item>
        </template>

        <!-- 多边形：点列表 -->
        <template v-if="form.type === 'polygon'">
          <el-form-item label="顶点列表">
            <div style="display: flex; flex-direction: column; gap: 8px; width: 100%;">
              <div v-for="(p, idx) in form.params.points" :key="idx" style="display: flex; gap: 8px; align-items: center;">
                <span style="font-size: 12px; color: var(--jte-text-muted); width: 40px;">点{{ idx + 1 }}</span>
                <el-input-number v-model="p.lng" :precision="6" :step="0.0001" :min="-180" :max="180" controls-position="right" placeholder="经度" style="width: 160px;" />
                <el-input-number v-model="p.lat" :precision="6" :step="0.0001" :min="-90" :max="90" controls-position="right" placeholder="纬度" style="width: 160px;" />
                <el-button type="danger" link size="small" :disabled="form.params.points.length <= 3" @click="removePoint(idx)">
                  <el-icon><Delete /></el-icon>
                </el-button>
              </div>
              <el-button size="small" style="width: 100px;" @click="addPoint">
                <el-icon><Plus /></el-icon><span style="margin-left: 4px;">添加顶点</span>
              </el-button>
            </div>
          </el-form-item>
        </template>

        <el-form-item label="生效时间">
          <el-date-picker
            v-model="form.effectiveRange"
            type="datetimerange"
            range-separator="至"
            start-placeholder="开始时间"
            end-placeholder="结束时间"
            value-format="YYYY-MM-DDTHH:mm:ssZ"
            style="width: 100%;"
          />
        </el-form-item>
        <el-form-item label="启用">
          <el-switch v-model="form.enabled" />
        </el-form-item>
      </el-form>

      <!-- 地图预览（可选，天地图） -->
      <div style="margin-top: 12px; border: 1px solid var(--jte-border); border-radius: 8px; overflow: hidden;">
        <div ref="mapRef" style="width: 100%; height: 220px; background: var(--jte-surface-2);"></div>
        <div v-if="!mapLoaded" style="padding: 12px; text-align: center; font-size: 12px; color: var(--jte-text-muted);">
          地图预览需配置天地图 Key（在系统配置中设置）
        </div>
      </div>

      <template #footer>
        <el-button @click="showDialog = false">取消</el-button>
        <el-button type="primary" :loading="submitting" @click="submit">确定</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, nextTick, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { geofenceApi, configApi } from '../api'

const list = ref([])
const loading = ref(false)
const searchKeyword = ref('')

const showDialog = ref(false)
const editing = ref(false)
const submitting = ref(false)

const mapRef = ref(null)
const mapLoaded = ref(false)
let mapInstance = null
let mapShape = null
let mapConfig = { tianditu_key: '' }

// 默认表单结构
function defaultForm() {
  return {
    id: null,
    name: '',
    type: 'circle',
    params: {
      center: { lng: 116.40, lat: 39.90 },
      radius: 500,
      southwest: { lng: 116.30, lat: 39.80 },
      northeast: { lng: 116.50, lat: 40.00 },
      points: [
        { lng: 116.40, lat: 39.90 },
        { lng: 116.45, lat: 39.90 },
        { lng: 116.45, lat: 39.95 },
        { lng: 116.40, lat: 39.95 },
      ],
    },
    effectiveRange: [],
    enabled: true,
  }
}

const form = ref(defaultForm())

const filteredList = computed(() => {
  if (!searchKeyword.value) return list.value
  const kw = searchKeyword.value.toLowerCase()
  return list.value.filter(f => f.name?.toLowerCase().includes(kw))
})

async function fetchList() {
  loading.value = true
  try {
    const res = await geofenceApi.getList({ limit: 200 })
    const data = res?.data || res || []
    list.value = (Array.isArray(data) ? data : (data.items || [])).map(normalizeFence)
  } catch (e) {
    list.value = []
  } finally {
    loading.value = false
  }
}

// 后端字段兼容
function normalizeFence(f) {
  return {
    id: f.id ?? f.fence_id,
    name: f.name || '',
    type: f.type || 'circle',
    params: f.params || parseLegacyParams(f),
    effective_from: f.effective_from || f.start_time,
    effective_to: f.effective_to || f.end_time,
    enabled: f.enabled ?? true,
  }
}

// 兼容旧版扁平字段
function parseLegacyParams(f) {
  if (f.type === 'circle') return { center: { lng: f.longitude ?? f.center_lng, lat: f.latitude ?? f.center_lat }, radius: f.radius ?? 500 }
  if (f.type === 'rectangle') return { southwest: { lng: f.sw_lng, lat: f.sw_lat }, northeast: { lng: f.ne_lng, lat: f.ne_lat } }
  if (f.type === 'polygon') return { points: f.points || [] }
  return {}
}

function typeLabel(t) {
  return { circle: '圆形', rectangle: '矩形', polygon: '多边形' }[t] || t
}

function typeTagType(t) {
  return { circle: '', rectangle: 'success', polygon: 'warning' }[t] || 'info'
}

function describeParams(f) {
  const p = f.params || {}
  if (f.type === 'circle') return `中心(${fmtNum(p.center?.lng)}, ${fmtNum(p.center?.lat)}) 半径 ${p.radius ?? 0}m`
  if (f.type === 'rectangle') return `西南(${fmtNum(p.southwest?.lng)}, ${fmtNum(p.southwest?.lat)}) 东北(${fmtNum(p.northeast?.lng)}, ${fmtNum(p.northeast?.lat)})`
  if (f.type === 'polygon') return `${p.points?.length || 0} 个顶点`
  return ''
}

function fmtNum(n) {
  if (n == null || isNaN(Number(n))) return '-'
  return Number(n).toFixed(4)
}

function formatTime(t) {
  if (!t) return '-'
  return new Date(t).toLocaleString('zh-CN')
}

function openAdd() {
  editing.value = false
  form.value = defaultForm()
  showDialog.value = true
}

function openEdit(row) {
  editing.value = true
  form.value = {
    id: row.id,
    name: row.name,
    type: row.type,
    params: JSON.parse(JSON.stringify(row.params)),
    effectiveRange: [row.effective_from, row.effective_to].filter(Boolean),
    enabled: row.enabled,
  }
  showDialog.value = true
}

function onTypeChange() {
  // 切换类型时保留已填参数（结构已存在）
  nextTick(drawShape)
}

function addPoint() {
  form.value.params.points.push({ lng: 116.40, lat: 39.90 })
}

function removePoint(idx) {
  if (form.value.params.points.length <= 3) return
  form.value.params.points.splice(idx, 1)
}

async function submit() {
  if (!form.value.name.trim()) {
    ElMessage.warning('请输入围栏名称')
    return
  }
  submitting.value = true
  try {
    const payload = {
      name: form.value.name.trim(),
      type: form.value.type,
      params: form.value.params,
      effective_from: form.value.effectiveRange?.[0] || null,
      effective_to: form.value.effectiveRange?.[1] || null,
      enabled: form.value.enabled,
    }
    if (editing.value) {
      await geofenceApi.update(form.value.id, payload)
      ElMessage.success('围栏已更新')
    } else {
      await geofenceApi.create(payload)
      ElMessage.success('围栏已创建')
    }
    showDialog.value = false
    await fetchList()
  } catch (e) {
    ElMessage.error(editing.value ? '更新失败' : '创建失败')
  } finally {
    submitting.value = false
  }
}

async function removeFence(row) {
  try {
    await ElMessageBox.confirm(`确认删除围栏「${row.name}」吗？`, '删除确认', {
      type: 'warning',
      confirmButtonText: '删除',
      cancelButtonText: '取消',
    })
    await geofenceApi.delete(row.id)
    ElMessage.success('围栏已删除')
    await fetchList()
  } catch (e) {
    // 用户取消或请求失败
  }
}

// ===== 地图预览（天地图，可选） =====
async function loadMapConfig() {
  try {
    const res = await configApi.getMapConfig()
    mapConfig = res?.data || res || {}
  } catch (e) {
    mapConfig = { tianditu_key: '' }
  }
}

async function initMap() {
  if (!mapRef.value || mapInstance) return
  await loadMapConfig()
  if (!window.T) {
    const script = document.createElement('script')
    script.src = `https://api.tianditu.gov.cn/api?v=4.0&tk=${mapConfig.tianditu_key || ''}`
    script.onload = () => buildMap()
    script.onerror = () => { mapLoaded.value = false }
    document.head.appendChild(script)
  } else {
    buildMap()
  }
}

function buildMap() {
  if (!window.T || !mapRef.value) return
  try {
    mapInstance = new window.T.Map(mapRef.value)
    mapInstance.centerAndZoom(new window.T.LngLat(116.40, 39.90), 11)
    mapLoaded.value = true
    drawShape()
  } catch (e) {
    mapLoaded.value = false
  }
}

function drawShape() {
  if (!mapInstance || !window.T) return
  if (mapShape) { mapInstance.removeOverLay(mapShape); mapShape = null }
  const p = form.value.params
  if (form.value.type === 'circle' && p.center?.lng != null) {
    mapShape = new window.T.Circle(new window.T.LngLat(p.center.lng, p.center.lat), p.radius || 500, {
      color: '#6366f1', weight: 2, opacity: 0.8, fillColor: '#6366f1', fillOpacity: 0.2,
    })
    mapInstance.addOverLay(mapShape)
    mapInstance.centerAndZoom(new window.T.LngLat(p.center.lng, p.center.lat), 13)
  } else if (form.value.type === 'rectangle' && p.southwest?.lng != null) {
    mapShape = new window.T.Rectangle(
      new window.T.LngLat(p.southwest.lng, p.southwest.lat),
      new window.T.LngLat(p.northeast.lng, p.northeast.lat),
      { color: '#6366f1', weight: 2, opacity: 0.8, fillColor: '#6366f1', fillOpacity: 0.2 }
    )
    mapInstance.addOverLay(mapShape)
  } else if (form.value.type === 'polygon' && p.points?.length >= 3) {
    const points = p.points.map(pt => new window.T.LngLat(pt.lng, pt.lat))
    mapShape = new window.T.Polygon(points, {
      color: '#6366f1', weight: 2, opacity: 0.8, fillColor: '#6366f1', fillOpacity: 0.2,
    })
    mapInstance.addOverLay(mapShape)
  }
}

function onDialogClosed() {
  if (mapInstance) { mapInstance = null; mapShape = null }
  if (mapRef.value) mapRef.value.innerHTML = ''
  mapLoaded.value = false
}

// 弹窗打开后初始化地图，参数变化时重绘
watch(showDialog, (v) => {
  if (v) nextTick(() => { initMap() })
})
watch(() => form.value.params, () => drawShape(), { deep: true })

onMounted(fetchList)
</script>
