// 用 vitest/config 的 defineConfig，使其识别 `test` 字段（vitest 4 不再通过
// `/// <reference types="vitest" />` 增强 vite 的 defineConfig）。
import { defineConfig } from 'vitest/config'
import vue from '@vitejs/plugin-vue'

// dev：/api/* 与 SSE 代理到 Go API（默认 localhost:8080），浏览器视角同源 →
// cookie 走 SameSite=Lax、无需 CORS。
export default defineConfig({
  plugins: [vue()],
  server: {
    proxy: {
      '/api': {
        target: 'http://localhost:8080',
        changeOrigin: true,
        rewrite: (p) => p.replace(/^\/api/, ''),
      },
    },
  },
  test: {
    environment: 'jsdom',
    globals: true,
  },
})
