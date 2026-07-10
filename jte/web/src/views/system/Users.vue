<template>
  <div class="page-container">
    <div class="page-header"><h2>用户管理</h2><el-button type="primary" @click="showAdd = true">添加用户</el-button></div>
    <el-table :data="users" stripe>
      <el-table-column prop="username" label="用户名" width="160" />
      <el-table-column prop="role" label="角色" width="100" />
      <el-table-column prop="created_at" label="创建时间" min-width="160" />
      <el-table-column label="操作" width="120" fixed="right"><template #default="{ row }"><el-button type="danger" link size="small" @click="deleteUser(row)">删除</el-button></template></el-table-column>
    </el-table>
    <el-dialog v-model="showAdd" title="添加用户" width="400px">
      <el-form label-width="80px">
        <el-form-item label="用户名"><el-input v-model="userForm.username" /></el-form-item>
        <el-form-item label="密码"><el-input v-model="userForm.password" type="password" /></el-form-item>
        <el-form-item label="角色"><el-select v-model="userForm.role"><el-option label="管理员" value="admin" /><el-option label="操作员" value="operator" /><el-option label="只读" value="viewer" /></el-select></el-form-item>
      </el-form>
      <template #footer><el-button @click="showAdd = false">取消</el-button><el-button type="primary" @click="addUser">确定</el-button></template>
    </el-dialog>
  </div>
</template>
<script setup>
import { ref, onMounted } from 'vue'
import { systemApi } from '../../api'
const users = ref([])
const showAdd = ref(false)
const userForm = ref({ username: '', password: '', role: 'operator' })
async function fetchUsers() { try { const res = await systemApi.getUsers(); if (res.code === 0) users.value = res.data || [] } catch (e) { console.error(e) } }
async function addUser() { try { await systemApi.createUser(userForm.value); showAdd.value = false; await fetchUsers() } catch (e) { console.error(e) } }
async function deleteUser(row) { try { await systemApi.deleteUser(row.id); await fetchUsers() } catch (e) { console.error(e) } }
onMounted(fetchUsers)
</script>
<style scoped>
.page-container { padding: 24px; }
.page-header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 20px; }
.page-header h2 { margin: 0; font-size: 18px; color: var(--jte-text); }
</style>
