// 用 vitest/config 的 defineConfig，使其识别 `test` 字段（vitest 4 不再通过
// `/// <reference types="vitest" />` 增强 vite 的 defineConfig）。
import path from 'node:path'
import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'

// dev：/api/* 与 SSE 代理到 Go API，浏览器视角同源 → cookie 走 SameSite=Lax、无需 CORS。
// 目标地址用 VITE_API_TARGET 覆盖（缺省 localhost:8080；若 api 用 LIUSHA_API_ADDR 改了端口，
// 启动时传 VITE_API_TARGET=http://localhost:8090 pnpm dev）。
const apiTarget = process.env.VITE_API_TARGET ?? 'http://localhost:8080'

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
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
    environmentOptions: {
      jsdom: {
        // 显式 http(s) origin 才能启用 Web Storage（jsdom 默认 about:blank 下 localStorage 不可用）。
        url: 'http://localhost/',
      },
    },
    globals: true,
    setupFiles: ['./src/test-setup.ts'],
    coverage: {
      // vitest 4：设置 include 后默认即统计全部匹配文件（`all` 字段已移除，语义由 include 隐式接管）。
      include: ['src/**/*.{ts,tsx}'],
      exclude: [
        'src/**/*.test.{ts,tsx}',
        'src/main.tsx',
        'src/router.tsx',
        'src/test-setup.ts',
        'src/components/ui/**',
        'src/vite-env.d.ts',
      ],
    },
  },
})
