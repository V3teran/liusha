import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { RouterProvider } from 'react-router-dom'
import { router } from './router'
import { ConfirmHost } from '@/components/ui/confirm'
import { Toaster } from '@/components/ui/toast'
// 自托管字体（按 Unicode 区段分片，浏览器只下载页面用到的字符）：正文 Plex Sans 可变字重，
// 等宽 Plex Mono（标识符、地址、数字、代码）各字重。离线部署不依赖外网。
import '@fontsource-variable/ibm-plex-sans'
import '@fontsource/ibm-plex-mono/400.css'
import '@fontsource/ibm-plex-mono/500.css'
import '@fontsource/ibm-plex-mono/600.css'
import '@fontsource/ibm-plex-mono/700.css'
import './index.css'

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      retry: 1,
      refetchOnWindowFocus: false,
    },
  },
})

// 直接挂载：登录态探测（GET /auth/me）在 AppShell 守卫内完成，
// 未登录由路由守卫转入 /login，无需在此阻塞启动。
createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
      <ConfirmHost />
      <Toaster />
    </QueryClientProvider>
  </StrictMode>,
)
