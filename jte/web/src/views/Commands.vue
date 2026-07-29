<template>
  <div class="page-container">
    <div class="page-header"><h2>指令下发</h2></div>
    <el-card shadow="never">
      <el-form label-width="100px" style="max-width:500px">
        <el-form-item label="终端手机号"><el-input v-model="form.phone" placeholder="输入终端手机号" /></el-form-item>
        <el-form-item label="指令类型">
          <el-select v-model="form.command" placeholder="选择指令类型">
            <el-option label="设置终端参数" value="set_params" />
            <el-option label="查询终端参数" value="get_params" />
            <el-option label="终端控制" value="terminal_control" />
            <el-option label="位置信息查询" value="location_query" />
            <el-option label="文本下发" value="text_message" />
            <el-option label="拍照" value="photo" />
          </el-select>
        </el-form-item>
        <el-form-item label="参数"><el-input v-model="form.parameter" type="textarea" :rows="3" placeholder='指令参数(JSON)，如 {"text":"你好","sign":0}' /></el-form-item>
        <el-form-item><el-button type="primary" @click="sendCommand" :loading="sending">发送指令</el-button></el-form-item>
      </el-form>
    </el-card>
    <el-card shadow="never" style="margin-top:16px" v-if="results.length > 0">
      <template #header><span>下发记录</span></template>
      <el-table :data="results" stripe size="small">
        <el-table-column prop="phone" label="终端手机号" width="160" />
        <el-table-column prop="command" label="指令类型" width="140" />
        <el-table-column prop="status" label="状态" width="80" />
        <el-table-column prop="time" label="时间" min-width="160" />
      </el-table>
    </el-card>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { useRoute } from 'vue-router'
import { deviceApi } from '../api'
import { ElMessage, ElMessageBox } from 'element-plus'

// FIXED: [功能缺失] 从路由参数预填手机号（Devices.vue 跳转时携带） [2026-07-17]
const route = useRoute()
const form = ref({ phone: '', command: '', parameter: '' })
const sending = ref(false)
const results = ref([])

onMounted(() => {
  if (route.query.phone) {
    form.value.phone = String(route.query.phone)
  }
})

// AUTO-FIX-2026-06-30 [P1-7]: 指令下发二次确认（防误操作/恶意触发）
async function sendCommand() {
  if (!form.value.phone || !form.value.command) return
  try {
    await ElMessageBox.confirm(
      `确认向终端 ${form.value.phone} 下发「${form.value.command}」指令？`,
      '指令下发确认',
      { type: 'warning', confirmButtonText: '确认下发', cancelButtonText: '取消' }
    )
  } catch {
    return // 用户取消
  }
  sending.value = true
  try {
    let params = {}
    if (form.value.parameter) {
      try { params = JSON.parse(form.value.parameter) } catch (e) {
        ElMessage.error('参数JSON格式错误')
        sending.value = false
        return
      }
    }
    await deviceApi.sendCommand({ phone: form.value.phone, command: form.value.command, params })
    results.value.unshift({ phone: form.value.phone, command: form.value.command, status: '已发送', time: new Date().toLocaleString() })
    ElMessage.success('指令已发送')
  } catch (e) {
    console.error(e)
    ElMessage.error('指令发送失败')
  } finally { sending.value = false }
}
</script>

<style scoped>
.page-container { padding: 24px; }
.page-header { margin-bottom: 20px; }
.page-header h2 { margin: 0; font-size: 18px; color: var(--jte-text); }
</style>
