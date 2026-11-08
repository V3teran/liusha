import { useEffect, useState } from 'react'
import { Navigate, Outlet } from 'react-router-dom'
import * as Dialog from '@radix-ui/react-dialog'
import { Menu } from 'lucide-react'
import { useTheme } from '@/hooks/useTheme'
import { useMediaQuery } from '@/hooks/useMediaQuery'
import { useAuthStore } from '@/stores/auth'
import { cn } from '@/lib/utils'
import { BrandMark, Sidebar } from './Sidebar'

const RAIL_STORAGE_KEY = 'liusha-sidebar-collapsed'

function readRailPreference(): boolean {
  try {
    return localStorage.getItem(RAIL_STORAGE_KEY) === '1'
  } catch {
    return false
  }
}

/**
 * AppShell - 应用主框架（登录后壳体，未登录由守卫转入 /login）。
 *
 * 响应式三档（断点对齐 Tailwind md / lg）：
 *   ≥ 1024  常驻侧栏，完整 256px ↔ 图标栏 80px 由用户切换（偏好持久化）
 *   768–1023 常驻图标栏（强制）：内容区让出宽度给密集表格与分栏详情
 *   < 768   侧栏收入左侧抽屉，顶部 48px 栏提供菜单入口；跳转后自动收起
 */
export function AppShell() {
  const { theme } = useTheme()
  const authStatus = useAuthStore((s) => s.status)
  const bootstrap = useAuthStore((s) => s.bootstrap)

  // 无 matchMedia 的环境（jsdom 等）按桌面处理。
  const isDesktop = useMediaQuery('(min-width: 1024px)', true)
  const isTabletUp = useMediaQuery('(min-width: 768px)', true)
  const isMobile = !isTabletUp

  const [railPreferred, setRailPreferred] = useState(readRailPreference)
  const toggleRail = () =>
    setRailPreferred((v) => {
      try {
        localStorage.setItem(RAIL_STORAGE_KEY, v ? '0' : '1')
      } catch {
        // 存储不可用（隐私模式等）时仅本次会话生效
      }
      return !v
    })
  const rail = !isDesktop || railPreferred

  const [drawerOpen, setDrawerOpen] = useState(false)
  // 视口回到平板及以上时抽屉形态不复存在，收起避免残留遮罩。
  useEffect(() => {
    if (!isMobile) setDrawerOpen(false)
  }, [isMobile])

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
    <div className="flex h-dvh flex-col bg-background text-text md:flex-row">
      {/* 键盘跳转主内容（WCAG 2.4.1）：聚焦时才可见，绝对定位不占 flex 流。 */}
      <a href="#main-content" className="skip-to-content">
        跳到主内容
      </a>

      {isMobile ? (
        <Dialog.Root open={drawerOpen} onOpenChange={setDrawerOpen}>
          <header className="flex h-12 flex-shrink-0 items-center gap-2 border-b border-border bg-surface px-3">
            <Dialog.Trigger asChild>
              <button
                type="button"
                aria-label="打开导航"
                className="flex h-9 w-9 items-center justify-center rounded-lg text-text-secondary transition-colors hover:bg-surface-2 hover:text-text"
              >
                <Menu className="h-5 w-5" />
              </button>
            </Dialog.Trigger>
            <BrandMark className="h-7 w-7" />
            <span className="brand-mark font-mono text-sm font-bold tracking-tight">liusha</span>
          </header>
          <Dialog.Portal>
            <Dialog.Overlay className="app-overlay fixed inset-0 z-50" />
            <Dialog.Content
              aria-describedby={undefined}
              className="app-drawer fixed inset-y-0 left-0 z-50 w-72 max-w-[85vw] border-r border-border bg-surface shadow-xl outline-none"
            >
              <Dialog.Title className="sr-only">导航</Dialog.Title>
              <Sidebar rail={false} onNavigate={() => setDrawerOpen(false)} />
            </Dialog.Content>
          </Dialog.Portal>
        </Dialog.Root>
      ) : (
        <aside
          className={cn(
            'flex-shrink-0 overflow-hidden border-r border-border bg-surface shadow-sm transition-[width] duration-200',
            rail ? 'w-20' : 'w-64',
          )}
        >
          <Sidebar rail={rail} onToggleRail={isDesktop ? toggleRail : undefined} />
        </aside>
      )}

      <main id="main-content" tabIndex={-1} className="min-h-0 min-w-0 flex-1 overflow-hidden">
        <Outlet />
      </main>
    </div>
  )
}
