<template>
  <!-- AUTO-FIX-2026-06-30 [P1-7]: 模块介绍与购买解锁弹窗 -->
  <el-dialog
    :model-value="modelValue"
    @update:model-value="$emit('update:modelValue', $event)"
    :title="title"
    width="480px"
    align-center
  >
    <div v-if="module" class="module-modal">
      <div class="module-header">
        <el-icon :size="28" color="var(--jte-accent)">
          <component :is="moduleIcon" />
        </el-icon>
        <div>
          <div class="module-title">{{ module.label }}</div>
          <div class="module-desc">{{ module.description }}</div>
        </div>
      </div>

      <el-divider />

      <div class="section-label">功能特性</div>
      <ul class="feature-list">
        <li v-for="f in module.features" :key="f">
          <el-icon color="var(--jte-success)"><Check /></el-icon>
          <span>{{ f }}</span>
        </li>
      </ul>

      <el-divider />

      <div class="price-row">
        <div>
          <div class="section-label">价格</div>
          <div class="price-value">{{ module.price }}</div>
          <div class="price-hint">永久授权 · 锁定主版本号 · 小版本免费升级</div>
        </div>
        <div v-if="module.trialDays" class="trial-box">
          <el-icon color="var(--jte-warning)"><Clock /></el-icon>
          <span>支持 {{ module.trialDays }} 天免费试用</span>
        </div>
      </div>

      <!-- 试用中倒计时提示 -->
      <el-alert
        v-if="trialActive"
        :title="`试用中 · 剩余 ${trialDays} 天`"
        type="warning"
        :closable="false"
        show-icon
        style="margin-top: 12px;"
      />
      <el-alert
        v-else-if="trialExpired"
        title="试用已结束，请购买解锁"
        type="error"
        :closable="false"
        show-icon
        style="margin-top: 12px;"
      />
    </div>

    <template #footer>
      <el-button @click="$emit('update:modelValue', false)">关闭</el-button>
      <el-button
        v-if="canStartTrial"
        type="warning"
        plain
        :loading="trialLoading"
        @click="$emit('start-trial', moduleName)"
      >开始试用</el-button>
      <el-button type="primary" @click="goPurchase">
        <el-icon><ShoppingCart /></el-icon>
        购买解锁
      </el-button>
    </template>
  </el-dialog>
</template>

<script setup>
import { computed, ref, watch } from 'vue'
import { Check, Clock, ShoppingCart, Connection, VideoCamera, DataLine, MagicStick, ChatDotRound, Aim } from '@element-plus/icons-vue'
import { findModule } from '../data/moduleCatalog'
import { useAuthStore } from '../stores/auth'
import { websiteApi } from '../api'

const props = defineProps({
  modelValue: Boolean,
  moduleName: { type: String, default: '' },
  trialLoading: { type: Boolean, default: false },
})

defineEmits(['update:modelValue', 'start-trial'])

const authStore = useAuthStore()
const purchaseURL = ref('https://jte.dev/pricing')

// 模块图标映射
const ICON_MAP = {
  protocol_809: Connection,
  protocol_1045: Aim,
  protocol_1078: VideoCamera,
  protocol_905: Aim,
  storage: DataLine,
  ai: MagicStick,
  ai_nlp: ChatDotRound,
}

const module = computed(() => findModule(props.moduleName))
const moduleIcon = computed(() => ICON_MAP[props.moduleName] || Connection)
const title = computed(() => module.value ? `${module.value.label} · 模块介绍` : '模块介绍')

// 试用状态
const status = computed(() => authStore.getModuleStatus(props.moduleName))
const trialActive = computed(() => status.value === 'trial')
const trialExpired = computed(() => status.value === 'trial_expired')
const trialDays = computed(() => authStore.getTrialRemainingDays(props.moduleName))
const canStartTrial = computed(() =>
  module.value?.trialDays > 0 &&
  (status.value === 'unlicensed' || status.value === 'trial_expired')
)

// 弹窗打开时加载购买链接（缓存，仅加载一次）
let urlLoaded = false
watch(() => props.modelValue, async (open) => {
  if (open && !urlLoaded) {
    urlLoaded = true
    try {
      const res = await websiteApi.getInfo()
      if (res.code === 0 && res.data && res.data.purchase_url) {
        purchaseURL.value = res.data.purchase_url
      }
    } catch (e) { /* 保留默认链接 */ }
  }
})

function goPurchase() {
  const url = new URL(purchaseURL.value, window.location.origin)
  if (props.moduleName) url.searchParams.set('module', props.moduleName)
  window.open(url.toString(), '_blank')
}
</script>

<style scoped>
.module-modal { padding: 0 4px; }
.module-header { display: flex; gap: 14px; align-items: flex-start; }
.module-title { font-size: 16px; font-weight: 600; color: var(--jte-text); }
.module-desc { font-size: 13px; color: var(--jte-text-muted); margin-top: 4px; line-height: 1.5; }
.section-label { font-size: 12px; color: var(--jte-text-muted); margin-bottom: 8px; text-transform: uppercase; letter-spacing: 0.05em; }
.feature-list { list-style: none; padding: 0; margin: 0; display: grid; grid-template-columns: 1fr 1fr; gap: 8px 16px; }
.feature-list li { display: flex; align-items: center; gap: 6px; font-size: 13px; color: var(--jte-text); }
.price-row { display: flex; justify-content: space-between; align-items: flex-end; }
.price-value { font-size: 24px; font-weight: 700; color: var(--jte-accent); }
.price-hint { font-size: 11px; color: var(--jte-text-muted); margin-top: 4px; }
.trial-box { display: flex; align-items: center; gap: 6px; font-size: 12px; color: var(--jte-warning); background: rgba(245,158,11,0.1); padding: 6px 10px; border-radius: 6px; }
</style>
