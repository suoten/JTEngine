<template>
  <div class="page-container">
    <div class="page-header"><h2>报警过滤</h2></div>
    <el-card shadow="never">
      <el-form label-width="100px" style="max-width:500px">
        <el-form-item label="报警类型"><el-input v-model="form.alarm_type" placeholder="如: dsm_fatigue" /></el-form-item>
        <el-form-item label="速度"><el-input-number v-model="form.speed" /></el-form-item>
        <el-form-item label="报警次数"><el-input-number v-model="form.alarm_count" :min="0" /></el-form-item>
        <el-form-item><el-button type="primary" @click="analyze" :loading="loading">分析</el-button></el-form-item>
      </el-form>
    </el-card>
    <el-card shadow="never" style="margin-top:16px" v-if="result">
      <el-descriptions :column="1" border>
        <el-descriptions-item label="是否误报"><el-tag :type="result.is_false_alarm ? 'warning' : 'success'">{{ result.is_false_alarm ? '误报' : '有效报警' }}</el-tag></el-descriptions-item>
        <el-descriptions-item label="原因">{{ result.reason || '-' }}</el-descriptions-item>
        <el-descriptions-item label="置信度">{{ ((result.confidence || 0) * 100).toFixed(1) }}%</el-descriptions-item>
      </el-descriptions>
    </el-card>
  </div>
</template>
<script setup>
import { ref } from 'vue'
import { aiApi } from '../../api'
const form = ref({ alarm_type: '', speed: 0, alarm_count: 0 })
const loading = ref(false)
const result = ref(null)
async function analyze() { loading.value = true; try { const res = await aiApi.analyzeAlarm(form.value); if (res.code === 0) result.value = res.data } catch (e) { console.error(e) } finally { loading.value = false } }
</script>
<style scoped>
.page-container { padding: 24px; }
.page-header { margin-bottom: 20px; }
.page-header h2 { margin: 0; font-size: 18px; color: var(--jte-text); }
</style>
