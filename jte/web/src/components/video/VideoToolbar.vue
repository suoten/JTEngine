<template>
  <!--
    VideoToolbar 视频控件栏组件（P0-3 关键帧 loading + P1-4 码流切换）
    从 Video.vue 抽出，减少主文件体积。
    - 关键帧按钮：点击后显示"请求中..."loading，至少 2s 后恢复（P0-3）
    - 码流切换：主/子码流切换，emit 事件让父组件刷新流（P1-4）
    - 通道切换、PTZ、截图、RTP 模式、全屏、停止：emit 事件给父组件
  -->
  <div class="video-toolbar">
    <div class="toolbar-left">
      <span class="vehicle-id">{{ stream.vehicle_id }}</span>
      <el-select
        :model-value="stream.channel"
        @change="(val) => emit('switch-channel', val)"
        size="small"
        style="width: 90px; margin-left: 8px;"
      >
        <el-option v-for="ch in 16" :key="ch" :label="`通道${ch}`" :value="ch" />
      </el-select>
      <el-button
        size="small"
        text
        :loading="stream.switching"
        :title="stream.stream_type === 1 ? '当前子码流，点击切换为主码流' : '当前主码流，点击切换为子码流'"
        @click="emit('switch-stream-type', stream.stream_type === 1 ? 0 : 1)"
      >
        <template v-if="stream.switching">切换中...</template>
        <template v-else>{{ stream.stream_type === 1 ? '子码流' : '主码流' }}</template>
      </el-button>
      <!-- AUTO-FIX-2026-07-02: 显示当前码流信息（分辨率/码率） -->
      <span v-if="streamInfo" class="stream-info">{{ streamInfo }}</span>
      <span v-if="stream.status === 'playing'" class="status-dot status-playing">● 播放中</span>
      <span v-else-if="stream.status === 'connecting'" class="status-dot status-connecting">● 连接中</span>
      <span v-else-if="stream.status === 'reconnecting'" class="status-dot status-connecting">● 重连中</span>
      <span v-else-if="stream.status === 'error'" class="status-dot status-error">● 错误</span>
      <span v-else-if="stream.status === 'ended'" class="status-dot status-ended">● 已结束</span>
    </div>

    <div class="toolbar-right">
      <el-popover trigger="click" placement="top" :width="200">
        <template #reference>
          <el-button size="small" text>云台</el-button>
        </template>
        <div class="ptz-panel">
          <div class="ptz-row">
            <el-button :icon="ArrowUpBold" circle size="small" @mousedown="emit('ptz-control', 1)" @mouseup="emit('ptz-control', 0)" />
          </div>
          <div class="ptz-row">
            <el-button :icon="ArrowLeftBold" circle size="small" @mousedown="emit('ptz-control', 3)" @mouseup="emit('ptz-control', 0)" />
            <el-button circle size="small" @click="emit('ptz-control', 0)">停</el-button>
            <el-button :icon="ArrowRightBold" circle size="small" @mousedown="emit('ptz-control', 4)" @mouseup="emit('ptz-control', 0)" />
          </div>
          <div class="ptz-row">
            <el-button :icon="ArrowDownBold" circle size="small" @mousedown="emit('ptz-control', 2)" @mouseup="emit('ptz-control', 0)" />
          </div>
          <div class="ptz-row" style="margin-top: 8px;">
            <el-button size="small" @mousedown="emit('ptz-control', 9)" @mouseup="emit('ptz-control', 0)">变倍+</el-button>
            <el-button size="small" @mousedown="emit('ptz-control', 10)" @mouseup="emit('ptz-control', 0)">变倍-</el-button>
          </div>
          <div class="ptz-row" style="margin-top: 4px;">
            <span style="font-size: 12px;">速度</span>
            <el-slider :model-value="ptzSpeed" @update:model-value="(v) => emit('update:ptz-speed', v)" :min="1" :max="7" :step="1" style="width: 100px;" />
          </div>
        </div>
      </el-popover>
      <el-button size="small" text @click="emit('screenshot')">截图</el-button>
      <!--
        P0-3 关键帧手动触发按钮
        - 点击后显示"请求中..."loading，至少 2s 后恢复
        - 失败时 toast 提示
      -->
      <el-button
        size="small"
        text
        :loading="keyframeLoading"
        @click="requestKeyFrame"
        :title="keyframeLoading ? '请求中...' : '请求关键帧（画面马赛克/黑屏时使用）'"
      >
        <template v-if="keyframeLoading">请求中...</template>
        <template v-else>关键帧</template>
      </el-button>
      <el-button
        size="small"
        text
        @click="emit('toggle-stream-mode')"
        :title="stream.rtp_mode === 'tcp' ? '当前TCP模式（公网/NAT环境），点击切UDP' : '当前UDP模式，点击切TCP（公网/NAT环境）'"
      >{{ stream.rtp_mode === 'tcp' ? 'TCP' : 'UDP' }}</el-button>
      <el-button size="small" text @click="emit('toggle-fullscreen')">全屏</el-button>
      <el-button type="danger" size="small" text @click="emit('stop')">停止</el-button>
    </div>
  </div>
</template>

<script setup>
// AUTO-FIX-2026-06-29 [P0-3]: 关键帧按钮 loading 状态（至少 2s）+ 控件栏抽离
import { ref, computed, onUnmounted } from 'vue'
import { ArrowUpBold, ArrowDownBold, ArrowLeftBold, ArrowRightBold } from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'
import { videoApi } from '../../api'

const props = defineProps({
  stream: { type: Object, required: true },
  ptzSpeed: { type: Number, default: 4 },
  // AUTO-FIX-2026-07-02: 本地质量统计（分辨率/码率/帧率），从父组件传入
  localStats: { type: Object, default: null },
})

// AUTO-FIX-2026-07-02: 格式化码流信息（分辨率 + 码率）
const streamInfo = computed(() => {
  const stats = props.localStats
  if (!stats || !props.stream || props.stream.status !== 'playing') return ''
  const parts = []
  // 兼容 resolution 字符串 ("1920x1080") 和 width/height 数字
  if (stats.resolution) {
    parts.push(stats.resolution)
  } else if (stats.width && stats.height) {
    parts.push(`${stats.width}×${stats.height}`)
  }
  if (stats.bitrate != null) {
    parts.push(`${Math.round(stats.bitrate)}kbps`)
  }
  if (stats.fps != null) {
    parts.push(`${stats.fps}fps`)
  }
  return parts.join(' · ')
})

const emit = defineEmits([
  'switch-channel',
  'switch-stream-type',
  'ptz-control',
  'screenshot',
  'toggle-stream-mode',
  'toggle-fullscreen',
  'stop',
  'update:ptz-speed',
])

// P0-3: 关键帧 loading 状态——点击后显示"请求中..."，至少 2s 后恢复
const keyframeLoading = ref(false)
let keyframeStartTime = 0
let keyframeTimer = null
const MIN_LOADING_MS = 2000 // 最少显示 2s loading，避免闪烁

async function requestKeyFrame() {
  if (keyframeLoading.value) return
  keyframeLoading.value = true
  keyframeStartTime = Date.now()

  let success = false
  try {
    const res = await videoApi.keyframe({
      vehicle_id: props.stream.vehicle_id,
      logic_channel: props.stream.channel,
    })
    if (res.code === 0) {
      success = true
      // 不立即 toast 成功，等 loading 结束后提示
    } else {
      ElMessage.error(res.message || '关键帧请求失败')
    }
  } catch (e) {
    console.error('Request keyframe failed:', e)
    ElMessage.error('关键帧请求失败')
  }

  // 确保至少显示 2s loading（用户规范：2s 后恢复）
  const elapsed = Date.now() - keyframeStartTime
  const remaining = Math.max(0, MIN_LOADING_MS - elapsed)
  if (keyframeTimer) clearTimeout(keyframeTimer)
  keyframeTimer = setTimeout(() => {
    keyframeLoading.value = false
    keyframeTimer = null
    if (success) {
      ElMessage.success('关键帧请求已发送，画面将在 2s 内恢复清晰')
    }
  }, remaining)
}

onUnmounted(() => {
  if (keyframeTimer) {
    clearTimeout(keyframeTimer)
    keyframeTimer = null
  }
})
</script>

<style scoped>
.video-toolbar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-top: 8px;
  gap: 8px;
  flex-wrap: wrap;
}
.toolbar-left {
  display: flex;
  align-items: center;
  gap: 4px;
  flex-wrap: wrap;
}
.toolbar-right {
  display: flex;
  gap: 4px;
  align-items: center;
  flex-wrap: wrap;
}
.vehicle-id {
  font-size: 13px;
  font-weight: 500;
}
.status-dot {
  font-size: 11px;
  margin-left: 8px;
}
.status-playing { color: #22c55e; }
.status-connecting { color: #f59e0b; }
.status-error { color: #ef4444; }
.status-ended { color: #9ca3af; }
/* AUTO-FIX-2026-07-02: 码流信息样式 */
.stream-info {
  font-size: 11px;
  color: var(--jte-text-muted, #909399);
  margin-left: 4px;
  font-family: monospace;
}
.ptz-panel {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 4px;
}
.ptz-row {
  display: flex;
  gap: 8px;
  align-items: center;
  justify-content: center;
}
</style>
