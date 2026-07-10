<template>
  <div class="page-container">
    <div class="page-header"><h2>模块管理</h2></div>
    <el-table :data="modules" stripe>
      <el-table-column prop="name" label="模块名称" min-width="200" />
      <el-table-column prop="enabled" label="状态" width="80"><template #default="{ row }"><el-tag :type="row.enabled ? 'success' : 'info'" size="small">{{ row.enabled ? '已启用' : '未启用' }}</el-tag></template></el-table-column>
    </el-table>
  </div>
</template>
<script setup>
import { ref, onMounted } from 'vue'
import { systemApi } from '../../api'
const modules = ref([])
async function fetchModules() { try { const res = await systemApi.getModules(); if (res.code === 0) modules.value = res.data || [] } catch (e) { console.error(e) } }
onMounted(fetchModules)
</script>
<style scoped>
.page-container { padding: 24px; }
.page-header { margin-bottom: 20px; }
.page-header h2 { margin: 0; font-size: 18px; color: var(--jte-text); }
</style>
