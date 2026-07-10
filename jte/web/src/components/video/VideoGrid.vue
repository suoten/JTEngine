<template>
  <!--
    VideoGrid 视频分屏布局容器（P1-5 16 画面非激活暂停优化）
    - 支持 1/4/9/16 画面布局
    - 16 画面模式下：非激活画面显示占位，不渲染 video 元素，不拉流（减少 16 路并发压力）
    - 点击占位激活该画面，触发父组件重新拉流
    - 通过 scoped slot 让父组件渲染每个格子的具体内容（video + QualityPanel + Toolbar）
    - slot props: { stream, active } —— active=false 时父组件应跳过播放器初始化
  -->
  <el-row :gutter="16">
    <el-col :span="gridSpan" v-for="stream in streams" :key="stream.id">
      <div
        class="grid-cell"
        :class="{ 'grid-cell-inactive': isInactive(stream.id) }"
        @click="handleCellClick(stream.id)"
      >
        <!-- 16 画面非激活占位：不渲染 slot 内容，父组件不会初始化播放器 -->
        <div v-if="isInactive(stream.id)" class="grid-placeholder">
          <el-icon :size="32" color="var(--jte-text-muted)"><VideoCamera /></el-icon>
          <p class="placeholder-text">{{ stream.vehicle_id }} · 通道{{ stream.channel }}</p>
          <p class="placeholder-hint">点击激活此画面</p>
        </div>
        <!-- 激活画面：渲染 slot 内容（video + 面板 + 工具栏） -->
        <slot v-else :stream="stream" :active="true" />
      </div>
    </el-col>
  </el-row>
</template>

<script setup>
// AUTO-FIX-2026-06-29 [P1-5]: 16 画面非激活暂停优化
// project_memory: 网络质量连续 3 次丢包>5% 或码率<100kbps 时自动切换到子码流
// 16 画面全开会导致浏览器/网络压力过大，非激活画面暂停拉流可显著降低 CPU 和带宽占用
import { computed } from 'vue'
import { VideoCamera } from '@element-plus/icons-vue'

const props = defineProps({
  streams: { type: Array, default: () => [] },
  layoutMode: { type: Number, default: 4 },
  // 当前激活的 stream id（16 画面模式下只有激活画面会拉流）
  activeId: { type: String, default: '' },
})

const emit = defineEmits(['activate'])

const gridSpan = computed(() => {
  if (props.layoutMode === 1) return 24
  if (props.layoutMode === 4) return 12
  if (props.layoutMode === 9) return 8
  return 6 // 16 画面 → 4×4
})

// 16 画面模式下，非 activeId 的画面标记为 inactive
// 1/4/9 画面模式下所有画面都激活（性能可承受）
function isInactive(streamId) {
  if (props.layoutMode !== 16) return false
  if (!props.activeId) return false // 无激活选中时全部激活（首次加载）
  return props.activeId !== streamId
}

function handleCellClick(streamId) {
  // 仅 16 画面模式下点击非激活格子时触发激活
  if (props.layoutMode === 16 && isInactive(streamId)) {
    emit('activate', streamId)
  }
}
</script>

<style scoped>
.grid-cell {
  margin-bottom: 16px;
}
.grid-cell-inactive {
  cursor: pointer;
  border: 1px dashed var(--jte-border, #e5e7eb);
  border-radius: 8px;
  transition: border-color 0.2s, background 0.2s;
}
.grid-cell-inactive:hover {
  border-color: var(--el-color-primary, #409eff);
  background: rgba(64, 158, 255, 0.04);
}
.grid-placeholder {
  height: 220px;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 6px;
}
.placeholder-text {
  margin: 4px 0 0 0;
  color: var(--jte-text-muted, #9ca3af);
  font-size: 13px;
  font-weight: 500;
}
.placeholder-hint {
  margin: 0;
  color: var(--jte-text-muted, #9ca3af);
  font-size: 11px;
  opacity: 0.7;
}
</style>
