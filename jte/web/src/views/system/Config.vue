<template>
  <div class="page-container">
    <div class="page-header"><h2>配置管理</h2></div>
    <el-card shadow="never">
      <el-form label-width="120px" style="max-width:600px">
        <el-form-item label="最大设备数"><el-input-number v-model="config.max_devices" :min="1" /></el-form-item>
        <el-form-item label="心跳间隔(秒)"><el-input-number v-model="config.heartbeat_interval" :min="10" /></el-form-item>
        <el-form-item label="心跳超时(秒)"><el-input-number v-model="config.heartbeat_timeout" :min="30" /></el-form-item>
        <el-form-item label="API端口"><el-input-number v-model="config.api_port" :min="1" :max="65535" /></el-form-item>
        <el-form-item><el-button type="primary" @click="saveConfig">保存配置</el-button></el-form-item>
      </el-form>
    </el-card>
  </div>
</template>
<script setup>
import { ref, onMounted } from 'vue'
import { systemApi } from '../../api'
import { ElMessage } from 'element-plus'
const config = ref({ max_devices: 20, heartbeat_interval: 60, heartbeat_timeout: 180, api_port: 8080 })
async function fetchConfig() { try { const res = await systemApi.getConfig(); if (res.code === 0 && res.data) config.value = { ...config.value, ...res.data } } catch (e) { console.error(e) } }
async function saveConfig() { try { await systemApi.updateConfig(config.value); ElMessage.success('配置已保存') } catch (e) { ElMessage.error('保存失败') } }
onMounted(fetchConfig)
</script>
<style scoped>
.page-container { padding: 24px; }
.page-header { margin-bottom: 20px; }
.page-header h2 { margin: 0; font-size: 18px; color: var(--jte-text); }
</style>
