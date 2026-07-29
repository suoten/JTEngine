import { defineStore } from 'pinia'

// FIXED: [状态管理] app store 持久化到 localStorage，刷新后保持用户偏好 [2026-07-17]
const LS_SIDEBAR = 'jte_sidebar_collapsed'
const LS_MAP_PROVIDER = 'jte_map_provider'
const LS_THEME = 'jte_theme'

function loadFromLS(key, defaultVal) {
  try {
    const v = localStorage.getItem(key)
    return v !== null ? JSON.parse(v) : defaultVal
  } catch {
    return defaultVal
  }
}

export const useAppStore = defineStore('app', {
  state: () => ({
    // 侧边栏折叠状态（持久化）
    sidebarCollapsed: loadFromLS(LS_SIDEBAR, false),
    // 移动端侧边栏抽屉打开状态（非持久化）
    mobileSidebarOpen: false,
    // 地图引擎选择（持久化）
    mapProvider: loadFromLS(LS_MAP_PROVIDER, 'tianditu'),
    tiandituToken: '',
    // 主题模式：dark / light（持久化）
    theme: loadFromLS(LS_THEME, 'dark'),
    // 全局加载状态
    globalLoading: false,
  }),

  getters: {
    isDark: (state) => state.theme === 'dark',
  },

  actions: {
    toggleSidebar() {
      this.sidebarCollapsed = !this.sidebarCollapsed
      localStorage.setItem(LS_SIDEBAR, JSON.stringify(this.sidebarCollapsed))
    },
    setMapProvider(provider) {
      this.mapProvider = provider
      localStorage.setItem(LS_MAP_PROVIDER, JSON.stringify(provider))
    },
    toggleTheme() {
      this.theme = this.theme === 'dark' ? 'light' : 'dark'
      localStorage.setItem(LS_THEME, JSON.stringify(this.theme))
      this.applyTheme()
    },
    setTheme(theme) {
      this.theme = theme
      localStorage.setItem(LS_THEME, JSON.stringify(theme))
      this.applyTheme()
    },
    applyTheme() {
      if (typeof document === 'undefined') return
      const html = document.documentElement
      if (this.theme === 'dark') {
        html.classList.add('dark')
      } else {
        html.classList.remove('dark')
      }
    },
    toggleMobileSidebar() {
      this.mobileSidebarOpen = !this.mobileSidebarOpen
    },
    closeMobileSidebar() {
      this.mobileSidebarOpen = false
    },
    setGlobalLoading(val) {
      this.globalLoading = val
    },
  },
})
