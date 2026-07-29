<template>
  <div class="page-container">
    <div class="page-header">
      <h2>用户管理</h2>
      <el-button type="primary" @click="showAdd = true">添加用户</el-button>
    </div>

    <div style="margin-bottom: 16px; display: flex; gap: 12px; flex-wrap: wrap;">
      <el-input
        v-model="searchKeyword"
        placeholder="搜索用户名"
        size="small"
        style="width: 200px;"
        clearable
        @keyup.enter="fetchUsers"
      />
      <el-button size="small" type="primary" @click="fetchUsers">查询</el-button>
    </div>

    <el-table :data="filteredUsers" stripe v-loading="loading">
      <el-table-column prop="username" label="用户名" width="160" />
      <el-table-column prop="role" label="角色" width="120">
        <template #default="{ row }">
          <el-tag size="small" :type="roleTagType(row.role)">{{ roleLabel(row.role) }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="email" label="邮箱" min-width="180" />
      <el-table-column prop="created_at" label="创建时间" min-width="160">
        <template #default="{ row }">{{ formatTime(row.created_at) }}</template>
      </el-table-column>
      <el-table-column label="状态" width="80">
        <template #default="{ row }">
          <el-tag v-if="row.disabled" size="small" type="danger">禁用</el-tag>
          <el-tag v-else size="small" type="success">正常</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="操作" width="180" fixed="right">
        <template #default="{ row }">
          <el-button type="primary" link size="small" @click="openEdit(row)">编辑</el-button>
          <el-button type="warning" link size="small" @click="toggleDisable(row)">
            {{ row.disabled ? '启用' : '禁用' }}
          </el-button>
          <el-button type="danger" link size="small" @click="deleteUser(row)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>

    <!-- 添加用户弹窗 -->
    <el-dialog v-model="showAdd" title="添加用户" width="440px">
      <el-form label-width="80px">
        <el-form-item label="用户名">
          <el-input v-model="userForm.username" placeholder="输入用户名" />
        </el-form-item>
        <el-form-item label="密码">
          <el-input v-model="userForm.password" type="password" placeholder="至少6位密码" />
        </el-form-item>
        <el-form-item label="角色">
          <el-select v-model="userForm.role" style="width: 100%;">
            <el-option label="超级管理员" value="super_admin" />
            <el-option label="管理员" value="admin" />
            <el-option label="操作员" value="operator" />
            <el-option label="只读用户" value="readonly" />
          </el-select>
        </el-form-item>
        <el-form-item label="邮箱">
          <el-input v-model="userForm.email" placeholder="可选" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="showAdd = false">取消</el-button>
        <el-button type="primary" @click="addUser" :loading="submitting">确定</el-button>
      </template>
    </el-dialog>

    <!-- 编辑用户弹窗 -->
    <el-dialog v-model="showEdit" title="编辑用户" width="440px">
      <el-form label-width="80px">
        <el-form-item label="用户名">
          <el-input :model-value="editForm.username" disabled />
        </el-form-item>
        <el-form-item label="角色">
          <el-select v-model="editForm.role" style="width: 100%;">
            <el-option label="超级管理员" value="super_admin" />
            <el-option label="管理员" value="admin" />
            <el-option label="操作员" value="operator" />
            <el-option label="只读用户" value="readonly" />
          </el-select>
        </el-form-item>
        <el-form-item label="邮箱">
          <el-input v-model="editForm.email" placeholder="可选" />
        </el-form-item>
        <el-form-item label="新密码">
          <el-input v-model="editForm.password" type="password" placeholder="留空则不修改密码" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="showEdit = false">取消</el-button>
        <el-button type="primary" @click="saveEdit" :loading="submitting">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { ref, computed, onMounted } from 'vue'
import { systemApi } from '../../api'
import { ElMessage, ElMessageBox } from 'element-plus'

const users = ref([])
const loading = ref(false)
const submitting = ref(false)
const showAdd = ref(false)
const showEdit = ref(false)
const searchKeyword = ref('')
const userForm = ref({ username: '', password: '', role: 'operator', email: '' })
const editForm = ref({ id: '', username: '', role: '', email: '', password: '' })

const roleLabels = {
  super_admin: '超级管理员', admin: '管理员', operator: '操作员', readonly: '只读用户',
}

function roleLabel(role) {
  return roleLabels[role] || role || '-'
}

function roleTagType(role) {
  const map = { super_admin: 'danger', admin: 'warning', operator: '', readonly: 'info' }
  return map[role] || 'info'
}

function formatTime(t) {
  if (!t) return '-'
  return new Date(t).toLocaleString('zh-CN', { hour12: false })
}

const filteredUsers = computed(() => {
  if (!searchKeyword.value) return users.value
  return users.value.filter(u =>
    u.username?.toLowerCase().includes(searchKeyword.value.toLowerCase())
  )
})

async function fetchUsers() {
  loading.value = true
  try {
    const res = await systemApi.getUsers()
    // FIXED-2026-07-24: 后端 ListUsers 返回 {users,total} 格式（无 code 字段），
    // 兼容两种响应格式：标准 {code:0,data:{users}} 和直接 {users,total}
    if (res.code === 0) {
      const _raw = res.data?.users || res.data
      users.value = Array.isArray(_raw) ? _raw : []
    } else if (res.users) {
      users.value = res.users
    }
  } catch (e) {
    console.error('Failed to fetch users:', e)
    ElMessage.error('加载用户列表失败，请检查网络或稍后重试')
  } finally {
    loading.value = false
  }
}

async function addUser() {
  if (!userForm.value.username || !userForm.value.password) {
    ElMessage.warning('用户名和密码不能为空')
    return
  }
  if (userForm.value.password.length < 6) {
    ElMessage.warning('密码长度不能少于6位')
    return
  }
  submitting.value = true
  try {
    await systemApi.createUser(userForm.value)
    ElMessage.success('用户创建成功')
    showAdd.value = false
    userForm.value = { username: '', password: '', role: 'operator', email: '' }
    await fetchUsers()
  } catch (e) {
    ElMessage.error(e.message || '创建用户失败')
  } finally {
    submitting.value = false
  }
}

function openEdit(row) {
  editForm.value = {
    id: row.id,
    username: row.username || '',
    role: row.role || 'readonly',
    email: row.email || '',
    password: '',
  }
  showEdit.value = true
}

async function saveEdit() {
  submitting.value = true
  try {
    const data = {
      role: editForm.value.role,
      email: editForm.value.email,
    }
    if (editForm.value.password) {
      data.password = editForm.value.password
    }
    await systemApi.updateUser(editForm.value.id, data)
    ElMessage.success('用户信息已更新')
    showEdit.value = false
    await fetchUsers()
  } catch (e) {
    ElMessage.error(e.message || '更新失败')
  } finally {
    submitting.value = false
  }
}

async function toggleDisable(row) {
  try {
    await systemApi.updateUser(row.id, { disabled: !row.disabled })
    ElMessage.success(row.disabled ? '用户已启用' : '用户已禁用')
    await fetchUsers()
  } catch (e) {
    ElMessage.error(e.message || '操作失败')
  }
}

async function deleteUser(row) {
  try {
    await ElMessageBox.confirm(
      `确认删除用户「${row.username}」？此操作不可恢复。`,
      '删除确认',
      { type: 'warning', confirmButtonText: '确认删除', cancelButtonText: '取消' }
    )
    await systemApi.deleteUser(row.id)
    ElMessage.success('用户已删除')
    await fetchUsers()
  } catch (e) {
    if (e !== 'cancel' && e?.message !== 'cancel') {
      ElMessage.error(e?.message || '删除失败')
    }
  }
}

onMounted(fetchUsers)
</script>

<style scoped>
.page-container { padding: 24px; }
.page-header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 20px; }
.page-header h2 { margin: 0; font-size: 18px; color: var(--jte-text); }
</style>
