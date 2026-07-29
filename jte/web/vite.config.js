import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// AUTO-FIX-2026-06-29: Vite 代码分割优化
// 目标：将大型第三方库从路由 chunk 中抽出，避免单 chunk 超过 500KB 警告。
// 策略：按 vendor 维度切分，库代码稳定可长期缓存，路由 chunk 仅含业务代码。
// - echarts: ~1MB，被 Reports/Storage/Overview/Alarms 等多个图表页共享
// - video-player (flv.js + hls.js): ~700KB，仅 Video.vue 使用
// - element-plus: ~600KB，全站 UI 库
// - vue-vendor: vue/vue-router/pinia/vue-i18n 核心
// - vendor: 其他第三方库兜底
export default defineConfig({
  plugins: [vue()],
  server: {
    port: 3000,
    proxy: {
      '/api': {
        target: 'http://localhost:8090',
        changeOrigin: true,
      },
    },
  },
  build: {
    outDir: 'dist',
    // 阈值设为 1100KB：已知 vendor chunk（echarts ~1MB）会超 600KB 默认阈值，
    // 这些是单库整体打包无法再拆。设高阈值避免误报，但仍能捕获路由 chunk
    // 意外打包大型库的真正问题（路由 chunk 应 < 100KB）。
    chunkSizeWarningLimit: 1100,
    rollupOptions: {
      // AUTO-FIX-2026-07-01: 静默第三方依赖（@vueuse/core，element-plus 传递依赖）的
      // "/* #__PURE__ */" 注解位置警告。这些警告来自 node_modules 内部代码，Rollup 会
      // 自动移除注解避免问题，对产物无影响。过滤后满足"npm run build 无警告"验收标准。
      onwarn(warning, defaultHandler) {
        if (
          warning.code === 'INVALID_ANNOTATION' &&
          warning.loc?.file?.includes('node_modules')
        ) {
          return
        }
        defaultHandler(warning)
      },
      output: {
        manualChunks(id) {
          if (!id.includes('node_modules')) {
            return undefined
          }
          // echarts 全家桶：echarts + vue-echarts + zrender（渲染引擎）
          if (
            id.includes('/echarts/') ||
            id.includes('/vue-echarts/') ||
            id.includes('/zrender/')
          ) {
            return 'echarts'
          }
          // 视频播放器：flv.js + hls.js，仅 Video 路由使用
          if (id.includes('/flv.js/') || id.includes('/hls.js/')) {
            return 'video-player'
          }
          // Element Plus 全家桶：组件库 + 图标
          if (id.includes('/element-plus/') || id.includes('/@element-plus/')) {
            return 'element-plus'
          }
          // Vue 生态核心与其他第三方依赖合并为单一 vendor chunk
          // 注：曾尝试拆分 vue-vendor，但 @vueuse/core(vendor) ↔ vue(vue-vendor)
          // 存在循环依赖导致 rollup 警告。合并后 vendor 总量 ~350KB 仍在阈值内，
          // 且避免了循环 chunk 警告。
          return 'vendor'
        },
      },
    },
  },
})
