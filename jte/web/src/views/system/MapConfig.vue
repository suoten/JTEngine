<template>
  <div class="page-container">
    <div class="page-header"><h2>地图配置</h2></div>

    <el-card shadow="never" v-loading="loading">
      <template #header><span style="font-weight: 600;">地图引擎选择</span></template>
      <el-form label-width="120px" style="max-width: 640px;">
        <el-form-item label="地图引擎">
          <el-radio-group v-model="form.provider">
            <el-radio value="tianditu">天地图</el-radio>
            <el-radio value="amap">高德地图</el-radio>
            <el-radio value="baidu">百度地图</el-radio>
          </el-radio-group>
        </el-form-item>
      </el-form>
    </el-card>

    <el-card shadow="never" style="margin-top: 16px;" v-loading="loading">
      <template #header><span style="font-weight: 600;">API Key 配置</span></template>
      <el-form :model="form" label-width="140px" style="max-width: 640px;">
        <!-- 天地图 -->
        <el-form-item label="天地图 API Key">
          <el-input v-model="form.tianditu_key" placeholder="天地图 API Key" />
          <div class="form-hint">在 <a href="https://console.tianditu.gov.cn/" target="_blank">天地图控制台</a> 申请</div>
        </el-form-item>

        <!-- 高德 -->
        <el-divider content-position="left">高德地图</el-divider>
        <el-form-item label="高德 API Key">
          <el-input v-model="form.amap_key" placeholder="高德地图 API Key" />
          <div class="form-hint">在 <a href="https://lbs.amap.com/dev/key/app" target="_blank">高德开放平台</a> 申请</div>
        </el-form-item>
        <el-form-item label="高德安全密钥">
          <el-input v-model="form.amap_security" placeholder="高德安全密钥（可选）" />
        </el-form-item>

        <!-- 百度 -->
        <el-divider content-position="left">百度地图</el-divider>
        <el-form-item label="百度 API Key">
          <el-input v-model="form.baidu_key" placeholder="百度地图 API Key" />
          <div class="form-hint">在 <a href="https://lbsyun.baidu.com/apiconsole/key" target="_blank">百度地图开放平台</a> 申请</div>
        </el-form-item>

        <el-form-item>
          <el-button type="primary" @click="save" :loading="saving">保存地图配置</el-button>
        </el-form-item>
      </el-form>
    </el-card>

    <el-alert type="info" :closable="false" style="margin-top: 12px;">
      <template #title>
        <span style="font-size: 13px;">地图 Key 也可在 jte.yaml 配置文件的 map 段中设置，修改后需重启服务生效。</span>
      </template>
    </el-alert>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { configApi } from '../../api'
import { ElMessage } from 'element-plus'

const loading = ref(false)
const saving = ref(false)
const form = ref({
  provider: 'tianditu',
  tianditu_key: '',
  amap_key: '',
  amap_security: '',
  baidu_key: '',
})

async function load() {
  loading.value = true
  try {
    const res = await configApi.getMapConfig()
    if (res.code === 0 && res.data) {
      Object.assign(form.value, res.data)
    } else if (res.provider) {
      Object.assign(form.value, res)
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
    const res = await configApi.updateMapConfig(form.value)
    if (res.code === 0) {
      ElMessage.success(res.message || '地图配置已保存')
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
.form-hint { font-size: 12px; color: var(--jte-text-muted); margin-top: 4px; }
.form-hint a { color: var(--jte-accent); }
</style>
