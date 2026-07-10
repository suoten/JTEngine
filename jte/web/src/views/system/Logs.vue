<template>
  <div>
    <div class="page-header">
      <h1 class="page-title">系统日志</h1>
      <p style="color: var(--jte-text-muted); font-size: 13px; margin-top: 4px;">审计日志与操作记录</p>
    </div>
    <div class="page-content">
      <div style="display: flex; gap: 12px; margin-bottom: 16px;">
        <el-input v-model="searchUser" placeholder="搜索用户" size="small" style="width: 200px;" clearable />
        <el-select v-model="filterAction" size="small" style="width: 150px;" clearable placeholder="操作类型">
          <el-option label="登录" value="login" />
          <el-option label="配置修改" value="config_update" />
          <el-option label="用户管理" value="user_manage" />
          <el-option label="授权操作" value="license" />
          <el-option label="指令下发" value="command" />
        </el-select>
        <el-date-picker v-model="dateRange" type="daterange" size="small" range-separator="至" start-placeholder="开始日期" end-placeholder="结束日期" />
      </div>
      <el-table :data="logs" stripe>
        <el-table-column prop="time" label="时间" width="180" />
        <el-table-column prop="user" label="用户" width="120" />
        <el-table-column prop="action" label="操作" width="150" />
        <el-table-column prop="detail" label="详情" min-width="300" />
        <el-table-column prop="ip" label="IP地址" width="140" />
      </el-table>
      <div style="margin-top: 16px; text-align: center;">
        <el-pagination :total="0" :page-size="20" layout="prev, pager, next" />
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref } from 'vue'

const searchUser = ref('')
const filterAction = ref('')
const dateRange = ref(null)
const logs = ref([])
</script>