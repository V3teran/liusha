import { lazy, Suspense } from 'react'
import { createBrowserRouter, Navigate } from 'react-router-dom'
import { AppShell } from '@/layout/AppShell'
import { PlaceholderPage } from '@/pages/PlaceholderPage'

// 路由级代码分割：各业务页按需加载，尤其 AttackGraphPage（React Flow + dagre，体量大）
// 独立成块，不拖慢首屏。ConversationsPage 是最常用入口，保留同类动态导入以保持一致。
const ConversationsPage = lazy(() => import('@/pages/ConversationsPage').then((m) => ({ default: m.ConversationsPage })))
const FindingsPage = lazy(() => import('@/pages/FindingsPage').then((m) => ({ default: m.FindingsPage })))
const SitemapPage = lazy(() => import('@/pages/SitemapPage').then((m) => ({ default: m.SitemapPage })))
const LlmAuditPage = lazy(() => import('@/pages/LlmAuditPage').then((m) => ({ default: m.LlmAuditPage })))
const AttackGraphPage = lazy(() => import('@/pages/AttackGraphPage').then((m) => ({ default: m.AttackGraphPage })))

function Loading() {
  return <div className="flex h-full items-center justify-center text-sm text-muted">加载中…</div>
}

function withSuspense(el: React.ReactNode) {
  return <Suspense fallback={<Loading />}>{el}</Suspense>
}

// 路由表：根用 AppShell 外壳，子路由为各业务页。
// 对话：渗透（active，用户下 brief → AI orchestrator 自主派子代理作战）与流量分析
// （passive，挂代理收流量 → AI 逐批分析挖洞）统一为「对话」模块，主动/被动为其下两个 tab。
// path 对齐后端 task.mode：两个子路由都等于 mode 枚举值。
export const router = createBrowserRouter([
  {
    path: '/',
    element: <AppShell />,
    children: [
      { index: true, element: <Navigate to="/conversations/active" replace /> },
      { path: 'dashboard', element: <PlaceholderPage title="总览" /> },
      {
        path: 'conversations',
        children: [
          { index: true, element: <Navigate to="/conversations/active" replace /> },
          { path: 'active', element: withSuspense(<ConversationsPage mode="active" />) },
          { path: 'passive', element: withSuspense(<ConversationsPage mode="passive" />) },
        ],
      },
      { path: 'findings', element: withSuspense(<FindingsPage />) },
      { path: 'sitemap', element: withSuspense(<SitemapPage />) },
      { path: 'attack-graph', element: withSuspense(<AttackGraphPage />) },
      { path: 'llm-audit', element: withSuspense(<LlmAuditPage />) },
      { path: 'credentials', element: <PlaceholderPage title="凭证库" /> },
      { path: 'settings', element: <PlaceholderPage title="设置" /> },
    ],
  },
])
