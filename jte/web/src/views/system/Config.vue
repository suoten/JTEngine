<template>
  <div class="page-container">
    <div class="page-header"><h2>系统参数</h2></div>

    <el-card shadow="never" v-loading="loading">
      <template #header><span style="font-weight: 600;">网关参数</span></template>
      <el-form :model="form" label-width="140px" style="max-width: 560px;">
        <el-form-item label="最大设备数">
          <el-input-number v-model="form.max_devices" :min="1" :max="100000" />
        </el-form-item>
        <el-form-item label="心跳间隔（秒）">
          <el-input-number v-model="form.heartbeat_interval" :min="5" :max="300" />
        </el-form-item>
        <el-form-item label="心跳超时（秒）">
          <el-input-number v-model="form.heartbeat_timeout" :min="30" :max="600" />
        </el-form-item>
        <el-form-item label="API 端口">
          <el-input-number v-model="form.api_port" :min="1" :max="65535" />
        </el-form-item>
        <el-form-item>
          <el-button type="primary" @click="save" :loading="saving">保存配置</el-button>
        </el-form-item>
      </el-form>
    </el-card>

    <el-alert type="info" :closable="false" style="margin-top: 12px;">
      <template #title>
        <span style="font-size: 13px;">
          💡 其他配置：<a href="#/system/map-config" style="color: var(--jte-accent);">地图配置</a> ·
          <a href="#/system/menu-config" style="color: var(--jte-accent);">菜单配置</a> ·
          <a href="#/system/ai-config" style="color: var(--jte-accent);">AI 配置</a>
        </span>
      </template>
    </el-alert>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { systemApi } from '../../api'
import { ElMessage } from 'element-plus'

const loading = ref(false)
const saving = ref(false)
const form = ref({
  max_devices: 10000,
  heartbeat_interval: 30,
  heartbeat_timeout: 180,
  api_port: 8090,
})

async function load() {
  loading.value = true
  try {
    const res = await systemApi.getConfig()
    if (res.code === 0 && res.data) {
      const d = res.data
      form.value.max_devices = d.gateway?.max_devices || d.max_devices || 10000
      form.value.heartbeat_interval = d.gateway?.heartbeat_interval || 30
      form.value.heartbeat_timeout = d.gateway?.heartbeat_timeout || 180
      form.value.api_port = d.api?.port || 8090
    }
  } catch (e) {
    console.error(e)
  } finally {
    loading.value = false
  }
}

async function save() {
  saving.value = true
  try {
    const res = await systemApi.updateConfig({
      gateway: {
        max_devices: form.value.max_devices,
        heartbeat_interval: form.value.heartbeat_interval,
        heartbeat_timeout: form.value.heartbeat_timeout,
      },
      api: { port: form.value.api_port },
    })
    if (res.code === 0) {
      ElMessage.success('系统配置已保存')
    } else {
      ElMessage.error(res.message || '保存失败')
    }
  } catch (e) {
    ElMessage.error(e.message || '保存失败')
  } finally {
    saving.value = false
  }
}

onMounted(load)
</script>

<style scoped>
.page-container { padding: 24px; }
.page-header { margin-bottom: 20px; }
.page-header h2 { margin: 0; font-size: 18px; color: var(--jte-text); }
</style>
