// 用 vitest/config 的 defineConfig，使其识别 `test` 字段（vitest 4 不再通过
// `/// <reference types="vitest" />` 增强 vite 的 defineConfig）。
import { defineConfig } from 'vitest/config'
import vue from '@vitejs/plugin-vue'

// dev：/api/* 与 SSE 代理到 Go API，浏览器视角同源 → cookie 走 SameSite=Lax、无需 CORS。
// 目标地址用 VITE_API_TARGET 覆盖（缺省 localhost:8080；若 api 用 LIUSHA_API_ADDR 改了端口，
// 启动时传 VITE_API_TARGET=http://localhost:8090 pnpm dev）。
const apiTarget = process.env.VITE_API_TARGET ?? 'http://localhost:8080'

export default defineConfig({
  plugins: [vue()],
  server: {
    proxy: {
      '/api': {
        target: apiTarget,
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
