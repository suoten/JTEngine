<template>
  <div class="login-container">
    <div class="login-bg-effects">
      <div class="bg-grid"></div>
      <div class="bg-glow glow-1"></div>
      <div class="bg-glow glow-2"></div>
    </div>
    <div class="login-card">
      <div class="login-logo">
        <Logo :size="72" variant="icon" />
      </div>
      <h1 class="login-title">JTE Platform</h1>
      <p class="login-subtitle">车联网部标协议智能引擎</p>
      <form @submit.prevent="handleLogin" class="login-form">
        <div class="form-group">
          <input
            v-model="username"
            type="text"
            placeholder="用户名"
            class="login-input"
            autocomplete="username"
            :disabled="locked"
            required
          />
        </div>
        <div class="form-group">
          <input
            v-model="password"
            type="password"
            placeholder="密码"
            class="login-input"
            autocomplete="current-password"
            :disabled="locked"
            required
          />
        </div>
        <!-- 登录失败计数提示（防暴力破解） -->
        <div v-if="failureCount > 0 && !locked" class="warn-msg">
          登录失败 {{ failureCount }} 次，连续失败 5 次将锁定账户 15 分钟
        </div>
        <!-- 账户锁定提示 -->
        <div v-if="locked" class="lockout-msg">
          账户已锁定，请 {{ lockoutRemaining }} 后重试
        </div>
        <div v-if="errorMsg" class="error-msg">{{ errorMsg }}</div>
        <button type="submit" class="login-btn" :disabled="loading || locked">
          {{ locked ? '已锁定' : (loading ? '登录中...' : '登 录') }}
        </button>
      </form>
      <div class="login-footer">
        <span>高性能接入 · 智能分析 · 自然交互</span>
      </div>
      <!-- 等保2.0 合规标识 -->
      <div class="compliance-badge">
        <span class="badge-item">等保 2.0 三级</span>
        <span class="badge-divider">·</span>
        <span class="badge-item">国密 SM2/SM3/SM4</span>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted, onUnmounted } from 'vue'
import { useRouter } from 'vue-router'
import { usePermissionStore } from '../stores/permission'
import { collectDeviceFingerprint, setCSRFToken } from '../utils/security'
import Logo from '../components/Logo.vue'

const router = useRouter()
const permStore = usePermissionStore()

const username = ref('')
const password = ref('')
const loading = ref(false)
const errorMsg = ref('')
// 防暴力破解：失败计数 + 锁定状态
const failureCount = ref(0)
const locked = ref(false)
const lockoutRemaining = ref('')
let lockoutTimer = null

// 登录失败计数持久化（防止刷新页面重置计数）
const FAILURE_KEY = 'jte_login_failures'
const LOCK_KEY = 'jte_login_locked_until'

const loadLockState = () => {
  try {
    const until = parseInt(localStorage.getItem(LOCK_KEY) || '0', 10)
    if (until && Date.now() < until) {
      locked.value = true
      updateLockoutRemaining(until)
      startLockoutTimer(until)
    } else if (until) {
      // 锁定已过期，清理
      localStorage.removeItem(LOCK_KEY)
      localStorage.removeItem(FAILURE_KEY)
    } else {
      failureCount.value = parseInt(localStorage.getItem(FAILURE_KEY) || '0', 10)
    }
  } catch { /* ignore */ }
}

const saveFailureCount = () => {
  try {
    localStorage.setItem(FAILURE_KEY, String(failureCount.value))
  } catch { /* ignore */ }
}

const updateLockoutRemaining = (until) => {
  const ms = until - Date.now()
  if (ms <= 0) {
    locked.value = false
    lockoutRemaining.value = ''
    localStorage.removeItem(LOCK_KEY)
    localStorage.removeItem(FAILURE_KEY)
    failureCount.value = 0
    return
  }
  const minutes = Math.floor(ms / 60000)
  const seconds = Math.floor((ms % 60000) / 1000)
  lockoutRemaining.value = `${minutes}分${seconds.toString().padStart(2, '0')}秒`
}

const startLockoutTimer = (until) => {
  if (lockoutTimer) clearInterval(lockoutTimer)
  lockoutTimer = setInterval(() => {
    updateLockoutRemaining(until)
    if (!locked.value) {
      clearInterval(lockoutTimer)
      lockoutTimer = null
    }
  }, 1000)
}

onMounted(() => {
  loadLockState()
})

onUnmounted(() => {
  if (lockoutTimer) clearInterval(lockoutTimer)
})

const handleLogin = async () => {
  if (locked.value) return
  errorMsg.value = ''
  loading.value = true
  try {
    // AUTO-FIX-2026-07-02 [防克隆]: 采集设备指纹随登录请求上报
    // 后端用于：新设备告警、多IP检测、异地登录检测、鉴权码绑定校验
    const fingerprint = collectDeviceFingerprint()
    // 将指纹特征序列化为字符串（后端用 SM3 摘要生成统一指纹哈希）
    const fingerprintStr = [
      fingerprint.user_agent,
      fingerprint.accept_language,
      fingerprint.screen,
      fingerprint.timezone,
      fingerprint.platform,
      fingerprint.canvas_hash,
      fingerprint.webgl_hash,
      fingerprint.fonts_hash,
    ].join('|')

    await permStore.login(username.value, password.value, fingerprintStr)
    // 登录成功：清除失败计数
    failureCount.value = 0
    localStorage.removeItem(FAILURE_KEY)
    // 设置 CSRF token（从登录响应中获取，由 store 传入）
    if (permStore.csrfToken) {
      setCSRFToken(permStore.csrfToken)
    }
    router.push('/')
  } catch (e) {
    const status = e.status || 0
    const message = e.message || '登录失败'

    // AUTO-FIX-2026-07-02 [防克隆]: 处理 429 账户锁定
    if (status === 429) {
      locked.value = true
      // 后端锁定 15 分钟（与 LoginGuard.lockoutDuration 一致）
      const until = Date.now() + 15 * 60 * 1000
      try { localStorage.setItem(LOCK_KEY, String(until)) } catch { /* ignore */ }
      updateLockoutRemaining(until)
      startLockoutTimer(until)
      errorMsg.value = '账户已被锁定，请稍后重试'
    } else if (status === 401) {
      // 登录失败：递增失败计数
      failureCount.value += 1
      saveFailureCount()
      const remaining = 5 - failureCount.value
      if (remaining > 0) {
        errorMsg.value = `登录失败，还剩 ${remaining} 次尝试机会`
      } else {
        // 达到 5 次后后端应返回 429，但兼容前端兜底
        errorMsg.value = '登录失败次数过多，账户即将锁定'
      }
    } else {
      errorMsg.value = message
    }
  } finally {
    loading.value = false
  }
}
</script>

<style scoped>
.login-container {
  min-height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  background: #0a0e17;
  position: relative;
  overflow: hidden;
}
.login-bg-effects {
  position: absolute;
  inset: 0;
  pointer-events: none;
}
.bg-grid {
  position: absolute;
  inset: 0;
  background-image:
    linear-gradient(rgba(99, 102, 241, 0.03) 1px, transparent 1px),
    linear-gradient(90deg, rgba(99, 102, 241, 0.03) 1px, transparent 1px);
  background-size: 40px 40px;
}
.bg-glow {
  position: absolute;
  border-radius: 50%;
  filter: blur(100px);
}
.glow-1 {
  width: 400px;
  height: 400px;
  background: radial-gradient(circle, rgba(59, 130, 246, 0.15), transparent 70%);
  top: -100px;
  left: -100px;
}
.glow-2 {
  width: 500px;
  height: 500px;
  background: radial-gradient(circle, rgba(139, 92, 246, 0.12), transparent 70%);
  bottom: -150px;
  right: -150px;
}
.login-card {
  background: rgba(26, 31, 46, 0.85);
  backdrop-filter: blur(20px);
  border: 1px solid rgba(99, 102, 241, 0.15);
  border-radius: 16px;
  padding: 48px 40px 36px;
  width: 420px;
  box-shadow: 0 25px 50px -12px rgba(0, 0, 0, 0.5), 0 0 0 1px rgba(99, 102, 241, 0.1);
  position: relative;
  z-index: 1;
}
.login-logo {
  display: flex;
  justify-content: center;
  margin-bottom: 20px;
}
.login-title {
  color: #f1f5f9;
  font-size: 28px;
  text-align: center;
  margin: 0 0 8px;
  font-weight: 700;
  letter-spacing: -0.02em;
  background: linear-gradient(135deg, #fff 0%, #c7d2fe 100%);
  -webkit-background-clip: text;
  -webkit-text-fill-color: transparent;
  background-clip: text;
}
.login-subtitle {
  color: #64748b;
  text-align: center;
  margin: 0 0 32px;
  font-size: 14px;
}
.login-form {
  display: flex;
  flex-direction: column;
  gap: 16px;
}
.login-input {
  width: 100%;
  padding: 14px 16px;
  background: rgba(15, 19, 32, 0.6);
  border: 1px solid #2d3548;
  border-radius: 10px;
  color: #f1f5f9;
  font-size: 14px;
  outline: none;
  transition: all 0.2s;
  box-sizing: border-box;
}
.login-input:focus {
  border-color: #6366f1;
  box-shadow: 0 0 0 3px rgba(99, 102, 241, 0.15);
  background: rgba(15, 19, 32, 0.8);
}
.login-input::placeholder {
  color: #475569;
}
.login-input:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
.error-msg {
  color: #ef4444;
  font-size: 13px;
  text-align: center;
  padding: 8px;
  background: rgba(239, 68, 68, 0.1);
  border-radius: 8px;
}
.warn-msg {
  color: #f59e0b;
  font-size: 12px;
  text-align: center;
  padding: 6px;
  background: rgba(245, 158, 11, 0.1);
  border-radius: 8px;
}
.lockout-msg {
  color: #ef4444;
  font-size: 13px;
  text-align: center;
  padding: 10px;
  background: rgba(239, 68, 68, 0.15);
  border-radius: 8px;
  border: 1px solid rgba(239, 68, 68, 0.3);
}
.login-btn {
  width: 100%;
  padding: 14px;
  background: linear-gradient(135deg, #3b82f6 0%, #6366f1 50%, #8b5cf6 100%);
  color: white;
  border: none;
  border-radius: 10px;
  font-size: 15px;
  font-weight: 600;
  cursor: pointer;
  transition: all 0.2s;
  margin-top: 8px;
  letter-spacing: 0.05em;
}
.login-btn:hover:not(:disabled) {
  transform: translateY(-1px);
  box-shadow: 0 10px 25px -5px rgba(99, 102, 241, 0.4);
}
.login-btn:active:not(:disabled) {
  transform: translateY(0);
}
.login-btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
  transform: none;
}
.login-footer {
  margin-top: 24px;
  text-align: center;
  font-size: 12px;
  color: #475569;
  letter-spacing: 0.05em;
}
.compliance-badge {
  margin-top: 16px;
  text-align: center;
  font-size: 11px;
  color: #475569;
}
.badge-item {
  color: #6366f1;
}
.badge-divider {
  margin: 0 6px;
  color: #334155;
}
</style>
