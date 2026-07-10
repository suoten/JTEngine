<template>
  <div class="page-container">
    <div class="page-header"><h2>系统设置</h2></div>
    <el-card shadow="never">
      <el-descriptions :column="2" border>
        <el-descriptions-item label="版本">1.0.0</el-descriptions-item>
        <el-descriptions-item label="运行时间">-</el-descriptions-item>
        <el-descriptions-item label="在线设备">-</el-descriptions-item>
        <el-descriptions-item label="已加载模块">-</el-descriptions-item>
      </el-descriptions>
    </el-card>
  </div>
</template>
<script setup>
</script>
<style scoped>
.page-container { padding: 24px; }
.page-header { margin-bottom: 20px; }
.page-header h2 { margin: 0; font-size: 18px; color: var(--jte-text); }
</style>
