import { useEffect, useState } from 'react'
import type { ComponentType } from 'react'
import { Navigate, NavLink, Outlet, useLocation } from 'react-router-dom'
import {
  ChevronLeft,
  ChevronRight,
  Activity,
  FolderOpen,
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
import { useAuthStore } from '@/stores/auth'
import { UserMenu } from '@/features/auth/UserMenu'
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
  { to: '/conversations/direct', label: '对话', icon: MessageSquare, match: '/conversations' },
  { to: '/findings', label: '漏洞', icon: Bug },
  { to: '/traffic', label: '代理流量', icon: Activity },
  { to: '/exploration-graph', label: '探索图', icon: GitBranch },
  { to: '/workspace', label: '工作空间', icon: FolderOpen },
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
 * AppShell - 应用主框架（登录后壳体，未登录由路由守卫转入 /login）
 */
export function AppShell() {
  const { theme, toggle } = useTheme()
  const location = useLocation()
  const authStatus = useAuthStore((s) => s.status)
  const bootstrap = useAuthStore((s) => s.bootstrap)
  // 侧栏收缩态持久化（图标栏 ↔ 完整）；默认展开。
  const [collapsed, setCollapsed] = useState(() => localStorage.getItem('liusha-sidebar-collapsed') === '1')
  const toggleCollapsed = () =>
    setCollapsed((v) => {
      localStorage.setItem('liusha-sidebar-collapsed', v ? '0' : '1')
      return !v
    })

  // 登录态探测（GET /auth/me）：首帧一次。checking 期间渲染品牌 splash 防内容闪现。
  useEffect(() => {
    void bootstrap()
  }, [bootstrap])
  if (authStatus === 'checking') {
    return (
      <div className="flex h-screen items-center justify-center bg-background">
        <img
          src={theme === 'dark' ? '/brand/mark-white.svg' : '/brand/mark-black.svg'}
          alt="liusha"
          className="h-8 w-8 animate-pulse"
        />
      </div>
    )
  }
  if (authStatus === 'anonymous') {
    return <Navigate to="/login" replace />
  }

  return (
    <div className="flex h-screen bg-background text-text">
      {/* 侧边栏：完整 256px ↔ 图标栏 80px（收起后悬停原生 tooltip 显示页面名） */}
      <aside
        className={cn(
          'flex shrink-0 flex-col overflow-hidden border-r border-border bg-surface shadow-sm transition-[width] duration-200',
          collapsed ? 'w-20' : 'w-64',
        )}
      >
          {/* Logo 行：收起开关内嵌在行内（业界标准位置），不做悬浮异物——
              展开 = 圆标 + 品牌文案 + 收起钮；收起 = 圆标 + 展开钮并排居中。
              徽记按主题取正负形：深色主题反白剪影，浅色主题黑白剪影。 */}
          <div className="flex h-16 flex-shrink-0 items-center border-b border-border px-3">
            <div className="flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-lg bg-gradient-to-br from-accent/20 to-accent/10 ring-1 ring-accent/20">
              <img
                src={theme === 'dark' ? '/brand/mark-white.svg' : '/brand/mark-black.svg'}
                alt="liusha"
                className="h-6 w-6"
              />
            </div>
          {!collapsed && (
            <div className="ml-3 min-w-0 flex-1">
              <span className="brand-mark block font-mono text-base font-bold leading-tight tracking-tight">liusha</span>
              <span className="block truncate text-xs leading-tight text-text-tertiary">LLM 智能化渗透测试平台</span>
            </div>
          )}
          <button
            type="button"
            onClick={toggleCollapsed}
            aria-label={collapsed ? '展开侧边栏' : '收起侧边栏'}
            title={collapsed ? '展开侧边栏' : '收起侧边栏'}
            className="ml-auto flex h-7 w-7 flex-shrink-0 items-center justify-center rounded-md text-text-secondary transition-colors hover:bg-surface-2 hover:text-text"
          >
            {collapsed ? <ChevronRight className="h-4 w-4" /> : <ChevronLeft className="h-4 w-4" />}
          </button>
        </div>

        {/* 导航列表 */}
        <nav className="flex flex-1 flex-col gap-1 overflow-y-auto overflow-x-hidden px-3 py-4">
          {NAV_ITEMS.map((item) => {
            const active = location.pathname.startsWith(item.match ?? item.to)
            const Icon = item.icon
            return (
              <NavLink
                key={item.to}
                to={item.to}
                title={collapsed ? item.label : undefined}
                aria-label={collapsed ? item.label : undefined}
                className={cn(
                  'group flex items-center rounded-lg text-sm font-medium',
                  'transition-all duration-200',
                  collapsed ? 'h-9 justify-center' : 'gap-3 px-3 py-2.5',
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
                {!collapsed && <span>{item.label}</span>}
              </NavLink>
            )
          })}
        </nav>

        {/* 底部控制栏：账号头像菜单（左） + 主题切换（右）；收起态并排居中 */}
        <div
          className={cn(
            'flex flex-shrink-0 items-center gap-2 border-t border-border px-3 py-3.5',
            collapsed ? 'justify-center' : 'justify-between',
          )}
        >
          <UserMenu collapsed={collapsed} />
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
