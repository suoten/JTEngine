<template>
  <div>
    <div class="page-header">
      <h1 class="page-title">角色管理</h1>
      <p style="color: var(--jte-text-muted); font-size: 13px; margin-top: 4px;">管理系统角色与权限分配</p>
    </div>
    <div class="page-content">
      <el-table :data="roles" stripe>
        <el-table-column prop="name" label="角色名称" width="180" />
        <el-table-column prop="label" label="显示名称" width="150" />
        <el-table-column label="权限" min-width="400">
          <template #default="{ row }">
            <el-tag v-for="p in row.permissions" :key="p" size="small" style="margin: 2px;">{{ permissionLabels[p] || p }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="userCount" label="用户数" width="100" />
      </el-table>
    </div>
  </div>
</template>

<script setup>
import { ref } from 'vue'

const permissionLabels = {
  monitor: '实时监控', device: '设备管理', vehicle: '车辆管理', alarm: '报警中心',
  track: '轨迹回放', video: '视频监控', command: '指令下发', report: '报表中心',
  cascade: '平台级联', user_manage: '用户管理', role_manage: '角色管理',
  system: '系统配置', module: '模块管理', license: '授权管理', audit_log: '系统日志', ai: 'AI助手',
}

const roles = ref([
  { name: 'super_admin', label: '超级管理员', permissions: Object.keys(permissionLabels), userCount: 1 },
  { name: 'admin', label: '管理员', permissions: ['monitor','device','vehicle','alarm','track','video','command','report','cascade','user_manage','role_manage','system','audit_log','ai'], userCount: 0 },
  { name: 'operator', label: '操作员', permissions: ['monitor','device','vehicle','alarm','track','video','command','report','ai'], userCount: 0 },
  { name: 'readonly', label: '只读用户', permissions: ['monitor','alarm','track','video','report','ai'], userCount: 0 },
])
</script>