import { createApp } from 'vue'
import ElementPlus from 'element-plus'
import 'element-plus/dist/index.css'
import 'element-plus/theme-chalk/dark/css-vars.css'
import * as ElementPlusIconsVue from '@element-plus/icons-vue'
import { createPinia } from 'pinia'
import { ElMessage } from 'element-plus'
import App from './App.vue'
import router from './router'
import i18n from './i18n'
import './styles/global.css'

const app = createApp(App)

// FIXED: [全局错误处理] 捕获未处理的 Vue 组件错误，防止白屏 [2026-07-17]
// FIXED: [全局错误处理] API 错误不弹 toast，避免刷屏 [2026-07-23]
app.config.errorHandler = (err, instance, info) => {
  console.error('[Vue Error]', info, err)
  // 避免在登录页等关键页面弹过多 toast
  // 过滤掉：NavigationDuplicated、Axios 网络错误（已被 API 拦截器处理）
  if (err?.message &&
      !err.message.includes('NavigationDuplicated') &&
      !err.message.includes('Network Error') &&
      !err.message.includes('timeout') &&
      !err.message.includes('Request failed') &&
      !err.message.includes('Request aborted') &&
      !(err.config && err.config.url) &&  // Axios error 对象（有 config.url）
      !(err.response && err.response.status) // Axios HTTP error（有 response.status）
  ) {
    ElMessage.error(`应用错误: ${err.message || '未知错误'}`)
  }
}

// FIXED: [全局错误处理] 捕获未处理的 Promise rejection [2026-07-17]
// FIXED: [全局错误处理] API/网络错误不弹 toast，避免刷屏 [2026-07-23]
if (typeof window !== 'undefined') {
  window.addEventListener('unhandledrejection', (event) => {
    console.error('[Unhandled Promise]', event.reason)
    const reason = event.reason
    if (!reason) return
    // Axios 错误对象（有 response 或 config 属性）→ 已被 API 拦截器 console.error 记录，不弹 toast
    if (reason.response || reason.config) return
    // 静默处理 WebSocket/Axios 网络错误，避免弹窗刷屏
    if (reason.message) {
      const msg = reason.message
      if (msg.includes('Network') || msg.includes('timeout') ||
          msg.includes('Request failed') || msg.includes('Request aborted') ||
          msg.includes('WebSocket') || msg.includes('socket')) {
        return
      }
      ElMessage.error(`请求失败: ${msg}`)
    }
  })
}

for (const [key, component] of Object.entries(ElementPlusIconsVue)) {
  app.component(key, component)
}

app.use(createPinia())
app.use(ElementPlus)
app.use(i18n)
app.use(router)

// FIXED: [主题] 应用启动时恢复主题设置 [2026-07-17]
import { useAppStore } from './stores/app'
const appStore = useAppStore()
appStore.applyTheme()

app.mount('#app')
