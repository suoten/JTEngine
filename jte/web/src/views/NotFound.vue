<template>
  <div class="not-found">
    <div class="content">
      <h1>404</h1>
      <p>抱歉，您访问的页面不存在</p>
      <el-button type="primary" @click="$router.push('/')">返回首页</el-button>
    </div>
  </div>
</template>

<script setup>
</script>

<style scoped>
.not-found {
  display: flex;
  align-items: center;
  justify-content: center;
  min-height: 60vh;
}
.content {
  text-align: center;
}
.content h1 {
  font-size: 96px;
  margin: 0;
  color: var(--el-color-primary);
}
.content p {
  color: var(--el-text-color-secondary);
  margin: 12px 0 24px;
}
</style>
