<template>
  <div class="page-container">
    <div class="page-header"><h2>菜单显示配置</h2></div>

    <el-card shadow="never">
      <template #header>
        <div style="display: flex; align-items: center; justify-content: space-between;">
          <span style="font-weight: 600;">菜单显示开关</span>
          <div>
            <el-button size="small" @click="checkAll">全选</el-button>
            <el-button size="small" @click="uncheckAll">全不选</el-button>
          </div>
        </div>
      </template>

      <el-tabs v-model="activeTab">
        <el-tab-pane label="监控中心" name="monitor">
          <el-checkbox-group v-model="checkedMenus" style="display: flex; flex-direction: column; gap: 8px;">
            <el-checkbox label="overview">概览</el-checkbox>
            <el-checkbox label="map">实时地图</el-checkbox>
            <el-checkbox label="sessions">会话管理</el-checkbox>
            <el-checkbox label="vehicles">车辆管理</el-checkbox>
            <el-checkbox label="drivers">驾驶员管理</el-checkbox>
            <el-checkbox label="geofences">电子围栏</el-checkbox>
            <el-checkbox label="alarms">报警中心</el-checkbox>
            <el-checkbox label="logs">协议日志</el-checkbox>
            <el-checkbox label="devices">设备管理</el-checkbox>
            <el-checkbox label="tracks">轨迹回放</el-checkbox>
            <el-checkbox label="video">视频监控</el-checkbox>
            <el-checkbox label="commands">指令下发</el-checkbox>
          </el-checkbox-group>
        </el-tab-pane>
        <el-tab-pane label="JT/T 809 级联 🔒" name="cascade">
          <el-checkbox-group v-model="checkedMenus" style="display: flex; flex-direction: column; gap: 8px;">
            <el-checkbox label="cascade">级联平台</el-checkbox>
          </el-checkbox-group>
        </el-tab-pane>
        <el-tab-pane label="JT/T 905 出租 🔒" name="taxi">
          <el-checkbox-group v-model="checkedMenus" style="display: flex; flex-direction: column; gap: 8px;">
            <el-checkbox label="taxi">出租车监控</el-checkbox>
          </el-checkbox-group>
        </el-tab-pane>
        <el-tab-pane label="数据存储与报表 🔒" name="storage">
          <el-checkbox-group v-model="checkedMenus" style="display: flex; flex-direction: column; gap: 8px;">
            <el-checkbox label="reports">报表中心</el-checkbox>
            <el-checkbox label="archive">归档查询</el-checkbox>
          </el-checkbox-group>
        </el-tab-pane>
        <el-tab-pane label="AI 智能分析 🔒" name="ai">
          <el-checkbox-group v-model="checkedMenus" style="display: flex; flex-direction: column; gap: 8px;">
            <el-checkbox label="ai">AI分析</el-checkbox>
            <el-checkbox label="alarm_filter">报警过滤</el-checkbox>
            <el-checkbox label="driver_fatigue">疲劳驾驶</el-checkbox>
            <el-checkbox label="risk_scoring">风险评分</el-checkbox>
          </el-checkbox-group>
        </el-tab-pane>
        <el-tab-pane label="AI 对话助手 🔒" name="nlp">
          <el-checkbox-group v-model="checkedMenus" style="display: flex; flex-direction: column; gap: 8px;">
            <el-checkbox label="nlp">智能助手</el-checkbox>
          </el-checkbox-group>
        </el-tab-pane>
        <el-tab-pane label="系统管理" name="system">
          <el-checkbox-group v-model="checkedMenus" style="display: flex; flex-direction: column; gap: 8px;">
            <el-checkbox label="system">系统设置</el-checkbox>
            <el-checkbox label="modules">模块管理</el-checkbox>
            <el-checkbox label="users">用户管理</el-checkbox>
            <el-checkbox label="config">系统参数</el-checkbox>
            <el-checkbox label="map_config">地图配置</el-checkbox>
            <el-checkbox label="menu_config">菜单配置</el-checkbox>
            <el-checkbox label="ai_config">AI 配置</el-checkbox>
            <el-checkbox label="storage">存储管理</el-checkbox>
            <el-checkbox label="auth">授权管理</el-checkbox>
            <el-checkbox label="roles">角色权限</el-checkbox>
            <el-checkbox label="logs_system">审计日志</el-checkbox>
          </el-checkbox-group>
        </el-tab-pane>
      </el-tabs>

      <div style="margin-top: 16px;">
        <el-button type="primary" @click="save" :loading="saving">保存菜单配置</el-button>
        <el-button @click="resetDefault">重置默认</el-button>
      </div>
    </el-card>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { ElMessage } from 'element-plus'

const activeTab = ref('monitor')
const checkedMenus = ref([])
const saving = ref(false)

const ALL_MENU_KEYS = [
  'overview', 'map', 'sessions', 'vehicles', 'drivers', 'geofences', 'alarms', 'logs',
  'devices', 'tracks', 'video', 'commands',
  'cascade', 'taxi', 'reports', 'archive',
  'ai', 'alarm_filter', 'driver_fatigue', 'risk_scoring', 'nlp',
  'system', 'modules', 'users', 'config', 'map_config', 'menu_config', 'ai_config',
  'storage', 'auth', 'roles', 'logs_system'
]

function loadFromStorage() {
  const userId = 'default'
  const key = `jte_visible_menus_${userId}`
  try {
    const saved = localStorage.getItem(key)
    if (saved) {
      const obj = JSON.parse(saved)
      checkedMenus.value = ALL_MENU_KEYS.filter(k => obj[k] !== false)
    } else {
      checkedMenus.value = [...ALL_MENU_KEYS]
    }
  } catch {
    checkedMenus.value = [...ALL_MENU_KEYS]
  }
}

function checkAll() {
  checkedMenus.value = [...ALL_MENU_KEYS]
}

function uncheckAll() {
  checkedMenus.value = []
}

function save() {
  saving.value = true
  try {
    const obj = {}
    ALL_MENU_KEYS.forEach(k => {
      obj[k] = checkedMenus.value.includes(k)
    })
    const userId = 'default'
    localStorage.setItem(`jte_visible_menus_${userId}`, JSON.stringify(obj))
    // 同步到全局配置
    if (window.jteMenuConfig) {
      ALL_MENU_KEYS.forEach(k => {
        window.jteMenuConfig.visibleMenus.value[k] = checkedMenus.value.includes(k)
      })
    }
    ElMessage.success('菜单配置已保存')
  } catch (e) {
    ElMessage.error('保存失败')
  } finally {
    saving.value = false
  }
}

function resetDefault() {
  checkedMenus.value = [...ALL_MENU_KEYS]
  save()
}

onMounted(loadFromStorage)
</script>

<style scoped>
.page-container { padding: 24px; }
.page-header { margin-bottom: 20px; }
.page-header h2 { margin: 0; font-size: 18px; color: var(--jte-text); }
</style>
