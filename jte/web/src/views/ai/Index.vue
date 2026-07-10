<template>
  <div>
    <div class="page-header">
      <div>
        <h1 class="page-title">AI智能分析</h1>
        <p style="color: var(--jte-text-muted); font-size: 13px; margin-top: 4px;">
          DeepSeek驱动的智能分析与自然语言交互
        </p>
      </div>
    </div>

    <div class="page-content">
      <el-row :gutter="20" style="margin-bottom: 24px;">
        <el-col :span="6">
          <el-card shadow="never" class="ai-stat-card" @click="$router.push('/ai/alarm-filter')">
            <el-icon :size="36" color="#f59e0b"><Warning /></el-icon>
            <div style="margin-top: 12px;">
              <div style="font-size: 16px; font-weight: 600;">报警过滤</div>
              <div style="font-size: 12px; color: var(--jte-text-muted); margin-top: 4px;">智能识别误报，减少无效报警</div>
            </div>
          </el-card>
        </el-col>
        <el-col :span="6">
          <el-card shadow="never" class="ai-stat-card" @click="$router.push('/ai/driver-fatigue')">
            <el-icon :size="36" color="#ef4444"><User /></el-icon>
            <div style="margin-top: 12px;">
              <div style="font-size: 16px; font-weight: 600;">疲劳驾驶</div>
              <div style="font-size: 12px; color: var(--jte-text-muted); margin-top: 4px;">实时检测疲劳驾驶行为</div>
            </div>
          </el-card>
        </el-col>
        <el-col :span="6">
          <el-card shadow="never" class="ai-stat-card" @click="$router.push('/ai/risk-scoring')">
            <el-icon :size="36" color="#3b82f6"><DataAnalysis /></el-icon>
            <div style="margin-top: 12px;">
              <div style="font-size: 16px; font-weight: 600;">风险评分</div>
              <div style="font-size: 12px; color: var(--jte-text-muted); margin-top: 4px;">综合风险评估与预警</div>
            </div>
          </el-card>
        </el-col>
        <el-col :span="6">
          <el-card shadow="never" class="ai-stat-card" @click="$router.push('/ai/nlp')">
            <el-icon :size="36" color="#8b5cf6"><ChatDotRound /></el-icon>
            <div style="margin-top: 12px;">
              <div style="font-size: 16px; font-weight: 600;">智能助手</div>
              <div style="font-size: 12px; color: var(--jte-text-muted); margin-top: 4px;">自然语言查询与报表生成</div>
            </div>
          </el-card>
        </el-col>
      </el-row>

      <el-row :gutter="20">
        <el-col :span="12">
          <el-card shadow="never">
            <template #header><span style="font-weight: 500;">今日报警统计</span></template>
            <div style="padding: 16px;">
              <el-row :gutter="16">
                <el-col :span="8">
                  <div style="text-align: center;">
                    <div style="font-size: 28px; font-weight: 700; color: #f59e0b;">--</div>
                    <div style="font-size: 12px; color: var(--jte-text-muted);">总报警</div>
                  </div>
                </el-col>
                <el-col :span="8">
                  <div style="text-align: center;">
                    <div style="font-size: 28px; font-weight: 700; color: #ef4444;">--</div>
                    <div style="font-size: 12px; color: var(--jte-text-muted);">有效报警</div>
                  </div>
                </el-col>
                <el-col :span="8">
                  <div style="text-align: center;">
                    <div style="font-size: 28px; font-weight: 700; color: #22c55e;">--</div>
                    <div style="font-size: 12px; color: var(--jte-text-muted);">已过滤误报</div>
                  </div>
                </el-col>
              </el-row>
            </div>
          </el-card>
        </el-col>
        <el-col :span="12">
          <el-card shadow="never">
            <template #header><span style="font-weight: 500;">AI模型状态</span></template>
            <div style="padding: 16px;">
              <el-descriptions :column="1" size="small">
                <el-descriptions-item label="规则引擎"><el-tag type="success" size="small">运行中</el-tag></el-descriptions-item>
                <el-descriptions-item label="DeepSeek API"><el-tag type="info" size="small">未配置</el-tag></el-descriptions-item>
                <el-descriptions-item label="报警过滤模型"><el-tag type="success" size="small">已加载</el-tag></el-descriptions-item>
                <el-descriptions-item label="NLP模型"><el-tag type="info" size="small">未配置</el-tag></el-descriptions-item>
              </el-descriptions>
            </div>
          </el-card>
        </el-col>
      </el-row>
    </div>
  </div>
</template>

<script setup>
import { Warning, User, DataAnalysis, ChatDotRound } from '@element-plus/icons-vue'
</script>

<style scoped>
.ai-stat-card {
  cursor: pointer;
  text-align: center;
  padding: 20px;
  transition: transform 0.2s, box-shadow 0.2s;
}
.ai-stat-card:hover {
  transform: translateY(-4px);
  box-shadow: 0 8px 24px rgba(0,0,0,0.12);
}
</style>
