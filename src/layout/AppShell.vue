<script setup lang="ts">
// 应用外壳：左侧栏（品牌 + 导航 + 主题/密钥）+ 顶栏（页名 + 操作）+ 主区 RouterView。
// 导航项顺序与图标在此集中声明；高亮交给 router-link-active。
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import {
  Dashboard, Message, Bug, Sitemap, PlugConnected,
  Crosshair, Hierarchy, Cpu, Key, Settings, Sun, Moon, Shield,
} from '@vicons/tabler'
import { useTheme } from '../composables/useTheme'

const route = useRoute()
const { theme, toggle } = useTheme()

const nav = [
  { to: '/dashboard', label: '总览', icon: Dashboard },
  { to: '/chat', label: '对话', icon: Message },
  { to: '/findings', label: '漏洞发现', icon: Bug },
  { to: '/sitemap', label: '攻击面', icon: Sitemap },
  { to: '/sessions', label: '被动会话', icon: PlugConnected },
  { to: '/scans', label: '主动扫描', icon: Crosshair },
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
        <span class="brand-mark"><Shield /></span>
        <div class="brand-text">
          <strong>流沙 Liusha</strong>
          <small>AI 渗透作战台</small>
        </div>
      </div>

      <nav class="nav">
        <RouterLink v-for="n in nav" :key="n.to" :to="n.to" class="nav-item">
          <span class="nav-icon"><component :is="n.icon" /></span>
          <span class="nav-label">{{ n.label }}</span>
        </RouterLink>
      </nav>

      <div class="side-foot">
        <button class="theme-toggle" @click="toggle">
          <span class="ti"><component :is="theme === 'dark' ? Sun : Moon" /></span>
          <span>{{ theme === 'dark' ? '浅色' : '深色' }}</span>
        </button>
      </div>
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
  grid-template-columns: 248px 1fr;
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
  display: grid;
  place-items: center;
  width: 38px;
  height: 38px;
  border-radius: 11px;
  background: linear-gradient(135deg, var(--primary), var(--accent));
  color: #fff;
  box-shadow: 0 4px 14px -4px var(--primary);
}
.brand-mark :deep(svg) { width: 22px; height: 22px; }
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

.side-foot { display: flex; flex-direction: column; gap: 8px; padding-top: 12px; }
.theme-toggle {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 12px;
  border-radius: 9px;
  border: 1px solid var(--border);
  background: var(--surface-2);
  color: var(--text);
  cursor: pointer;
  font-size: 13px;
}
.theme-toggle:hover { border-color: var(--primary); }
.theme-toggle .ti :deep(svg) { width: 16px; height: 16px; display: block; }

/* ---- 主列 ---- */
.main-col { display: flex; flex-direction: column; min-width: 0; }
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
