import { defineConfig } from 'vitest/config'
import vue from '@vitejs/plugin-vue'

// AUTO-FIX-2026-07-02: Vitest 配置
// 使用 jsdom 环境支持 localStorage / window.addEventListener 等 DOM API
export default defineConfig({
  plugins: [vue()],
  test: {
    environment: 'jsdom',
    globals: true,
    include: ['src/**/*.{test,spec}.js'],
    coverage: {
      provider: 'v8',
      reporter: ['text', 'json'],
    },
  },
})
