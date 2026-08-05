import type { ComponentType } from 'react'
import { NavLink, Outlet, useLocation } from 'react-router-dom'
import {
  Bot,
  Bug,
  Cpu,
  KeyRound,
  MessageSquare,
  Moon,
  Network,
  Settings,
  Sun,
  Target,
  Waypoints,
} from 'lucide-react'
import { useTheme } from '@/hooks/useTheme'
import { cn } from '@/lib/utils'

interface NavItem {
  to: string
  label: string
  icon: ComponentType<{ className?: string }>
  match?: string // 高亮判定前缀（子路由聚合），缺省用 to 精确前缀
}

// 「对话」导航项覆盖 /conversations/manual 与 /conversations/auto 两个子路由，
// 高亮判定用 startsWith 而非精确匹配。
const NAV_ITEMS: NavItem[] = [
  { to: '/conversations/manual', label: '对话', icon: MessageSquare, match: '/conversations' },
  { to: '/findings', label: '漏洞管理', icon: Bug },
  { to: '/sitemap', label: '攻击面', icon: Waypoints },
  { to: '/attack-graph', label: '执行图', icon: Network },
  { to: '/llm-audit', label: 'LLM 审计', icon: Cpu },
  { to: '/credentials', label: '凭证库', icon: KeyRound },
  { to: '/config/scenarios', label: '场景', icon: Target },
  { to: '/config/hunters', label: '智能体', icon: Bot },
]

export function AppShell() {
  const { theme, toggle } = useTheme()
  const location = useLocation()

  return (
    <div className="flex h-screen bg-background text-text">
      <aside className="flex w-56 shrink-0 flex-col border-r border-border bg-surface">
        <div className="flex h-14 items-center gap-2 px-4">
          <img src="/logo.svg" alt="" className="h-6 w-6" />
          <span className="brand-mark font-mono text-sm font-semibold tracking-tight">liusha</span>
        </div>
        <nav className="flex flex-1 flex-col gap-1 overflow-y-auto px-2 py-2">
          {NAV_ITEMS.map((item) => {
            const active = location.pathname.startsWith(item.match ?? item.to)
            const Icon = item.icon
            return (
              <NavLink
                key={item.to}
                to={item.to}
                className={cn(
                  'flex items-center gap-3 rounded-md px-3 py-2 text-sm transition-colors',
                  active
                    ? 'bg-accent-soft font-medium text-accent shadow-[var(--glow-accent)]'
                    : 'text-muted hover:bg-surface-2 hover:text-text',
                )}
              >
                <Icon className="h-4 w-4" />
                {item.label}
              </NavLink>
            )
          })}
        </nav>
        <div className="flex items-center justify-between border-t border-border px-4 py-3">
          <NavLink
            to="/settings"
            className="flex items-center gap-2 text-sm text-muted hover:text-text"
          >
            <Settings className="h-4 w-4" />
            设置
          </NavLink>
          <button
            type="button"
            onClick={toggle}
            aria-label={theme === 'dark' ? '切换到浅色' : '切换到深色'}
            className="rounded-md p-1.5 text-muted hover:bg-surface-2 hover:text-text"
          >
            {theme === 'dark' ? <Sun className="h-4 w-4" /> : <Moon className="h-4 w-4" />}
          </button>
        </div>
      </aside>
      <main className="min-w-0 flex-1 overflow-hidden">
        <Outlet />
      </main>
    </div>
  )
}
