<template>
  <div class="page-container">
    <div class="page-header">
      <h2>授权管理</h2>
      <el-button type="primary" @click="showActivate = true">激活授权码</el-button>
    </div>

    <!-- AUTO-FIX-2026-06-26: 第六轮前端修复 - 到期前 7 天警告横幅 -->
    <el-alert
      v-for="lic in expiringLicenses" :key="lic.id"
      :title="`授权 ${lic.id} 将在 ${daysLeft(lic)} 天后到期（${lic.expires_at}）`"
      type="warning"
      show-icon
      :closable="false"
      style="margin-bottom: 12px;"
    >
      <template #default>
        授权模块：{{ (lic.modules || []).join(', ') }}，请及时续费以免功能不可用。
      </template>
    </el-alert>

    <el-card shadow="never" style="margin-bottom:16px">
      <el-descriptions :column="1" border>
        <el-descriptions-item label="机器指纹">
          <el-text copyable>{{ authStore.machineFingerprint || '-' }}</el-text>
        </el-descriptions-item>
        <el-descriptions-item label="已授权模块">{{ authStore.activeModules.join(', ') || '无' }}</el-descriptions-item>
      </el-descriptions>
    </el-card>

    <el-table :data="authStore.licenses" stripe style="margin-bottom: 16px;">
      <el-table-column prop="id" label="授权ID" width="160" />
      <el-table-column prop="modules" label="包含模块" min-width="200">
        <template #default="{ row }">{{ (row.modules || []).join(', ') }}</template>
      </el-table-column>
      <el-table-column prop="expires_at" label="到期时间" width="160" />
      <el-table-column prop="expired" label="状态" width="100">
        <template #default="{ row }">
          <el-tag v-if="row.expired" type="danger" size="small">已过期</el-tag>
          <el-tag v-else-if="isExpiringSoon(row)" type="warning" size="small">即将到期</el-tag>
          <el-tag v-else type="success" size="small">有效</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="操作" width="160" fixed="right">
        <template #default="{ row }">
          <el-button type="danger" link size="small" @click="removeLicense(row)">删除</el-button>
          <el-button type="primary" link size="small" @click="copyFingerprint">复制指纹</el-button>
        </template>
      </el-table-column>
    </el-table>

    <!-- AUTO-FIX-2026-06-26: 第六轮前端修复 - 模块授权状态矩阵 + 试用倒计时 + 购买解锁 -->
    <el-card shadow="never">
      <template #header>
        <span style="font-weight: 600;">模块授权状态</span>
      </template>
      <el-table :data="moduleStatusList" stripe>
        <el-table-column prop="name" label="模块" min-width="140" />
        <el-table-column prop="label" label="说明" min-width="180" />
        <el-table-column label="状态" width="120">
          <template #default="{ row }">
            <el-tag v-if="row.status === 'licensed'" type="success" size="small">已授权</el-tag>
            <el-tag v-else-if="row.status === 'expiring_soon'" type="warning" size="small">即将到期</el-tag>
            <el-tag v-else-if="row.status === 'expired'" type="danger" size="small">已过期</el-tag>
            <el-tag v-else-if="row.status === 'trial'" type="primary" size="small">试用中</el-tag>
            <el-tag v-else-if="row.status === 'trial_expired'" type="info" size="small">试用结束</el-tag>
            <el-tag v-else type="info" size="small">未授权</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="到期/倒计时" width="180">
          <template #default="{ row }">
            <span v-if="row.status === 'trial'">剩余 {{ row.trialDays }} 天</span>
            <span v-else-if="row.status === 'expiring_soon'">{{ row.daysLeft }} 天</span>
            <span v-else-if="row.status === 'licensed'">-</span>
            <span v-else>-</span>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="160" fixed="right">
          <template #default="{ row }">
            <el-button
              v-if="row.status === 'unlicensed' || row.status === 'trial_expired'"
              type="primary"
              link
              size="small"
              @click="startTrial(row.name)"
              :loading="trialLoading[row.name]"
            >开始试用</el-button>
            <el-button
              v-if="row.status !== 'licensed' && row.status !== 'expiring_soon'"
              type="success"
              link
              size="small"
              @click="openPurchaseModal(row.name)"
            >购买解锁</el-button>
          </template>
        </el-table-column>
      </el-table>
    </el-card>

    <el-dialog v-model="showActivate" title="激活授权码" width="400px">
      <el-form label-width="80px">
        <el-form-item label="授权码">
          <el-input v-model="licenseKey" type="textarea" :rows="4" placeholder="输入授权码（支持换行）" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="showActivate = false">取消</el-button>
        <el-button type="primary" @click="activateLicense" :loading="activating">激活</el-button>
      </template>
    </el-dialog>

    <!-- AUTO-FIX-2026-06-30 [P1-7]: 模块介绍与购买解锁弹窗 -->
    <ModulePurchaseModal
      v-model="purchaseModalVisible"
      :module-name="purchaseModuleName"
      :trial-loading="purchaseTrialLoading"
      @start-trial="handleStartTrial"
    />
  </div>
</template>
<script setup>
// AUTO-FIX-2026-06-26: 第六轮前端修复 - 授权联动（试用倒计时 + 到期警告 + 购买解锁）
import { ref, computed, onMounted, reactive } from 'vue'
import { useAuthStore } from '../../stores/auth'
import { ElMessage } from 'element-plus'
import ModulePurchaseModal from '../../components/ModulePurchaseModal.vue'

const authStore = useAuthStore()
const showActivate = ref(false)
const licenseKey = ref('')
const activating = ref(false)
const trialLoading = reactive({})

// AUTO-FIX-2026-06-30 [P1-7]: 模块购买解锁弹窗
const purchaseModalVisible = ref(false)
const purchaseModuleName = ref('')
const purchaseTrialLoading = ref(false)

function openPurchaseModal(moduleName) {
  purchaseModuleName.value = moduleName
  purchaseModalVisible.value = true
}

async function handleStartTrial(moduleName) {
  purchaseTrialLoading.value = true
  try {
    await authStore.startTrial(moduleName)
    ElMessage.success('试用已开启')
    purchaseModalVisible.value = false
  } catch (e) {
    ElMessage.error(e.message || '试用开启失败')
  } finally {
    purchaseTrialLoading.value = false
  }
}

// 所有可授权模块清单（与后端 TrialModules / License.Modules 对齐）
const KNOWN_MODULES = [
  { name: 'protocol_809', label: 'JT/T 809 平台级联' },
  { name: 'protocol_1045', label: 'JT/T 1045 主动安全' },
  { name: 'protocol_1078', label: 'JT/T 1078 音视频' },
  { name: 'protocol_905', label: 'JT/T 905 北斗' },
  { name: 'storage', label: '数据存储与报表' },
  { name: 'ai', label: 'AI 智能分析' },
  { name: 'ai_nlp', label: 'AI 对话助手' },
]

const moduleStatusList = computed(() => {
  return KNOWN_MODULES.map(m => {
    const status = authStore.getModuleStatus(m.name)
    const trialDays = authStore.getTrialRemainingDays(m.name)
    let daysLeft = null
    if (status === 'expiring_soon') {
      const lic = authStore.licenses.find(l => l.modules && l.modules.includes(m.name))
      if (lic && lic.expires_at) {
        daysLeft = Math.ceil((new Date(lic.expires_at) - new Date()) / (1000 * 60 * 60 * 24))
      }
    }
    return { ...m, status, trialDays, daysLeft }
  })
})

const expiringLicenses = computed(() => {
  return (authStore.licenses || []).filter(lic => {
    if (lic.expired) return false
    if (!lic.expires_at) return false
    const days = (new Date(lic.expires_at) - new Date()) / (1000 * 60 * 60 * 24)
    return days >= 0 && days < 7
  })
})

function daysLeft(lic) {
  if (!lic.expires_at) return 0
  return Math.max(0, Math.ceil((new Date(lic.expires_at) - new Date()) / (1000 * 60 * 60 * 24)))
}

function isExpiringSoon(lic) {
  if (lic.expired || !lic.expires_at) return false
  const days = (new Date(lic.expires_at) - new Date()) / (1000 * 60 * 60 * 24)
  return days >= 0 && days < 7
}

async function activateLicense() {
  if (!licenseKey.value) return
  activating.value = true
  try {
    await authStore.activateLicense(licenseKey.value)
    ElMessage.success('授权码激活成功')
    showActivate.value = false
    licenseKey.value = ''
  } catch (e) {
    ElMessage.error(e.message || '激活失败')
  } finally {
    activating.value = false
  }
}

async function removeLicense(row) {
  try {
    await authStore.removeLicense(row.id)
    ElMessage.success('授权码已删除')
  } catch (e) {
    ElMessage.error(e.message || '删除失败')
  }
}

async function startTrial(moduleName) {
  trialLoading[moduleName] = true
  try {
    await authStore.startTrial(moduleName)
    ElMessage.success('试用已开启')
  } catch (e) {
    ElMessage.error(e.message || '试用开启失败')
  } finally {
    trialLoading[moduleName] = false
  }
}

function copyFingerprint() {
  if (!authStore.machineFingerprint) {
    ElMessage.warning('机器指纹未加载')
    return
  }
  navigator.clipboard.writeText(authStore.machineFingerprint).then(() => {
    ElMessage.success('机器指纹已复制')
  }).catch(() => {
    ElMessage.warning('复制失败，请手动选择复制')
  })
}

onMounted(() => {
  if (!authStore.loaded) {
    authStore.fetchStatus()
  }
})
</script>
<style scoped>
.page-container { padding: 24px; }
.page-header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 20px; }
.page-header h2 { margin: 0; font-size: 18px; color: var(--jte-text); }
</style>
