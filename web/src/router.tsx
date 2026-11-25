import { lazy, Suspense } from 'react'
import { createBrowserRouter, Navigate } from 'react-router-dom'
import { AppShell } from '@/layout/AppShell'
import { LoginPage } from '@/pages/LoginPage'
import { NotFoundPage } from '@/pages/NotFoundPage'
import { LoadingState } from '@/components/ui/LoadingState'

// 独立成块，不拖慢首屏。ConversationsPage 是最常用入口，保留同类动态导入以保持一致。
const ConversationsPage = lazy(() => import('@/pages/ConversationsPage').then((m) => ({ default: m.ConversationsPage })))
const TasksPage = lazy(() => import('@/pages/TasksPage').then((m) => ({ default: m.TasksPage })))
const OverviewPage = lazy(() => import('@/pages/OverviewPage').then((m) => ({ default: m.OverviewPage })))
const WorkspacePage = lazy(() => import('@/pages/WorkspacePage').then((m) => ({ default: m.WorkspacePage })))
const FindingsPage = lazy(() => import('@/pages/FindingsPage').then((m) => ({ default: m.FindingsPage })))
const ModelCallsPage = lazy(() => import('@/pages/ModelCallsPage').then((m) => ({ default: m.ModelCallsPage })))
const AuditPage = lazy(() => import('@/pages/AuditPage').then((m) => ({ default: m.AuditPage })))
const AgentsPage = lazy(() => import('@/pages/AgentsPage').then((m) => ({ default: m.AgentsPage })))
const ToolsPage = lazy(() => import('@/pages/ToolsPage').then((m) => ({ default: m.ToolsPage })))
const ScenariosPage = lazy(() => import('@/pages/ScenariosPage').then((m) => ({ default: m.ScenariosPage })))
const SkillsPage = lazy(() => import('@/pages/SkillsPage').then((m) => ({ default: m.SkillsPage })))
const TrafficPage = lazy(() => import('@/pages/TrafficPage').then((m) => ({ default: m.TrafficPage })))
const ModelPage = lazy(() => import('@/pages/ModelPage').then((m) => ({ default: m.ModelPage })))
const SettingsPage = lazy(() => import('@/pages/SettingsPage').then((m) => ({ default: m.SettingsPage })))
const ExplorationGraphPage = lazy(() => import('@/pages/ExplorationGraphPage').then((m) => ({ default: m.ExplorationGraphPage })))
const CredentialsPage = lazy(() => import('@/pages/CredentialsPage').then((m) => ({ default: m.CredentialsPage })))
const CorpusPage = lazy(() => import('@/pages/CorpusPage').then((m) => ({ default: m.CorpusPage })))
const LogsPage = lazy(() => import('@/pages/LogsPage').then((m) => ({ default: m.LogsPage })))

function Loading() {
  return <LoadingState className="h-full" />
}

function withSuspense(el: React.ReactNode) {
  return <Suspense fallback={<Loading />}>{el}</Suspense>
}

// 路由表：根用 AppShell 外壳，子路由为各业务页，一律是资源名的一级路径（与后端接口的资源同名：
// /agents、/models、/settings……），不按侧边栏分组再加一层前缀——分组是看的方式，页面的地址只说
// 它是哪个资源。
// 对话：按来源分两个 tab——人工下发（source=direct，用户下 brief → AI planner 自主派子代理作战）
// 与代理捕获（source=proxy，挂代理收流量 → AI 逐批分析挖洞）。来源轴与场景/引擎正交。
// path 对齐后端 assignment.source：两个子路由都等于 source 枚举值。
export const router = createBrowserRouter([
  // 登录页独立于 AppShell（登录前没有侧栏壳体）；直接导入不 lazy——
  // 它是未登录访问者的第一屏，懒加载只多一次 chunk 往返。
  { path: '/login', element: <LoginPage /> },
  {
    path: '/',
    element: <AppShell />,
    children: [
      { index: true, element: <Navigate to="/conversations/direct" replace /> },
      { path: 'overview', element: withSuspense(<OverviewPage />) },
      // 任务：批次（assignment）→ 任务（task）列表与下发，位于对话上游。
      { path: 'tasks', element: withSuspense(<TasksPage />) },
      {
        path: 'conversations',
        children: [
          { index: true, element: <Navigate to="/conversations/direct" replace /> },
          { path: 'direct', element: withSuspense(<ConversationsPage source="direct" />) },
          { path: 'proxy', element: withSuspense(<ConversationsPage source="proxy" />) },
        ],
      },
      { path: 'findings', element: withSuspense(<FindingsPage />) },
      { path: 'traffic', element: withSuspense(<TrafficPage />) },
      { path: 'model-calls', element: withSuspense(<ModelCallsPage />) },
      { path: 'audit', element: withSuspense(<AuditPage />) },
      { path: 'exploration-graph', element: withSuspense(<ExplorationGraphPage />) },
      { path: 'workspace', element: withSuspense(<WorkspacePage />) },
      { path: 'agents', element: withSuspense(<AgentsPage />) },
      { path: 'scenarios', element: withSuspense(<ScenariosPage />) },
      { path: 'tools', element: withSuspense(<ToolsPage />) },
      { path: 'skills', element: withSuspense(<SkillsPage />) },
      { path: 'models', element: withSuspense(<ModelPage />) },
      // 系统级的运行参数（运行时、上下文压缩、代理过滤）：与后端 /settings 同名
      { path: 'settings', element: withSuspense(<SettingsPage />) },
      { path: 'credentials', element: withSuspense(<CredentialsPage />) },
      { path: 'corpus', element: withSuspense(<CorpusPage />) },
      // 日志：各组件运行日志（api 尾读 logs/ 目录），运维排障视图。
      { path: 'logs', element: withSuspense(<LogsPage />) },
      // 兜底：未匹配路径在外壳内渲染 404，侧栏仍可导航（不落到路由库默认报错页）。
      { path: '*', element: <NotFoundPage /> },
    ],
  },
])
