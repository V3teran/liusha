// 路由表：根用 AppShell 外壳，子路由为各业务页。
// hash 模式——后端把 SPA 挂在 /viewer 静态前缀下，hash 路由免去 base 配置。
// meta.title 供顶栏/侧栏复用；nav.ts 单独定义侧栏顺序与图标。
import { createRouter, createWebHashHistory, type RouteRecordRaw } from 'vue-router'
import AppShell from '../layout/AppShell.vue'

const routes: RouteRecordRaw[] = [
  {
    path: '/',
    component: AppShell,
    children: [
      { path: '', redirect: '/chat' },
      { path: 'dashboard', name: 'dashboard', component: () => import('../views/PlaceholderView.vue'), meta: { title: '总览' } },
      { path: 'chat', name: 'chat', component: () => import('../views/ChatView.vue'), meta: { title: '主动扫描' } },
      { path: 'findings', name: 'findings', component: () => import('../views/FindingsView.vue'), meta: { title: '漏洞发现' } },
      { path: 'sitemap', name: 'sitemap', component: () => import('../views/SitemapView.vue'), meta: { title: '攻击面' } },
      { path: 'sessions', name: 'sessions', component: () => import('../views/SessionsView.vue'), meta: { title: '被动扫描' } },
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
