// 路由表：根用 AppShell 外壳，子路由为各业务页。
// hash 模式——SPA 独立部署，hash 路由免去 base/history 回退配置。
// meta.title 供顶栏/侧栏复用；nav.ts 单独定义侧栏顺序与图标。
import { createRouter, createWebHashHistory, type RouteRecordRaw } from 'vue-router'
import AppShell from '../layout/AppShell.vue'

const routes: RouteRecordRaw[] = [
  {
    path: '/',
    component: AppShell,
    children: [
      { path: '', redirect: '/active' },
      { path: 'dashboard', name: 'dashboard', component: () => import('../views/PlaceholderView.vue'), meta: { title: '总览' } },
      // 渗透会话：用户下 brief → AI orchestrator 自主派子代理作战。此页发起 active 会话 + 观战。
      // path=/active 对齐后端 task.mode；页名避开 task/assignment 层级词（留给以后的批量/定时下发页）。
      { path: 'active', name: 'active', component: () => import('../views/ChatView.vue'), meta: { title: '渗透会话' } },
      { path: 'findings', name: 'findings', component: () => import('../views/FindingsView.vue'), meta: { title: '漏洞发现' } },
      { path: 'sitemap', name: 'sitemap', component: () => import('../views/SitemapView.vue'), meta: { title: '攻击面' } },
      { path: 'attack-graph', name: 'attack-graph', component: () => import('../views/AttackGraphView.vue'), meta: { title: '执行图' } },
      // 流量分析：挂代理收流量 → AI 逐批分析挖洞。独立工作区（左会话列表 + 右分析 feed）。
      { path: 'traffic', name: 'traffic', component: () => import('../views/SessionsView.vue'), meta: { title: '流量分析' } },
      { path: 'agent-runs', name: 'agent-runs', component: () => import('../views/AgentRunsView.vue'), meta: { title: 'Agent 任务' } },
      { path: 'llm-audit', name: 'llm-audit', component: () => import('../views/LlmAuditView.vue'), meta: { title: 'LLM 审计' } },
      { path: 'credentials', name: 'credentials', component: () => import('../views/PlaceholderView.vue'), meta: { title: '凭证库' } },
      { path: 'settings', name: 'settings', component: () => import('../views/PlaceholderView.vue'), meta: { title: '设置' } },
    ],
  },
]

export const router = createRouter({
  history: createWebHashHistory(),
  routes,
})
