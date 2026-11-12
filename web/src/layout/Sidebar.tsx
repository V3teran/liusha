import { Link, useLocation } from 'react-router-dom'
import { ChevronLeft, ChevronRight, Moon, Sun } from 'lucide-react'
import { useTheme } from '@/hooks/useTheme'
import { UserMenu } from '@/features/auth/UserMenu'
import { cn } from '@/lib/utils'
import { NAV_GROUPS, type NavItem } from './navigation'

interface SidebarProps {
  /** 图标栏形态（80px，仅图标 + 悬停原生 tooltip） */
  rail: boolean
  /** 收起/展开开关；不传则不渲染（视口强制形态下用户不可切换） */
  onToggleRail?: () => void
  /** 点击导航项后的回调（抽屉形态下用于关闭抽屉） */
  onNavigate?: () => void
}

/**
 * 品牌徽记，按主题取形态：
 * - 浅色：与登录页同一枚应用图标（黑剪影 + 近白渐变容器）。资产自带圆角与发丝描边，
 *   外面不再套容器——套 accent 着色容器会成灰底小剪影，与登录页对不上。
 * - 深色：反白剪影 + accent 着色容器（深底上的功能尺度徽记）。
 */
export function BrandMark({ className }: { className?: string }) {
  const { theme } = useTheme()
  if (theme === 'light') {
    return <img src="/brand/app-icon.svg" alt="liusha" className={cn('flex-shrink-0', className ?? 'h-9 w-9')} />
  }
  return (
    <span
      className={cn(
        'flex flex-shrink-0 items-center justify-center rounded-lg bg-gradient-to-br from-accent/20 to-accent/10 ring-1 ring-accent/20',
        className ?? 'h-9 w-9',
      )}
    >
      <img src="/brand/mark-white.svg" alt="liusha" className="h-2/3 w-2/3" />
    </span>
  )
}

/**
 * 侧栏内容（品牌行 + 分组导航 + 账号/主题控制）。桌面常驻侧栏与手机抽屉共用同一份内容，
 * 两种外壳只决定它放在哪里、以何种宽度出现。
 */
export function Sidebar({ rail, onToggleRail, onNavigate }: SidebarProps) {
  const { theme, toggle } = useTheme()
  const location = useLocation()

  return (
    <div className="flex h-full flex-col">
      {/* 品牌行：与页头同为 64px 高，底边线贯穿壳体 */}
      <div className={cn('flex h-16 flex-shrink-0 items-center border-b border-border px-3', rail && 'justify-center gap-1')}>
        <BrandMark />
        {!rail && (
          <div className="ml-3 min-w-0 flex-1">
            <span className="brand-mark block font-mono text-base font-bold leading-tight tracking-tight">liusha</span>
            <span className="block truncate text-xs leading-tight text-text-tertiary">LLM 智能化渗透测试平台</span>
          </div>
        )}
        {onToggleRail && (
          <button
            type="button"
            onClick={onToggleRail}
            aria-label={rail ? '展开侧边栏' : '收起侧边栏'}
            title={rail ? '展开侧边栏' : '收起侧边栏'}
            className={cn(
              'flex h-7 w-7 flex-shrink-0 items-center justify-center rounded-md text-text-secondary transition-colors hover:bg-surface-2 hover:text-text',
              !rail && 'ml-auto',
            )}
          >
            {rail ? <ChevronRight className="h-4 w-4" /> : <ChevronLeft className="h-4 w-4" />}
          </button>
        )}
      </div>

      <nav aria-label="主导航" className="flex flex-1 flex-col overflow-y-auto overflow-x-hidden px-3 py-3">
        {NAV_GROUPS.map((group, gi) => {
          const headingId = `nav-group-${gi}`
          return (
            <div key={group.label} role="group" aria-labelledby={headingId} className={cn(gi > 0 && 'mt-3')}>
              {/* 组名：展开态为小号标题；图标栏态视觉上以分隔线代替，读屏仍可读到组名 */}
              <div
                id={headingId}
                className={cn(
                  rail
                    ? 'sr-only'
                    : 'px-3 pb-1.5 pt-1 text-[11px] font-semibold uppercase tracking-wider text-text-tertiary',
                )}
              >
                {group.label}
              </div>
              {rail && gi > 0 && <div aria-hidden className="mx-2 mb-3 h-px bg-border" />}
              <ul className="flex flex-col gap-0.5">
                {group.items.map((item) => (
                  <li key={item.to}>
                    <SidebarLink item={item} rail={rail} active={location.pathname.startsWith(item.match ?? item.to)} onNavigate={onNavigate} />
                  </li>
                ))}
              </ul>
            </div>
          )
        })}
      </nav>

      {/* 底部控制栏：账号头像菜单（左）+ 主题切换（右）；图标栏态并排居中 */}
      <div
        className={cn(
          'flex flex-shrink-0 items-center gap-2 border-t border-border px-3 py-3.5',
          rail ? 'justify-center' : 'justify-between',
        )}
      >
        <UserMenu collapsed={rail} />
        <button
          type="button"
          onClick={toggle}
          aria-label={theme === 'dark' ? '切换到浅色模式' : '切换到深色模式'}
          className="rounded-lg p-2 text-text-secondary transition-colors hover:bg-surface-2 hover:text-text"
        >
          {theme === 'dark' ? <Sun className="h-4 w-4" /> : <Moon className="h-4 w-4" />}
        </button>
      </div>
    </div>
  )
}

function SidebarLink({ item, rail, active, onNavigate }: { item: NavItem; rail: boolean; active: boolean; onNavigate?: () => void }) {
  const Icon = item.icon
  return (
    <Link
      to={item.to}
      onClick={onNavigate}
      title={rail ? item.label : undefined}
      aria-label={rail ? item.label : undefined}
      // 用 Link 而非 NavLink：当前页由前缀匹配决定（/conversations/* 都算「对话」），
      // NavLink 只按自身 to 精确判定 aria-current，会漏掉聚合子路由。
      aria-current={active ? 'page' : undefined}
      className={cn(
        'group flex items-center rounded-lg text-sm font-medium transition-colors duration-150',
        rail ? 'h-9 justify-center' : 'gap-3 px-3 py-2',
        active
          ? 'bg-accent-soft text-accent shadow-[var(--glow-accent)]'
          : 'text-text-secondary hover:bg-surface-2 hover:text-text',
      )}
    >
      <Icon className="h-4.5 w-4.5 flex-shrink-0" />
      {!rail && <span className="truncate">{item.label}</span>}
    </Link>
  )
}
