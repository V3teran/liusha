import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { KeyRound, LogOut, UserRound } from 'lucide-react'
import { logout as apiLogout } from '@/api/auth'
import { useAuthStore } from '@/stores/auth'
import { cn } from '@/lib/utils'
import { ChangePasswordDialog } from './ChangePasswordDialog'

/**
 * 侧栏底部账号菜单（个人头像）。
 *
 * 头像是中性的账号图标（UserRound）而非用户名首字母——首字母用于多用户产品区分
 * 身份；单管理员平台只有一个账号，字母没有区分价值，图标才是「账号菜单」的正确语义。
 *
 * 菜单用 Radix DropdownMenu：经 portal 逃出侧栏的 overflow 裁剪，并与 Dialog 同处一套
 * 分层栈——窄屏下侧栏收进模态抽屉，模态层会把 body 设为 pointer-events:none，
 * 手写的 body 浮层因此点不到；同栈的菜单层叠在抽屉之上、可交互。外点 / Escape 关闭、
 * 方向键在菜单项间移动由它负责。
 * 登出 = 服务端吊销会话 + 清 Cookie，本地清账号态回登录页（请求失败也照走——
 * 会话可能已失效，留在原页无意义）。
 */
export function UserMenu({ collapsed }: { collapsed: boolean }) {
  const navigate = useNavigate()
  const username = useAuthStore((s) => s.username)
  const clear = useAuthStore((s) => s.clear)
  const [pwdOpen, setPwdOpen] = useState(false)
  const [loggingOut, setLoggingOut] = useState(false)

  async function handleLogout() {
    if (loggingOut) return
    setLoggingOut(true)
    try {
      await apiLogout()
    } catch {
      /* 会话可能已失效：服务端清不掉也照常回登录页 */
    }
    clear()
    navigate('/login', { replace: true })
  }

  return (
    <div className="relative">
      <DropdownMenu.Root>
        <DropdownMenu.Trigger asChild>
          <button
            type="button"
            aria-label={`账号 ${username}`}
            title={collapsed ? username : undefined}
            className={cn(
              'flex h-8 w-8 cursor-pointer items-center justify-center rounded-full bg-accent-soft text-accent',
              'ring-1 ring-accent/20 transition-all hover:bg-accent-softer hover:scale-105',
            )}
          >
            <UserRound className="h-4 w-4" strokeWidth={2.2} />
          </button>
        </DropdownMenu.Trigger>
        <DropdownMenu.Portal>
          <DropdownMenu.Content
            side="top"
            align="start"
            sideOffset={8}
            aria-label="账号菜单"
            className="app-menu w-52 rounded-xl border border-border bg-surface py-1.5 shadow-lg"
          >
            <div className="border-b border-border px-3.5 pb-2 pt-1.5">
              <div className="truncate text-[13px] font-semibold text-text">{username}</div>
              <div className="mt-0.5 text-[11px] text-text-tertiary">管理员</div>
            </div>
            <div className="py-1">
              <DropdownMenu.Item
                onSelect={() => setPwdOpen(true)}
                className="flex w-full cursor-pointer items-center gap-2.5 px-3.5 py-1.5 text-[13px] text-text-secondary outline-none transition-colors data-[highlighted]:bg-surface-2 data-[highlighted]:text-text"
              >
                <KeyRound className="h-3.5 w-3.5" />
                修改密码
              </DropdownMenu.Item>
              <DropdownMenu.Item
                onSelect={() => void handleLogout()}
                disabled={loggingOut}
                className="flex w-full cursor-pointer items-center gap-2.5 px-3.5 py-1.5 text-[13px] text-text-secondary outline-none transition-colors data-[highlighted]:bg-status-error/10 data-[highlighted]:text-status-error"
              >
                <LogOut className="h-3.5 w-3.5" />
                {loggingOut ? '退出中…' : '退出登录'}
              </DropdownMenu.Item>
            </div>
          </DropdownMenu.Content>
        </DropdownMenu.Portal>
      </DropdownMenu.Root>

      <ChangePasswordDialog open={pwdOpen} onOpenChange={setPwdOpen} />
    </div>
  )
}
