<template>
  <div class="page-container">
    <div class="page-header"><h2>AI 配置</h2></div>

    <el-alert type="info" :closable="false" style="margin-bottom: 16px;">
      <template #title>
        <span style="font-size: 13px;">
          AI 模块需要配置 LLM 接口才能使用。推荐 DeepSeek（性价比最高），也支持 Ollama 本地部署和通义千问。
          配置后需 <b>重启后端服务</b> 生效。
        </span>
      </template>
    </el-alert>

    <el-card shadow="never" v-loading="loading">
      <template #header><span style="font-weight: 600;">LLM 接口配置</span></template>
      <el-form :model="form" label-width="160px" style="max-width: 640px;">
        <!-- DeepSeek -->
        <el-divider content-position="left">DeepSeek（推荐）</el-divider>
        <el-form-item label="DeepSeek API Key">
          <el-input v-model="form.deepseek_api_key" type="password" show-password placeholder="sk-xxxxxxxxxxxxxxxx" />
          <div class="form-hint">在 <a href="https://platform.deepseek.com/api_keys" target="_blank">platform.deepseek.com</a> 申请 API Key</div>
        </el-form-item>
        <el-form-item label="DeepSeek API URL">
          <el-input v-model="form.deepseek_url" placeholder="https://api.deepseek.com/v1" />
        </el-form-item>

        <!-- Ollama -->
        <el-divider content-position="left">Ollama（本地部署）</el-divider>
        <el-form-item label="Ollama 服务地址">
          <el-input v-model="form.ollama_url" placeholder="http://127.0.0.1:11434" />
          <div class="form-hint">本地部署 <a href="https://ollama.com" target="_blank">Ollama</a>，无需 API Key，离线可用</div>
        </el-form-item>

        <!-- 通义千问 -->
        <el-divider content-position="left">通义千问（备选）</el-divider>
        <el-form-item label="通义千问 API Key">
          <el-input v-model="form.qwen_api_key" type="password" show-password placeholder="sk-xxxxxxxx" />
          <div class="form-hint">在 <a href="https://dashscope.console.aliyun.com" target="_blank">阿里云 DashScope</a> 申请</div>
        </el-form-item>

        <!-- 通用参数 -->
        <el-divider content-position="left">通用参数</el-divider>
        <el-form-item label="调用超时（秒）">
          <el-input-number v-model="form.timeout_seconds" :min="1" :max="60" />
        </el-form-item>
        <el-form-item label="失败重试次数">
          <el-input-number v-model="form.retry_count" :min="0" :max="10" />
        </el-form-item>
        <el-form-item label="启用结果缓存">
          <el-switch v-model="form.cache_enabled" />
        </el-form-item>
        <el-form-item label="缓存 TTL（分钟）" v-if="form.cache_enabled">
          <el-input-number v-model="form.cache_ttl_minutes" :min="5" :max="1440" />
        </el-form-item>

        <el-form-item>
          <el-button type="primary" @click="save" :loading="saving">保存配置</el-button>
        </el-form-item>
      </el-form>
    </el-card>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { configApi } from '../../api'
import { ElMessage } from 'element-plus'

const loading = ref(false)
const saving = ref(false)
const form = ref({
  deepseek_api_key: '',
  deepseek_url: 'https://api.deepseek.com/v1',
  ollama_url: 'http://127.0.0.1:11434',
  qwen_api_key: '',
  timeout_seconds: 3,
  retry_count: 3,
  cache_enabled: true,
  cache_ttl_minutes: 60,
})

async function load() {
  loading.value = true
  try {
    const res = await configApi.getAIConfig()
    if (res.code === 0 && res.data) {
      Object.assign(form.value, res.data)
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
    const res = await configApi.updateAIConfig(form.value)
    if (res.code === 0) {
      ElMessage.success(res.message || 'AI 配置已保存')
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
