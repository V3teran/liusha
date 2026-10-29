import type { ComponentType } from 'react'
import { NavLink, Outlet, useLocation } from 'react-router-dom'
import {
  Activity,
  BookOpen,
  Bot,
  Boxes,
  Bug,
  Cpu,
  GitBranch,
  KeyRound,
  ListChecks,
  MessageSquare,
  Moon,
  ScrollText,
  SlidersHorizontal,
  Sun,
  Wrench,
} from 'lucide-react'
import { useTheme } from '@/hooks/useTheme'
import { cn } from '@/lib/utils'

interface NavItem {
  to: string
  label: string
  icon: ComponentType<{ className?: string }>
  match?: string // 高亮判定前缀（子路由聚合），缺省用 to 精确前缀
}

const NAV_ITEMS: NavItem[] = [
  // 任务是对话的上游容器（批次 → 任务 → 对话），置于对话之上。
  { to: '/tasks', label: '任务', icon: ListChecks },
  { to: '/conversations/manual', label: '对话', icon: MessageSquare, match: '/conversations' },
  { to: '/findings', label: '漏洞', icon: Bug },
  { to: '/traffic', label: '代理流量', icon: Activity },
  { to: '/exploration-graph', label: '探索图', icon: GitBranch },
  { to: '/credentials', label: '凭证库', icon: KeyRound },
  { to: '/config/agents', label: '智能体', icon: Bot },
  { to: '/config/tools', label: '工具', icon: Wrench },
  { to: '/config/skills', label: '技能', icon: BookOpen },
  { to: '/llm-audit', label: 'LLM 审计', icon: Cpu },
  { to: '/config/models', label: 'LLM 配置', icon: Boxes },
  // 运维视图（三进程运行日志）收尾，紧邻系统配置。
  { to: '/logs', label: '日志', icon: ScrollText },
  { to: '/config/system', label: '系统配置', icon: SlidersHorizontal },
]

/**
 * AppShell - 应用主框架
 */
export function AppShell() {
  const { theme, toggle } = useTheme()
  const location = useLocation()

  return (
    <div className="flex h-screen bg-background text-text">
      {/* 侧边栏 */}
      <aside className="flex w-64 shrink-0 flex-col border-r border-border bg-surface shadow-sm">
        {/* Logo 区域 */}
        <div className="flex h-16 items-center gap-3 border-b border-border px-5">
          <div className="flex h-9 w-9 items-center justify-center rounded-lg bg-gradient-to-br from-accent/20 to-accent/10 ring-1 ring-accent/20">
            <img src="/logo.svg" alt="" className="h-6 w-6" />
          </div>
          <div className="flex flex-col">
            <span className="brand-mark font-mono text-base font-bold tracking-tight">liusha</span>
            <span className="text-xs text-text-tertiary">智能化渗透测试平台</span>
          </div>
        </div>

        {/* 导航列表 */}
        <nav className="flex flex-1 flex-col gap-1 overflow-y-auto px-3 py-4">
          {NAV_ITEMS.map((item) => {
            const active = location.pathname.startsWith(item.match ?? item.to)
            const Icon = item.icon
            return (
              <NavLink
                key={item.to}
                to={item.to}
                className={cn(
                  'group flex items-center gap-3 rounded-lg px-3 py-2.5 text-sm font-medium',
                  'transition-all duration-200',
                  active
                    ? 'bg-accent-soft text-accent shadow-[var(--glow-accent)]'
                    : 'text-text-secondary hover:bg-surface-2 hover:text-text'
                )}
              >
                <Icon
                  className={cn(
                    'h-4.5 w-4.5 flex-shrink-0 transition-transform duration-200',
                    active ? 'scale-110' : 'group-hover:scale-105'
                  )}
                />
                <span>{item.label}</span>
              </NavLink>
            )
          })}
        </nav>

        {/* 底部控制栏：主题切换 */}
        <div className="flex items-center justify-end border-t border-border px-4 py-3.5">
          <button
            type="button"
            onClick={toggle}
            aria-label={theme === 'dark' ? '切换到浅色模式' : '切换到深色模式'}
            className={cn(
              'rounded-lg p-2 text-text-secondary transition-all',
              'hover:bg-surface-2 hover:text-text hover:scale-110'
            )}
          >
            {theme === 'dark' ? (
              <Sun className="h-4 w-4" />
            ) : (
              <Moon className="h-4 w-4" />
            )}
          </button>
        </div>
      </aside>

      {/* 主内容区 */}
      <main className="min-w-0 flex-1 overflow-hidden">
        <Outlet />
      </main>
    </div>
  )
}
