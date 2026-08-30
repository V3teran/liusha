import { lazy, Suspense } from 'react'
import { createBrowserRouter, Navigate } from 'react-router-dom'
import { AppShell } from '@/layout/AppShell'
import { PlaceholderPage } from '@/pages/PlaceholderPage'

// 独立成块，不拖慢首屏。ConversationsPage 是最常用入口，保留同类动态导入以保持一致。
const ConversationsPage = lazy(() => import('@/pages/ConversationsPage').then((m) => ({ default: m.ConversationsPage })))
const FindingsPage = lazy(() => import('@/pages/FindingsPage').then((m) => ({ default: m.FindingsPage })))
const LlmAuditPage = lazy(() => import('@/pages/LlmAuditPage').then((m) => ({ default: m.LlmAuditPage })))
const ScenarioAdmin = lazy(() => import('@/pages/ScenarioAdmin').then((m) => ({ default: m.ScenarioAdmin })))
const AgentAdmin = lazy(() => import('@/pages/AgentAdmin').then((m) => ({ default: m.AgentAdmin })))
const ToolsPage = lazy(() => import('@/pages/ToolsPage').then((m) => ({ default: m.ToolsPage })))
const TrafficPage = lazy(() => import('@/pages/TrafficPage').then((m) => ({ default: m.TrafficPage })))
const ModelPage = lazy(() => import('@/pages/ModelPage').then((m) => ({ default: m.ModelPage })))
const SystemConfig = lazy(() => import('@/pages/SystemConfig').then((m) => ({ default: m.SystemConfig })))

function Loading() {
  return <div className="flex h-full items-center justify-center text-sm text-muted">加载中…</div>
}

function withSuspense(el: React.ReactNode) {
  return <Suspense fallback={<Loading />}>{el}</Suspense>
}

// 路由表：根用 AppShell 外壳，子路由为各业务页。
// 对话：按来源分两个 tab——主动下发（source=manual，用户下 brief → AI planner 自主派子代理作战）
// 与被动代理（source=auto，挂代理收流量 → AI 逐批分析挖洞）。来源轴与场景/引擎正交。
// path 对齐后端 assignment.source：两个子路由都等于 source 枚举值。
export const router = createBrowserRouter([
  {
    path: '/',
    element: <AppShell />,
    children: [
      { index: true, element: <Navigate to="/conversations/manual" replace /> },
      { path: 'dashboard', element: <PlaceholderPage title="总览" /> },
      {
        path: 'conversations',
        children: [
          { index: true, element: <Navigate to="/conversations/manual" replace /> },
          { path: 'manual', element: withSuspense(<ConversationsPage source="manual" />) },
          { path: 'auto', element: withSuspense(<ConversationsPage source="auto" />) },
        ],
      },
      { path: 'findings', element: withSuspense(<FindingsPage />) },
      { path: 'traffic', element: withSuspense(<TrafficPage />) },
      { path: 'llm-audit', element: withSuspense(<LlmAuditPage />) },
      {
        // 配置管理：场景/智能体（后端 agent）两资源各自独立页，侧栏平铺入口。
        path: 'config',
        children: [
          { index: true, element: <Navigate to="/config/scenarios" replace /> },
          { path: 'scenarios', element: withSuspense(<ScenarioAdmin />) },
          { path: 'agents', element: withSuspense(<AgentAdmin />) },
          { path: 'tools', element: withSuspense(<ToolsPage />) },
          { path: 'models', element: withSuspense(<ModelPage />) },
          // 旧「模型路由」独立路由已并入 /config/models（模块内 ?tab=assignment），重定向保链接可用。
          { path: 'routing', element: <Navigate to="/config/models?tab=assignment" replace /> },
          { path: 'system', element: withSuspense(<SystemConfig />) },
        ],
      },
      { path: 'credentials', element: <PlaceholderPage title="凭证库" /> },
      { path: 'settings', element: <PlaceholderPage title="设置" /> },
    ],
  },
])
