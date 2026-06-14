<script setup lang="ts">
// 应用外壳：左侧栏（品牌 + 导航 + 主题/密钥）+ 顶栏（页名 + 操作）+ 主区 RouterView。
// 导航项顺序与图标在此集中声明；高亮交给 router-link-active。
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import {
  Dashboard, Crosshair, PlugConnected, Bug, Sitemap,
  Hierarchy, Cpu, Key, Settings, Sun, Moon,
} from '@vicons/tabler'
import { useTheme } from '../composables/useTheme'

const route = useRoute()
const { theme, toggle } = useTheme()

// 导航项平铺，均可点、同样式。主动扫描=对话发起 active；流量监听=被动代理流量分析。
const nav = [
  { to: '/dashboard', label: '总览', icon: Dashboard },
  { to: '/active-scan', label: '主动扫描', icon: Crosshair },
  { to: '/traffic', label: '流量监听', icon: PlugConnected },
  { to: '/findings', label: '漏洞发现', icon: Bug },
  { to: '/sitemap', label: '攻击面', icon: Sitemap },
  { to: '/agent-runs', label: 'Agent 任务', icon: Hierarchy },
  { to: '/llm-audit', label: 'LLM 审计', icon: Cpu },
  { to: '/credentials', label: '凭证库', icon: Key },
  { to: '/settings', label: '设置', icon: Settings },
]

const title = computed(() => (route.meta.title as string) || '流沙')
</script>

<template>
  <div class="shell">
    <aside class="sidebar">
      <div class="brand">
        <img class="brand-mark" src="/logo.svg" alt="流沙 Liusha" width="38" height="38" />
        <div class="brand-text">
          <strong>流沙 Liusha</strong>
          <small>智能渗透测试</small>
        </div>
      </div>

      <nav class="nav">
        <RouterLink v-for="n in nav" :key="n.to" :to="n.to" class="nav-item">
          <span class="nav-icon"><component :is="n.icon" /></span>
          <span class="nav-label">{{ n.label }}</span>
        </RouterLink>
      </nav>
    </aside>

    <div class="main-col">
      <header class="topbar">
        <h1 class="page-title">{{ title }}</h1>
        <div class="top-actions">
          <button class="icon-btn" :title="theme === 'dark' ? '切浅色' : '切深色'" @click="toggle">
            <component :is="theme === 'dark' ? Sun : Moon" />
          </button>
        </div>
      </header>
      <main class="content">
        <RouterView />
      </main>
    </div>
  </div>
</template>

<style scoped>
.shell {
  display: grid;
  grid-template-columns: 210px 1fr;
  height: 100vh;
  overflow: hidden;
}

/* ---- 侧栏 ---- */
.sidebar {
  display: flex;
  flex-direction: column;
  background: var(--surface);
  border-right: 1px solid var(--border);
  padding: 16px 12px;
  gap: 4px;
}
.brand {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 6px 8px 18px;
}
.brand-mark {
  width: 38px;
  height: 38px;
  border-radius: 11px;
  /* logo.svg 自带深色圆底 + 鲨齿剑/流沙，无需额外背景 */
  box-shadow: 0 4px 14px -4px rgba(220, 38, 38, 0.5);
  display: block;
}
.brand-text { display: flex; flex-direction: column; line-height: 1.25; }
.brand-text strong { font-size: 15px; letter-spacing: 0.3px; }
.brand-text small { color: var(--muted); font-size: 11px; }

.nav { display: flex; flex-direction: column; gap: 2px; flex: 1; overflow-y: auto; }
.nav-item {
  display: flex;
  align-items: center;
  gap: 11px;
  padding: 9px 12px;
  border-radius: 9px;
  color: var(--muted);
  text-decoration: none;
  font-size: 13.5px;
  position: relative;
  transition: background 0.15s, color 0.15s;
}
.nav-item:hover { background: var(--surface-2); color: var(--text); }
.nav-icon { display: grid; place-items: center; }
.nav-icon :deep(svg) { width: 19px; height: 19px; }
.nav-item.router-link-active {
  background: var(--primary-soft);
  color: var(--primary);
  font-weight: 600;
}
.nav-item.router-link-active::before {
  content: '';
  position: absolute;
  left: -12px;
  top: 50%;
  transform: translateY(-50%);
  width: 3px;
  height: 60%;
  border-radius: 0 3px 3px 0;
  background: var(--primary);
}


/* ---- 主列 ---- */
/* min-height:0 关键：main-col 是 .shell 的 grid item，默认 min-height:auto 会被内部内容
   撑破（超出 100vh 被 shell overflow:hidden 裁掉 → 看不到下面、无滚动条）。设 0 让内部
   content/thread 的 overflow 接管滚动。 */
.main-col { display: flex; flex-direction: column; min-width: 0; min-height: 0; }
.topbar {
  height: 56px;
  flex-shrink: 0;
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 22px;
  border-bottom: 1px solid var(--border);
  background: var(--bg);
}
.page-title { font-size: 16px; font-weight: 600; margin: 0; letter-spacing: 0.2px; }
.top-actions { display: flex; gap: 8px; }
.icon-btn {
  display: grid;
  place-items: center;
  width: 34px;
  height: 34px;
  border-radius: 9px;
  border: 1px solid var(--border);
  background: var(--surface);
  color: var(--muted);
  cursor: pointer;
}
.icon-btn:hover { color: var(--primary); border-color: var(--primary); }
.icon-btn :deep(svg) { width: 18px; height: 18px; }
.content { flex: 1; min-height: 0; overflow: hidden; }
</style>
