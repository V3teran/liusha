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
      { path: '', redirect: '/conversations/active' },
      { path: 'dashboard', name: 'dashboard', component: () => import('../views/PlaceholderView.vue'), meta: { title: '总览' } },
      // 对话：渗透（active，用户下 brief → AI orchestrator 自主派子代理作战）与流量分析
      // （passive，挂代理收流量 → AI 逐批分析挖洞）统一为「对话」模块，主动/被动为其下两个 tab。
      // path 对齐后端 task.mode：两个子路由都等于 mode 枚举值。
      {
        path: 'conversations',
        redirect: '/conversations/active',
        children: [
          { path: 'active', name: 'conversations-active', component: () => import('../views/ConversationsView.vue'), meta: { title: '对话', mode: 'active' } },
          { path: 'passive', name: 'conversations-passive', component: () => import('../views/ConversationsView.vue'), meta: { title: '对话', mode: 'passive' } },
        ],
      },
      { path: 'findings', name: 'findings', component: () => import('../views/FindingsView.vue'), meta: { title: '漏洞管理' } },
      { path: 'sitemap', name: 'sitemap', component: () => import('../views/SitemapView.vue'), meta: { title: '攻击面' } },
      { path: 'attack-graph', name: 'attack-graph', component: () => import('../views/AttackGraphView.vue'), meta: { title: '执行图' } },
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
