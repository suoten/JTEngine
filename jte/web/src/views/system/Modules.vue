<template>
  <div class="page-container">
    <div class="page-header">
      <h2>模块管理</h2>
    </div>

    <!-- FIXED-2026-07-24: 分类说明卡片，让用户理解三类模块的区别 -->
    <el-alert type="info" :closable="false" style="margin-bottom: 16px;">
      <template #title>
        <span style="font-weight: 600;">模块分类说明</span>
      </template>
      <div style="display: flex; gap: 24px; flex-wrap: wrap; margin-top: 4px;">
        <span><el-tag type="success" size="small">核心免费</el-tag> 开源版自带，无需授权</span>
        <span><el-tag type="warning" size="small">授权模块</el-tag> 需试用或购买授权后启用</span>
        <span><el-tag type="info" size="small">基础设施</el-tag> 随引擎自动启用，不单独授权</span>
      </div>
    </el-alert>

    <el-table :data="sortedModules" stripe>
      <!-- 分类标签 -->
      <el-table-column label="分类" width="110">
        <template #default="{ row }">
          <el-tag
            :type="categoryTag(row.category)"
            size="small"
          >{{ categoryLabel(row.category) }}</el-tag>
        </template>
      </el-table-column>
      <!-- 模块名称：中文名主标题 + 英文名副标题 + 描述 -->
      <el-table-column label="模块名称" min-width="320">
        <template #default="{ row }">
          <div style="display: flex; flex-direction: column; gap: 2px;">
            <span style="font-weight: 500;">{{ row.display_name || row.name }}</span>
            <span style="font-size: 12px; color: var(--jte-text-muted);">{{ row.name }}</span>
            <span v-if="row.description" style="font-size: 12px; color: var(--jte-text-muted); margin-top: 2px;">{{ row.description }}</span>
          </div>
        </template>
      </el-table-column>
      <!-- 状态 -->
      <el-table-column prop="enabled" label="状态" width="100">
        <template #default="{ row }">
          <el-tag :type="row.enabled ? 'success' : 'info'" size="small">
            {{ row.enabled ? '已启用' : '未启用' }}
          </el-tag>
        </template>
      </el-table-column>
    </el-table>
  </div>
</template>
<script setup>
import { ref, computed, onMounted } from 'vue'
import { systemApi } from '../../api'

const modules = ref([])

async function fetchModules() {
  try {
    const res = await systemApi.getModules()
    if (res.code === 0) modules.value = Array.isArray(res.data) ? res.data : (res.data?.items || [])
  } catch (e) { console.error(e) }
}

// 按分类排序：核心免费 → 授权模块 → 基础设施
const categoryOrder = { core: 0, licensed: 1, infrastructure: 2 }
const sortedModules = computed(() => {
  return [...modules.value].sort((a, b) => {
    const ca = categoryOrder[a.category] ?? 99
    const cb = categoryOrder[b.category] ?? 99
    return ca - cb
  })
})

function categoryTag(cat) {
  return { core: 'success', licensed: 'warning', infrastructure: 'info' }[cat] || 'info'
}

function categoryLabel(cat) {
  return { core: '核心免费', licensed: '授权模块', infrastructure: '基础设施' }[cat] || cat
}

onMounted(fetchModules)
</script>
<style scoped>
.page-container { padding: 24px; }
.page-header { margin-bottom: 20px; }
.page-header h2 { margin: 0; font-size: 18px; color: var(--jte-text); }
</style>
