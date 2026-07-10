<template>
  <div class="page-container">
    <div class="page-header"><h2>风险评分</h2></div>
    <el-card shadow="never">
      <el-form label-width="100px" style="max-width:500px">
        <el-form-item label="报警次数"><el-input-number v-model="form.alarm_count" :min="0" /></el-form-item>
        <el-form-item label="超速次数"><el-input-number v-model="form.overspeed_count" :min="0" /></el-form-item>
        <el-form-item label="疲劳次数"><el-input-number v-model="form.fatigue_count" :min="0" /></el-form-item>
        <el-form-item label="异常次数"><el-input-number v-model="form.abnormal_count" :min="0" /></el-form-item>
        <el-form-item><el-button type="primary" @click="score" :loading="loading">评估</el-button></el-form-item>
      </el-form>
    </el-card>
    <el-card shadow="never" style="margin-top:16px" v-if="result">
      <div style="text-align:center;padding:20px">
        <el-progress type="dashboard" :percentage="result.score || 0" :color="scoreColor" :width="160">
          <template #default><div style="font-size:28px;font-weight:700">{{ result.score || 0 }}</div><div style="font-size:12px;color:var(--jte-text-muted)">{{ result.level }}</div></template>
        </el-progress>
      </div>
      <el-descriptions :column="1" border v-if="result.factors && result.factors.length">
        <el-descriptions-item label="风险因素"><el-tag v-for="f in result.factors" :key="f" size="small" style="margin:2px">{{ f }}</el-tag></el-descriptions-item>
      </el-descriptions>
    </el-card>
  </div>
</template>
<script setup>
import { ref, computed } from 'vue'
import { aiApi } from '../../api'
const form = ref({ alarm_count: 0, overspeed_count: 0, fatigue_count: 0, abnormal_count: 0 })
const loading = ref(false)
const result = ref(null)
const scoreColor = computed(() => { if (!result.value) return '#67c23a'; const s = result.value.score || 0; if (s >= 70) return '#f56c6c'; if (s >= 40) return '#e6a23c'; return '#67c23a' })
async function score() { loading.value = true; try { const res = await aiApi.getRiskScore(form.value); if (res.code === 0) result.value = res.data } catch (e) { console.error(e) } finally { loading.value = false } }
</script>
<style scoped>
.page-container { padding: 24px; }
.page-header { margin-bottom: 20px; }
.page-header h2 { margin: 0; font-size: 18px; color: var(--jte-text); }
</style>
