<template>
  <div>
    <div class="page-header">
      <div style="display: flex; align-items: center; justify-content: space-between;">
        <div>
          <h1 class="page-title">角色管理</h1>
          <p style="color: var(--jte-text-muted); font-size: 13px; margin-top: 4px;">管理系统角色与权限分配</p>
        </div>
        <el-button type="primary" size="small" @click="openCreateDialog">添加角色</el-button>
      </div>
    </div>
    <div class="page-content">
      <el-table :data="roles" stripe v-loading="loading">
        <el-table-column prop="name" label="角色名称" width="180" />
        <el-table-column prop="label" label="显示名称" width="150" />
        <el-table-column label="权限" min-width="400">
          <template #default="{ row }">
            <el-tag v-for="p in (row.permissions || [])" :key="p" size="small" style="margin: 2px;">{{ permissionLabels[p] || p }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="user_count" label="用户数" width="100" />
        <el-table-column label="操作" width="160" fixed="right">
          <template #default="{ row }">
            <el-button type="primary" link size="small" @click="openEditDialog(row)">编辑</el-button>
            <el-button v-if="!isBuiltinRole(row.name)" type="danger" link size="small" @click="deleteRole(row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>

      <!-- 创建/编辑角色弹窗 -->
      <el-dialog v-model="dialogVisible" :title="editingRole ? '编辑角色' : '添加角色'" width="600px">
        <el-form :model="roleForm" label-width="100px">
          <el-form-item label="角色名称">
            <el-input v-model="roleForm.name" :disabled="!!editingRole" placeholder="如 operator_cn" />
          </el-form-item>
          <el-form-item label="显示名称">
            <el-input v-model="roleForm.label" placeholder="如 自定义操作员" />
          </el-form-item>
          <el-form-item label="权限分配">
            <el-checkbox-group v-model="roleForm.permissions">
              <el-checkbox v-for="perm in allPermissions" :key="perm.value" :label="perm.value" style="margin-bottom: 4px;">
              {{ perm.label }}
              </el-checkbox>
            </el-checkbox-group>
          </el-form-item>
        </el-form>
        <template #footer>
          <el-button @click="dialogVisible = false">取消</el-button>
          <el-button type="primary" @click="submitRole" :loading="submitting">确定</el-button>
        </template>
      </el-dialog>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { systemApi } from '../../api'
import { ElMessage, ElMessageBox } from 'element-plus'

const permissionLabels = {
  monitor: '实时监控', device: '设备管理', vehicle: '车辆管理', alarm: '报警中心',
  track: '轨迹回放', video: '视频监控', command: '指令下发', report: '报表中心',
  cascade: '平台级联', user_manage: '用户管理', role_manage: '角色管理',
  system: '系统配置', module: '模块管理', license: '授权管理', audit_log: '系统日志', ai: 'AI助手',
}

const allPermissions = Object.entries(permissionLabels).map(([value, label]) => ({ value, label }))

const builtinRoles = new Set(['super_admin', 'admin', 'operator', 'readonly'])
function isBuiltinRole(name) {
  return builtinRoles.has(name)
}

// 兜底角色列表（后端不可达时显示）
const fallbackRoles = [
  { name: 'super_admin', label: '超级管理员', permissions: Object.keys(permissionLabels), user_count: 1 },
  { name: 'admin', label: '管理员', permissions: ['monitor','device','vehicle','alarm','track','video','command','report','cascade','user_manage','role_manage','system','audit_log','ai'], user_count: 0 },
  { name: 'operator', label: '操作员', permissions: ['monitor','device','vehicle','alarm','track','video','command','report','ai'], user_count: 0 },
  { name: 'readonly', label: '只读用户', permissions: ['monitor','alarm','track','video','report','ai'], user_count: 0 },
]

const roles = ref([])
const loading = ref(false)
const dialogVisible = ref(false)
const editingRole = ref(null)
const submitting = ref(false)
const roleForm = ref({ name: '', label: '', permissions: [] })

async function fetchRoles() {
  loading.value = true
  try {
    const res = await systemApi.getRoles()
    if (res.code === 0 && res.data) {
      roles.value = Array.isArray(res.data) ? res.data : (res.data.roles || [])
    } else {
      roles.value = fallbackRoles
    }
  } catch (e) {
    console.error('Failed to fetch roles:', e)
    roles.value = fallbackRoles
  } finally {
    loading.value = false
  }
}

function openCreateDialog() {
  editingRole.value = null
  roleForm.value = { name: '', label: '', permissions: [] }
  dialogVisible.value = true
}

function openEditDialog(row) {
  editingRole.value = row
  roleForm.value = {
    name: row.name,
    label: row.label || '',
    permissions: [...(row.permissions || [])],
  }
  dialogVisible.value = true
}

async function submitRole() {
  if (!roleForm.value.name || !roleForm.value.label) {
    ElMessage.warning('角色名称和显示名称不能为空')
    return
  }
  submitting.value = true
  try {
    if (editingRole.value) {
      await systemApi.updateRole(editingRole.value.id || editingRole.value.name, {
        label: roleForm.value.label,
        permissions: roleForm.value.permissions,
      })
      ElMessage.success('角色已更新')
    } else {
      await systemApi.createRole({
        name: roleForm.value.name,
        label: roleForm.value.label,
        permissions: roleForm.value.permissions,
      })
      ElMessage.success('角色已创建')
    }
    dialogVisible.value = false
    await fetchRoles()
  } catch (e) {
    ElMessage.error(e.message || '操作失败')
  } finally {
    submitting.value = false
  }
}

async function deleteRole(row) {
  try {
    await ElMessageBox.confirm(
      `确认删除角色「${row.label || row.name}」？关联的用户将需要重新分配角色。`,
      '删除确认',
      { type: 'warning', confirmButtonText: '确认删除', cancelButtonText: '取消' }
    )
    await systemApi.deleteRole(row.id || row.name)
    ElMessage.success('角色已删除')
    await fetchRoles()
  } catch (e) {
    if (e !== 'cancel' && e?.message !== 'cancel') {
      ElMessage.error(e?.message || '删除失败')
    }
  }
}

onMounted(fetchRoles)
</script>
