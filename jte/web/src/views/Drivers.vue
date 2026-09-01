<template>
  <div>
    <div class="page-header">
      <div style="display: flex; align-items: center; justify-content: space-between;">
        <div>
          <h1 class="page-title">驾驶员管理</h1>
          <p style="color: var(--jte-text-muted); font-size: 13px; margin-top: 4px;">
            驾驶员信息与驾驶证档案
          </p>
        </div>
        <div style="display: flex; gap: 8px;">
          <el-input v-model="searchKeyword" placeholder="搜索姓名/手机号/车牌" size="small" style="width: 220px;" clearable>
            <template #prefix><el-icon><Search /></el-icon></template>
          </el-input>
          <el-select v-model="filterStatus" placeholder="状态" size="small" style="width: 110px;" clearable>
            <el-option label="在职" value="active" />
            <el-option label="停职" value="inactive" />
          </el-select>
          <el-button size="small" @click="fetchList">
            <el-icon><Refresh /></el-icon>
          </el-button>
          <el-button type="primary" size="small" @click="openAdd">
            <el-icon><Plus /></el-icon><span style="margin-left: 4px;">新增驾驶员</span>
          </el-button>
        </div>
      </div>
    </div>

    <div class="page-content">
      <el-card shadow="never">
        <template #header>
          <span style="font-weight: 500; font-size: 14px;">驾驶员列表</span>
        </template>
        <el-table :data="filteredList" style="width: 100%" size="small" v-loading="loading">
          <el-table-column prop="name" label="姓名" width="120" />
          <el-table-column prop="id_card" label="身份证号" width="180">
            <template #default="{ row }">
              <span style="font-family: monospace; font-size: 12px;">{{ maskIdCard(row.id_card) }}</span>
            </template>
          </el-table-column>
          <el-table-column prop="phone" label="手机号" width="140">
            <template #default="{ row }">
              <span style="font-family: monospace; font-size: 12px;">{{ row.phone }}</span>
            </template>
          </el-table-column>
          <el-table-column prop="license_no" label="驾驶证号" width="180">
            <template #default="{ row }">
              <span style="font-family: monospace; font-size: 12px;">{{ row.license_no }}</span>
            </template>
          </el-table-column>
          <el-table-column prop="vehicle_plate" label="所属车辆" width="130">
            <template #default="{ row }">
              <el-tag v-if="row.vehicle_plate" size="small">{{ row.vehicle_plate }}</el-tag>
              <span v-else style="color: var(--jte-text-muted); font-size: 12px;">未分配</span>
            </template>
          </el-table-column>
          <el-table-column label="状态" width="100">
            <template #default="{ row }">
              <el-tag size="small" :type="row.status === 'active' ? 'success' : 'info'">
                {{ statusLabel(row.status) }}
              </el-tag>
            </template>
          </el-table-column>
          <el-table-column prop="created_at" label="创建时间" min-width="160">
            <template #default="{ row }">{{ formatTime(row.created_at) }}</template>
          </el-table-column>
          <el-table-column label="操作" width="160" fixed="right">
            <template #default="{ row }">
              <el-button type="primary" link size="small" @click="openEdit(row)">编辑</el-button>
              <el-button type="danger" link size="small" @click="removeDriver(row)">删除</el-button>
            </template>
          </el-table-column>
        </el-table>
      </el-card>
    </div>

    <!-- 新增/编辑弹窗 -->
    <el-dialog v-model="showDialog" :title="editing ? '编辑驾驶员' : '新增驾驶员'" width="520px">
      <el-form :model="form" label-width="90px">
        <el-form-item label="姓名">
          <el-input v-model="form.name" placeholder="请输入姓名" />
        </el-form-item>
        <el-form-item label="身份证号">
          <el-input v-model="form.id_card" placeholder="18 位身份证号" />
        </el-form-item>
        <el-form-item label="手机号">
          <el-input v-model="form.phone" placeholder="11 位手机号" />
        </el-form-item>
        <el-form-item label="驾驶证号">
          <el-input v-model="form.license_no" placeholder="驾驶证档案编号" />
        </el-form-item>
        <el-form-item label="所属车辆">
          <el-input v-model="form.vehicle_plate" placeholder="车牌号，如 京A12345" />
        </el-form-item>
        <el-form-item label="状态">
          <el-radio-group v-model="form.status">
            <el-radio value="active">在职</el-radio>
            <el-radio value="inactive">停职</el-radio>
          </el-radio-group>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="showDialog = false">取消</el-button>
        <el-button type="primary" :loading="submitting" @click="submit">确定</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { ref, computed, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { driverApi } from '../api'

const list = ref([])
const loading = ref(false)
const searchKeyword = ref('')
const filterStatus = ref('')

const showDialog = ref(false)
const editing = ref(false)
const submitting = ref(false)

const form = ref(defaultForm())

function defaultForm() {
  return {
    id: null,
    name: '',
    id_card: '',
    phone: '',
    license_no: '',
    vehicle_plate: '',
    status: 'active',
  }
}

const filteredList = computed(() => {
  let arr = list.value
  if (filterStatus.value) {
    arr = arr.filter(d => d.status === filterStatus.value)
  }
  if (searchKeyword.value) {
    const kw = searchKeyword.value.toLowerCase()
    arr = arr.filter(d =>
      d.name?.toLowerCase().includes(kw) ||
      d.phone?.toLowerCase().includes(kw) ||
      d.vehicle_plate?.toLowerCase().includes(kw)
    )
  }
  return arr
})

async function fetchList() {
  loading.value = true
  try {
    const res = await driverApi.getList({ limit: 200 })
    const data = res?.data || res || []
    list.value = (Array.isArray(data) ? data : (data.items || [])).map(normalize)
  } catch (e) {
    list.value = []
    ElMessage.error('加载驾驶员列表失败，请检查网络或稍后重试')
  } finally {
    loading.value = false
  }
}

// 后端字段兼容
// BROWSER-TEST-FIX-2026-09-01 [P2]: 后端 DriverInfoData 返回 driver_name/license_id/received_at，
// 原映射只读 name/license_no/created_at，导致姓名、驾驶证号、创建时间列永远显示空。
function normalize(d) {
  return {
    id: d.id ?? d.driver_id,
    name: d.driver_name || d.name || '',
    id_card: d.id_card || d.id_number || '',
    phone: d.phone || d.mobile || '',
    license_no: d.license_id || d.license_no || d.license_number || '',
    vehicle_plate: d.vehicle_plate || d.plate || '',
    status: d.status || 'active',
    created_at: d.received_at || d.created_at,
  }
}

function statusLabel(s) {
  return { active: '在职', inactive: '停职' }[s] || s
}

// 身份证号脱敏：保留前 6 后 4
function maskIdCard(id) {
  if (!id || id.length < 10) return id || '-'
  return id.substring(0, 6) + '********' + id.substring(id.length - 4)
}

function formatTime(t) {
  if (!t) return '-'
  return new Date(t).toLocaleString('zh-CN')
}

function openAdd() {
  editing.value = false
  form.value = defaultForm()
  showDialog.value = true
}

function openEdit(row) {
  editing.value = true
  form.value = {
    id: row.id,
    name: row.name,
    id_card: row.id_card,
    phone: row.phone,
    license_no: row.license_no,
    vehicle_plate: row.vehicle_plate,
    status: row.status,
  }
  showDialog.value = true
}

async function submit() {
  if (!form.value.name.trim()) {
    ElMessage.warning('请输入姓名')
    return
  }
  submitting.value = true
  try {
    const payload = {
      // BROWSER-TEST-FIX-2026-09-01 [P1]: 后端必填 driver_name，原只发 name 导致新增驾驶员 400
      driver_name: form.value.name.trim(),
      name: form.value.name.trim(),
      id_card: form.value.id_card.trim(),
      phone: form.value.phone.trim(),
      license_id: form.value.license_no.trim(),
      license_no: form.value.license_no.trim(),
      vehicle_plate: form.value.vehicle_plate.trim(),
      status: form.value.status,
    }
    if (editing.value) {
      await driverApi.update(form.value.id, payload)
      ElMessage.success('驾驶员信息已更新')
    } else {
      await driverApi.create(payload)
      ElMessage.success('驾驶员已创建')
    }
    showDialog.value = false
    await fetchList()
  } catch (e) {
    ElMessage.error(editing.value ? '更新失败' : '创建失败')
  } finally {
    submitting.value = false
  }
}

async function removeDriver(row) {
  try {
    await ElMessageBox.confirm(`确认删除驾驶员「${row.name}」吗？`, '删除确认', {
      type: 'warning',
      confirmButtonText: '删除',
      cancelButtonText: '取消',
    })
    await driverApi.delete(row.id)
    ElMessage.success('驾驶员已删除')
    await fetchList()
  } catch (e) {
    // 用户取消或请求失败
  }
}

onMounted(fetchList)
</script>
