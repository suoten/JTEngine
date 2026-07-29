<template>
  <div class="page-container">
    <div class="page-header">
      <h2>授权管理</h2>
      <el-button type="primary" @click="showActivate = true">
        <el-icon style="margin-right: 4px;"><Key /></el-icon>激活授权码
      </el-button>
    </div>

    <!-- ==================== 购买安装指引 ==================== -->
    <el-card shadow="never" class="guide-card">
      <template #header>
        <span style="font-weight: 600;">📋 模块购买与安装指引</span>
      </template>

      <el-tabs v-model="guideTab">
        <!-- ======== 试用流程 ======== -->
        <el-tab-pane label="试用流程" name="trial">
          <div class="guide-steps">
            <div class="guide-step">
              <div class="step-num">1</div>
              <div class="step-content">
                <div class="step-title">注册官网账号</div>
                <div class="step-desc">访问 <a href="https://www.jtengine.cn" target="_blank" style="color: var(--jte-accent);">jtengine.cn</a> 注册账号，每个模块支持 30 天免费试用（每台机器限一次）。</div>
              </div>
            </div>
            <div class="step-arrow">→</div>
            <div class="guide-step">
              <div class="step-num">2</div>
              <div class="step-content">
                <div class="step-title">官网申请试用</div>
                <div class="step-desc">在官网"模块商店"页面选择模块，点击"开始试用"。官网会生成试用授权码并记录（防止重复试用）。
                  <br><a :href="purchaseURL + '/store'" target="_blank" style="color: var(--jte-accent); font-weight: 600;">👉 点击前往官网申请试用 →</a>
                </div>
              </div>
            </div>
            <div class="step-arrow">→</div>
            <div class="guide-step">
              <div class="step-num">3</div>
              <div class="step-content">
                <div class="step-title">服务器激活授权码</div>
                <div class="step-desc">复制试用授权码，在服务器执行：<br><code>jte auth activate '&lt;授权码&gt;'</code><br>或在本页面点击右上角"激活授权码"按钮在线激活。</div>
              </div>
            </div>
            <div class="step-arrow">→</div>
            <div class="guide-step">
              <div class="step-num">4</div>
              <div class="step-content">
                <div class="step-title">下载并安装模块</div>
                <div class="step-desc">执行以下命令下载 .so 模块文件并安装到 <code>modules/</code> 目录：<br><code>jte module pull &lt;模块名&gt;</code><br><code>jte module install &lt;模块名&gt;</code></div>
              </div>
            </div>
            <div class="step-arrow">→</div>
            <div class="guide-step">
              <div class="step-num">5</div>
              <div class="step-content">
                <div class="step-title">重启服务</div>
                <div class="step-desc">重启 JTE 后端服务使模块加载生效：<br><code>systemctl restart jte</code> 或 <code>docker restart jte</code></div>
              </div>
            </div>
          </div>
        </el-tab-pane>

        <!-- ======== 购买流程 ======== -->
        <el-tab-pane label="购买流程" name="purchase">
          <div class="guide-steps">
            <div class="guide-step">
              <div class="step-num">1</div>
              <div class="step-content">
                <div class="step-title">官网选购</div>
                <div class="step-desc">访问 <a href="https://www.jtengine.cn/pricing" target="_blank" style="color: var(--jte-accent);">jtengine.cn/pricing</a>，浏览模块功能和价格，完成在线购买。</div>
              </div>
            </div>
            <div class="step-arrow">→</div>
            <div class="guide-step">
              <div class="step-num">2</div>
              <div class="step-content">
                <div class="step-title">获取授权码</div>
                <div class="step-desc">购买后在官网"我的订单"页面获取永久授权码（绑定机器指纹，锁定主版本号，小版本免费升级）。</div>
              </div>
            </div>
            <div class="step-arrow">→</div>
            <div class="guide-step">
              <div class="step-num">3</div>
              <div class="step-content">
                <div class="step-title">服务器激活授权码</div>
                <div class="step-desc">在服务器执行：<br><code>jte auth activate '&lt;授权码&gt;'</code><br>也可在本页面点击右上角"激活授权码"按钮在线激活。</div>
              </div>
            </div>
            <div class="step-arrow">→</div>
            <div class="guide-step">
              <div class="step-num">4</div>
              <div class="step-content">
                <div class="step-title">下载并安装模块</div>
                <div class="step-desc">执行以下命令下载 .so 模块文件并安装：<br><code>jte module pull &lt;模块名&gt;</code><br><code>jte module install &lt;模块名&gt;</code></div>
              </div>
            </div>
            <div class="step-arrow">→</div>
            <div class="guide-step">
              <div class="step-num">5</div>
              <div class="step-content">
                <div class="step-title">重启服务</div>
                <div class="step-desc">重启 JTE 后端服务使模块加载生效：<br><code>systemctl restart jte</code> 或 <code>docker restart jte</code></div>
              </div>
            </div>
          </div>
        </el-tab-pane>
      </el-tabs>

      <el-alert type="warning" :closable="false" style="margin-top: 12px;">
        <template #title>
          <span style="font-size: 13px;">
            ⚠️ <b>重要提示</b>：授权模块为 <b>.so</b> 格式（Go Plugin），仅支持 <b>Linux</b> 部署。
            <code>jte module pull</code> 从官网模块仓库（modules.jte.dev）下载，需要先激活授权码。
            授权码绑定机器指纹，迁移服务器需先在官网或本页面解绑。
          </span>
        </template>
      </el-alert>
    </el-card>

    <!-- ==================== SO 文件缺失提示 ==================== -->
    <el-alert
      v-if="missingSOModules.length > 0"
      type="error"
      :closable="false"
      show-icon
      style="margin-bottom: 16px;"
    >
      <template #title>
        <span style="font-size: 14px; font-weight: 600;">
          <el-icon><WarningFilled /></el-icon>
          检测到 {{ missingSOModules.length }} 个已授权模块未正确安装（缺少 .so 文件）
        </span>
      </template>
      <template #default>
        <div style="margin-top: 8px;">
          <p style="margin: 4px 0;">以下模块已激活授权码，但对应的 .so 文件未在 <code>modules/</code> 目录中找到：</p>
          <div style="display: flex; flex-wrap: wrap; gap: 8px; margin: 8px 0;">
            <el-tag v-for="mod in missingSOModules" :key="mod" type="danger" size="small">{{ mod }}</el-tag>
          </div>
          <div style="background: #fef0f0; padding: 12px; border-radius: 8px; margin-top: 8px;">
            <p style="font-weight: 600; margin: 0 0 6px;">📋 请按以下步骤安装模块：</p>
            <ol style="padding-left: 20px; margin: 0; line-height: 2;">
              <li>登录官网 <a :href="purchaseURL" target="_blank" style="color: #f56c6c; font-weight: 600;">{{ purchaseURL }}</a> → 用户中心 → 授权码管理</li>
              <li>点击「下载模块文件」按钮，下载对应的 <code>.so</code> 和 <code>.so.sig</code> 文件</li>
              <li>将下载的文件放入服务器的 <code>modules/</code> 目录</li>
              <li>执行命令重启服务：<code>systemctl restart jte</code> 或 <code>docker restart jte</code></li>
            </ol>
          </div>
          <el-button
            type="danger"
            plain
            size="small"
            style="margin-top: 8px;"
            @click="goToDownloadPage"
          >
            <el-icon><Download /></el-icon> 前往官网下载模块文件
          </el-button>
        </div>
      </template>
    </el-alert>

    <!-- 到期前 7 天警告横幅 -->
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

    <!-- 机器信息 -->
    <el-card shadow="never" style="margin-bottom:16px">
      <el-descriptions :column="1" border>
        <el-descriptions-item label="机器指纹">
          <el-text copyable>{{ authStore.machineFingerprint || '-' }}</el-text>
          <el-button link type="primary" size="small" @click="copyFingerprint" style="margin-left: 8px;">
            <el-icon><DocumentCopy /></el-icon> 复制
          </el-button>
          <span style="font-size: 12px; color: var(--jte-text-muted); margin-left: 8px;">（购买授权时需提供此指纹）</span>
        </el-descriptions-item>
        <el-descriptions-item label="已授权模块">{{ authStore.activeModules.join(', ') || '无' }}</el-descriptions-item>
      </el-descriptions>
    </el-card>

    <!-- 已激活的授权码列表 -->
    <el-table v-if="authStore.licenses && authStore.licenses.length > 0" :data="authStore.licenses" stripe style="margin-bottom: 16px;">
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

    <!-- 模块授权状态 -->
    <el-card shadow="never">
      <template #header>
        <span style="font-weight: 600;">模块授权状态</span>
        <span style="font-size: 12px; color: var(--jte-text-muted); margin-left: 12px;">（共 {{ KNOWN_MODULES.length }} 个授权模块，点击"购买解锁"查看详情和价格）</span>
      </template>
      <el-table :data="moduleStatusList" stripe>
        <el-table-column prop="label" label="模块名称" min-width="140" />
        <el-table-column prop="desc" label="功能说明" min-width="220">
          <template #default="{ row }">
            <span style="font-size: 13px;">{{ row.desc }}</span>
          </template>
        </el-table-column>
        <el-table-column label="价格" width="100" align="center">
          <template #default="{ row }">
            <span style="font-weight: 600; color: var(--jte-accent);">{{ row.price }}</span>
          </template>
        </el-table-column>
        <el-table-column label="状态" width="110">
          <template #default="{ row }">
            <el-tag v-if="row.status === 'licensed'" type="success" size="small">已授权</el-tag>
            <el-tag v-else-if="row.status === 'expiring_soon'" type="warning" size="small">即将到期</el-tag>
            <el-tag v-else-if="row.status === 'expired'" type="danger" size="small">已过期</el-tag>
            <el-tag v-else-if="row.status === 'trial'" type="primary" size="small">试用中</el-tag>
            <el-tag v-else-if="row.status === 'trial_expired'" type="info" size="small">试用结束</el-tag>
            <el-tag v-else type="info" size="small">未授权</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="到期/倒计时" width="140">
          <template #default="{ row }">
            <span v-if="row.status === 'trial'" style="color: var(--jte-warning);">剩余 {{ row.trialDays }} 天</span>
            <span v-else-if="row.status === 'expiring_soon'" style="color: var(--jte-warning);">{{ row.daysLeft }} 天</span>
            <span v-else-if="row.status === 'licensed'">永久</span>
            <span v-else style="color: var(--jte-text-muted);">-</span>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="180" fixed="right">
          <template #default="{ row }">
            <el-button
              v-if="row.status === 'unlicensed' || row.status === 'trial_expired'"
              type="warning"
              plain
              link
              size="small"
              @click="goToWebsiteTrial(row.name)"
            >开始试用</el-button>
            <el-button
              v-if="row.status !== 'licensed' && row.status !== 'expiring_soon'"
              type="success"
              link
              size="small"
              @click="openPurchaseModal(row.name)"
            >购买解锁</el-button>
            <el-button
              v-if="row.status === 'licensed' || row.status === 'expiring_soon'"
              type="info"
              link
              size="small"
              @click="copyFingerprint"
            >续费</el-button>
          </template>
        </el-table-column>
      </el-table>
    </el-card>

    <!-- 常见问题 -->
    <el-card shadow="never" class="faq-card">
      <template #header>
        <span style="font-weight: 600;">❓ 常见问题</span>
      </template>
      <el-collapse>
        <el-collapse-item title="试用到期后数据会丢失吗？">
          <p>不会。试用到期后模块功能暂停（菜单灰显），但已存储的数据不会删除。重新授权后功能立即恢复。</p>
        </el-collapse-item>
        <el-collapse-item title="如何将授权迁移到另一台服务器？">
          <p>授权码绑定机器指纹。迁移步骤：</p>
          <ol style="padding-left: 20px; line-height: 1.8;">
            <li>在旧服务器上点击"删除"解除绑定</li>
            <li>在新服务器上复制机器指纹</li>
            <li>在新服务器上点击"激活授权码"并粘贴授权码</li>
          </ol>
          <p>如旧服务器已不可用，可在官网使用"离线解绑"功能（需提供购买凭证）。</p>
        </el-collapse-item>
        <el-collapse-item title="授权码丢失怎么办？">
          <p>授权码与购买账户绑定，登录官网 <a href="https://www.jtengine.cn" target="_blank" style="color: var(--jte-accent);">www.jtengine.cn</a> → 我的订单 → 查看授权码。或联系客服提供购买凭证找回。</p>
        </el-collapse-item>
        <el-collapse-item title="是否支持退款？">
          <p>授权码激活后不支持退款。建议先使用 30 天免费试用，确认功能满足需求后再购买。</p>
        </el-collapse-item>
        <el-collapse-item title="升级版本需要重新授权吗？">
          <p>不需要。授权码锁定主版本号（如 v1.x），所有 1.x 小版本免费升级。主版本升级（如 v1→v2）可能需要补差价升级。</p>
        </el-collapse-item>
      </el-collapse>
    </el-card>

    <!-- 激活授权码弹窗 -->
    <el-dialog v-model="showActivate" title="激活授权码" width="480px">
      <el-steps :active="1" align-center style="margin-bottom: 20px;">
        <el-step title="复制指纹" />
        <el-step title="粘贴授权码" />
        <el-step title="激活成功" />
      </el-steps>
      <el-alert type="info" :closable="false" style="margin-bottom: 16px;">
        <template #title>
          <span style="font-size: 13px;">购买时请提供本机指纹：<b>{{ authStore.machineFingerprint?.substring(0, 16) }}...</b></span>
        </template>
      </el-alert>
      <el-form label-width="80px">
        <el-form-item label="授权码">
          <el-input v-model="licenseKey" type="textarea" :rows="5" placeholder="粘贴您收到的授权码（支持多行格式）" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="showActivate = false">取消</el-button>
        <el-button type="primary" @click="activateLicense" :loading="activating">
          <el-icon style="margin-right: 4px;"><Check /></el-icon>确认激活
        </el-button>
      </template>
    </el-dialog>

    <!-- 模块购买弹窗 -->
    <ModulePurchaseModal
      v-model="purchaseModalVisible"
      :module-name="purchaseModuleName"
      :trial-loading="purchaseTrialLoading"
      @start-trial="handleStartTrial"
    />
  </div>
</template>

<script setup>
import { ref, computed, onMounted } from 'vue'
import { useAuthStore } from '../../stores/auth'
import { systemApi, websiteApi } from '../../api'
import { ElMessage } from 'element-plus'
import { Key, Check, DocumentCopy, WarningFilled, Download } from '@element-plus/icons-vue'
import ModulePurchaseModal from '../../components/ModulePurchaseModal.vue'
import { findModule } from '../../data/moduleCatalog'

const authStore = useAuthStore()
const showActivate = ref(false)
const licenseKey = ref('')
const activating = ref(false)
const guideTab = ref('trial') // 指引Tab：trial=试用流程 / purchase=购买流程

// 模块购买弹窗
const purchaseModalVisible = ref(false)
const purchaseModuleName = ref('')
const purchaseTrialLoading = ref(false)

// 已加载模块列表（从系统 API 获取，用于检测 SO 文件是否缺失）
const loadedModules = ref([])
const purchaseURL = ref('https://www.jtengine.cn')

// 授权但未加载的模块（SO文件缺失）
const missingSOModules = computed(() => {
  if (!authStore.activeModules || authStore.activeModules.length === 0) return []
  return authStore.activeModules.filter(mod => {
    // 标准化模块名
    const normalized = mod.replace('module-', '').replace(/-/g, '_')
    return !loadedModules.value.some(lm => {
      const lmNormalized = lm.replace('module-', '').replace(/-/g, '_')
      return lmNormalized === normalized || lm === mod
    })
  })
})

async function fetchLoadedModules() {
  try {
    const res = await systemApi.getModules()
    if (res.code === 0 && res.data) {
      loadedModules.value = res.data.filter(m => m.enabled).map(m => m.name)
    }
  } catch (e) {
    // 忽略错误
  }
}

async function fetchPurchaseURL() {
  try {
    const res = await websiteApi.getInfo()
    if (res.code === 0 && res.data?.website_url) {
      purchaseURL.value = res.data.website_url
    }
  } catch (e) {
    // 使用默认值
  }
}

function openPurchaseModal(moduleName) {
  purchaseModuleName.value = moduleName
  purchaseModalVisible.value = true
}

function handleStartTrial(moduleName) {
  // 跳转到官网申请试用
  const url = purchaseURL.value + '/store?module=' + encodeURIComponent(moduleName)
  window.open(url, '_blank', 'noopener,noreferrer')
  ElMessage.info('已跳转到官网申请试用，请在官网完成试用申请后复制授权码回到此页面激活')
  purchaseModalVisible.value = false
}

// 所有可授权模块清单（与后端 TrialModules / License.Modules 对齐）
// FIXED-2026-07-24: 增加描述和价格，与 moduleCatalog 数据源对齐
const KNOWN_MODULES = [
  { name: 'protocol_809',  label: 'JT/T 809 平台级联',  desc: '上下级平台级联，位置/报警/车辆数据双向转发',  price: '¥12,000' },
  { name: 'protocol_1045', label: 'JT/T 1045 主动安全', desc: 'DSM/ADAS 主动安全报警接入，AI 误报过滤',         price: '¥9,800' },
  { name: 'protocol_1078', label: 'JT/T 1078 音视频',   desc: '音视频实时回传与历史回放，多协议播放',            price: '¥15,000' },
  { name: 'protocol_905',  label: 'JT/T 905 北斗出租',  desc: '北斗短报文通信与位置服务接入',                   price: '¥6,000' },
  { name: 'storage',       label: '数据存储与报表',     desc: 'TDengine/Redis/MinIO 分层存储，报表中心',       price: '¥18,000' },
  { name: 'ai',            label: 'AI 智能分析',        desc: '报警过滤/疲劳驾驶/风险评分，多模型推理',        price: '¥20,000' },
  { name: 'ai_nlp',        label: 'AI 对话助手',        desc: 'NL2SQL 智能查询/协议调试/RAG 知识库',          price: '¥14,000' },
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

function goToWebsiteTrial(moduleName) {
  const url = purchaseURL.value + '/store?module=' + encodeURIComponent(moduleName)
  window.open(url, '_blank', 'noopener,noreferrer')
  ElMessage.info('已跳转到官网申请试用，请在官网完成试用申请后复制授权码回到此页面激活')
}

function goToDownloadPage() {
  const url = purchaseURL.value + '/user/licenses'
  window.open(url, '_blank', 'noopener,noreferrer')
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
  fetchLoadedModules()
  fetchPurchaseURL()
})
</script>

<style scoped>
.page-container { padding: 24px; }
.page-header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 20px; }
.page-header h2 { margin: 0; font-size: 18px; color: var(--jte-text); }

/* 购买安装指引卡片 */
.guide-card { margin-bottom: 16px; }
.guide-steps { display: flex; align-items: flex-start; gap: 8px; }
.guide-step { display: flex; gap: 10px; align-items: flex-start; flex: 1; }
.step-num { width: 28px; height: 28px; border-radius: 50%; background: var(--jte-accent); color: #fff; display: flex; align-items: center; justify-content: center; font-size: 14px; font-weight: 700; flex-shrink: 0; }
.step-content { flex: 1; }
.step-title { font-size: 14px; font-weight: 600; color: var(--jte-text); margin-bottom: 4px; }
.step-desc { font-size: 12px; color: var(--jte-text-muted); line-height: 1.5; }
.step-arrow { font-size: 20px; color: var(--jte-text-muted); padding-top: 4px; flex-shrink: 0; }

/* 常见问题卡片 */
.faq-card { margin-top: 16px; }
.faq-card :deep(.el-collapse-item__header) { font-size: 14px; font-weight: 500; }
.faq-card p { margin: 4px 0; font-size: 13px; color: var(--jte-text-muted); line-height: 1.6; }
</style>
