<template>
  <div class="page-container">
    <div class="page-header"><h2>疲劳驾驶检测</h2></div>
    <el-card shadow="never">
      <el-form label-width="100px" style="max-width:500px">
        <el-form-item label="驾驶时长(小时)"><el-input-number v-model="form.driving_hours" :min="0" :step="0.5" /></el-form-item>
        <el-form-item><el-button type="primary" @click="check" :loading="loading">检测</el-button></el-form-item>
      </el-form>
    </el-card>
    <el-card shadow="never" style="margin-top:16px" v-if="result">
      <el-descriptions :column="1" border>
        <el-descriptions-item label="是否疲劳"><el-tag :type="result.is_fatigued ? 'danger' : 'success'">{{ result.is_fatigued ? '疲劳' : '正常' }}</el-tag></el-descriptions-item>
        <el-descriptions-item label="驾驶时长">{{ result.driving_hours }}小时</el-descriptions-item>
        <el-descriptions-item label="夜间驾驶"><el-tag :type="result.is_night_time ? 'warning' : 'info'" size="small">{{ result.is_night_time ? '是' : '否' }}</el-tag></el-descriptions-item>
      </el-descriptions>
    </el-card>
  </div>
</template>
<script setup>
import { ref } from 'vue'
import { aiApi } from '../../api'
const form = ref({ driving_hours: 0 })
const loading = ref(false)
const result = ref(null)
async function check() { loading.value = true; try { const res = await aiApi.checkFatigue(form.value); if (res.code === 0) result.value = res.data } catch (e) { console.error(e) } finally { loading.value = false } }
</script>
<style scoped>
.page-container { padding: 24px; }
.page-header { margin-bottom: 20px; }
.page-header h2 { margin: 0; font-size: 18px; color: var(--jte-text); }
</style>
