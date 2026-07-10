<template>
  <!--
    QualityPanel 视频质量统计悬浮面板（P0-2）
    - 悬浮在视频画面上方（absolute 定位，右上角）
    - 可收起/展开（收起时只显示一个小图标）
    - 数据源双轨：
      1) localStats prop（父组件传入的浏览器本地 getStats/playbackQuality 实时数据，2s 刷新）
      2) serverStats（组件内部每 5s 调用后端 /video/quality 聚合接口获取）
    - 显示优先级：localStats 字段非 null 用 localStats，否则用 serverStats 补充
    - project_memory: 视频质量统计需实时显示码率、帧率、丢包率
  -->
  <div class="quality-panel" :class="{ collapsed }">
    <div v-if="collapsed" class="quality-collapsed" @click="collapsed = false" title="展开质量统计">
      <el-icon :size="14"><DataLine /></el-icon>
    </div>
    <div v-else class="quality-expanded">
      <div class="quality-header">
        <span class="quality-title">质量统计</span>
        <div class="quality-actions">
          <span v-if="serverOnline === false" class="quality-tag quality-tag-danger" title="后端判定流已断开">离线</span>
          <span v-else-if="serverOnline === true" class="quality-tag quality-tag-ok" title="后端判定流在线">在线</span>
          <el-icon :size="12" class="quality-collapse-btn" @click="collapsed = true" title="收起"><Close /></el-icon>
        </div>
      </div>
      <div class="quality-body">
        <div class="quality-row">
          <span class="quality-label">分辨率</span>
          <span class="quality-value">{{ displayResolution }}</span>
        </div>
        <div class="quality-row">
          <span class="quality-label">码率</span>
          <span class="quality-value" :class="bitrateClass">{{ displayBitrate }}</span>
        </div>
        <div class="quality-row">
          <span class="quality-label">帧率</span>
          <span class="quality-value">{{ displayFps }}</span>
        </div>
        <div class="quality-row">
          <span class="quality-label">丢包率</span>
          <span class="quality-value" :class="lossClass">{{ displayLoss }}</span>
        </div>
        <div class="quality-row quality-row-source">
          <span class="quality-source">{{ dataSourceLabel }}</span>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
// AUTO-FIX-2026-06-29 [P0-2]: 视频质量统计悬浮面板（可收起 + 后端聚合数据源）
import { ref, computed, onMounted, onUnmounted, watch } from 'vue'
import { DataLine, Close } from '@element-plus/icons-vue'
import { videoApi } from '../../api'

const props = defineProps({
  // 父组件传入的本地实时统计（来自 WebRTC getStats / flv.js / hls.js，2s 刷新）
  // 字段：bitrate(fps), fps, lossRate, packetsLost, resolution
  localStats: {
    type: Object,
    default: () => ({}),
  },
  // 终端手机号（用于调用后端 /video/quality?device_id=xxx）
  vehicleId: {
    type: String,
    default: '',
  },
  // 逻辑通道号
  channel: {
    type: [Number, String],
    default: '',
  },
  // 流状态（playing/connecting/error/reconnecting/ended）
  status: {
    type: String,
    default: '',
  },
})

const collapsed = ref(false)
const serverStats = ref(null)
const serverOnline = ref(null) // null=未知, true=在线, false=离线
const serverError = ref(false)
let pollTimer = null

// 合并显示：本地优先，后端补充
const displayBitrate = computed(() => {
  const local = props.localStats?.bitrate
  if (local != null) return `${local} kbps`
  const server = serverStats.value?.bitrate_kbps
  if (server != null && server > 0) return `${Math.round(server)} kbps`
  return '--'
})

const displayFps = computed(() => {
  const local = props.localStats?.fps
  if (local != null) return `${local} fps`
  const server = serverStats.value?.frame_rate
  if (server != null && server > 0) return `${Math.round(server)} fps`
  return '--'
})

const displayLoss = computed(() => {
  const local = props.localStats?.lossRate
  if (local != null) return `${local}%`
  const server = serverStats.value?.loss_rate
  if (server != null) return `${Number(server).toFixed(1)}%`
  return '--'
})

const displayResolution = computed(() => {
  const local = props.localStats?.resolution
  if (local) return local
  return '--'
})

const dataSourceLabel = computed(() => {
  if (serverError.value) return '后端不可达 · 仅本地'
  if (serverStats.value) return '本地 + 后端'
  return '仅本地'
})

const bitrateClass = computed(() => {
  const v = props.localStats?.bitrate ?? serverStats.value?.bitrate_kbps
  if (v == null) return ''
  if (v < 100) return 'quality-warn'
  return 'quality-ok'
})

const lossClass = computed(() => {
  const v = props.localStats?.lossRate ?? serverStats.value?.loss_rate
  if (v == null) return ''
  if (v > 5) return 'quality-warn'
  return 'quality-ok'
})

// 每 5s 调用后端 /video/quality 聚合接口补充全局视角
async function fetchServerStats() {
  if (!props.vehicleId) return
  try {
    const params = { device_id: props.vehicleId }
    if (props.channel !== '' && props.channel != null) params.channel = props.channel
    const res = await videoApi.getQuality(params)
    if (res.code === 0 && res.data && Array.isArray(res.data.streams)) {
      // 取第一条匹配的流（device_id + channel 已在后端过滤）
      if (res.data.streams.length > 0) {
        serverStats.value = res.data.streams[0]
        serverOnline.value = res.data.streams[0].online
      } else {
        // 后端无此流记录——可能流尚未注册到 VideoEngine
        serverStats.value = null
        serverOnline.value = false
      }
    }
    serverError.value = false
  } catch (e) {
    serverError.value = true
    // 静默失败，不影响本地统计显示
  }
}

function startPolling() {
  stopPolling()
  fetchServerStats()
  pollTimer = setInterval(fetchServerStats, 5000)
}

function stopPolling() {
  if (pollTimer) {
    clearInterval(pollTimer)
    pollTimer = null
  }
}

// 流状态变化时重新拉取：playing 状态开始轮询，其他状态停止
watch(
  () => props.status,
  (newStatus) => {
    if (newStatus === 'playing' || newStatus === 'connecting') {
      startPolling()
    } else {
      stopPolling()
    }
  }
)

onMounted(() => {
  if (props.status === 'playing' || props.status === 'connecting') {
    startPolling()
  }
})

onUnmounted(() => {
  stopPolling()
})
</script>

<style scoped>
.quality-panel {
  position: absolute;
  top: 8px;
  right: 8px;
  z-index: 10;
  font-size: 11px;
  pointer-events: auto;
  user-select: none;
}
.quality-collapsed {
  width: 24px;
  height: 24px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: rgba(0, 0, 0, 0.55);
  border-radius: 4px;
  color: #fff;
  cursor: pointer;
  transition: background 0.2s;
}
.quality-collapsed:hover {
  background: rgba(0, 0, 0, 0.75);
}
.quality-expanded {
  min-width: 140px;
  background: rgba(10, 14, 23, 0.82);
  border: 1px solid rgba(255, 255, 255, 0.12);
  border-radius: 6px;
  padding: 6px 8px;
  color: #e5e7eb;
  backdrop-filter: blur(4px);
}
.quality-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 4px;
  padding-bottom: 4px;
  border-bottom: 1px solid rgba(255, 255, 255, 0.08);
}
.quality-title {
  font-weight: 600;
  font-size: 11px;
  color: #f3f4f6;
}
.quality-actions {
  display: flex;
  align-items: center;
  gap: 4px;
}
.quality-tag {
  font-size: 9px;
  padding: 1px 4px;
  border-radius: 3px;
  font-weight: 600;
}
.quality-tag-ok {
  background: rgba(34, 197, 94, 0.25);
  color: #4ade80;
}
.quality-tag-danger {
  background: rgba(239, 68, 68, 0.25);
  color: #f87171;
}
.quality-collapse-btn {
  cursor: pointer;
  color: #9ca3af;
  transition: color 0.2s;
}
.quality-collapse-btn:hover {
  color: #fff;
}
.quality-body {
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.quality-row {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  line-height: 1.4;
}
.quality-label {
  color: #9ca3af;
  font-size: 10px;
}
.quality-value {
  color: #f3f4f6;
  font-family: 'SF Mono', 'Consolas', monospace;
  font-size: 11px;
}
.quality-ok {
  color: #4ade80;
}
.quality-warn {
  color: #fbbf24;
}
.quality-row-source {
  margin-top: 2px;
  padding-top: 2px;
  border-top: 1px dashed rgba(255, 255, 255, 0.06);
  justify-content: flex-end;
}
.quality-source {
  font-size: 9px;
  color: #6b7280;
  font-style: italic;
}
</style>
