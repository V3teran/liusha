import { useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { useNavigate } from 'react-router-dom'
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
 * 弹层经 portal 挂 document.body 并按按钮实际坐标 fixed 定位：侧栏是
 * overflow-hidden 的窄容器（展开 256px / 收起 80px），菜单 208px 在容器内
 * 无论怎么锚定都会被裁剪——浮层逃出裁剪上下文是唯一正确做法。
 * 外点 / Escape 关闭（portal 后外点判定须同时覆盖按钮与浮层两个节点）。
 * 登出 = 服务端吊销会话 + 清 Cookie，本地清账号态回登录页（请求失败也照走——
 * 会话可能已失效，留在原页无意义）。
 */
export function UserMenu({ collapsed }: { collapsed: boolean }) {
  const navigate = useNavigate()
  const username = useAuthStore((s) => s.username)
  const clear = useAuthStore((s) => s.clear)
  const [open, setOpen] = useState(false)
  const [pwdOpen, setPwdOpen] = useState(false)
  const [loggingOut, setLoggingOut] = useState(false)
  const [pos, setPos] = useState<{ left: number; bottom: number } | null>(null)
  const buttonRef = useRef<HTMLButtonElement>(null)
  const menuRef = useRef<HTMLDivElement>(null)

  function toggle() {
    if (open) {
      setOpen(false)
      return
    }
    // 以按钮左上角为锚：菜单从按钮左缘向右上展开——收起态窄栏也不会被裁。
    const rect = buttonRef.current!.getBoundingClientRect()
    setPos({ left: rect.left, bottom: window.innerHeight - rect.top + 8 })
    setOpen(true)
  }

  useEffect(() => {
    if (!open) return
    const onDown = (e: MouseEvent) => {
      const t = e.target as Node
      if (!buttonRef.current?.contains(t) && !menuRef.current?.contains(t)) setOpen(false)
    }
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setOpen(false)
    }
    document.addEventListener('mousedown', onDown)
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('mousedown', onDown)
      document.removeEventListener('keydown', onKey)
    }
  }, [open])

  async function handleLogout() {
    if (loggingOut) return
    setOpen(false)
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
      <button
        ref={buttonRef}
        type="button"
        onClick={toggle}
        aria-label={`账号 ${username}`}
        aria-expanded={open}
        title={collapsed ? username : undefined}
        className={cn(
          'flex h-8 w-8 cursor-pointer items-center justify-center rounded-full bg-accent-soft text-accent',
          'ring-1 ring-accent/20 transition-all hover:bg-accent-softer hover:scale-105',
        )}
      >
        <UserRound className="h-4 w-4" strokeWidth={2.2} />
      </button>

      {open && pos &&
        createPortal(
          <div
            ref={menuRef}
            role="menu"
            aria-label="账号菜单"
            style={{ left: pos.left, bottom: pos.bottom }}
            className="animate-fade-in fixed z-50 w-52 rounded-xl border border-border bg-surface py-1.5 shadow-lg"
          >
            <div className="border-b border-border px-3.5 pb-2 pt-1.5">
              <div className="truncate text-[13px] font-semibold text-text">{username}</div>
              <div className="mt-0.5 text-[11px] text-text-tertiary">管理员</div>
            </div>
            <div className="py-1">
              <button
                type="button"
                role="menuitem"
                onClick={() => {
                  setOpen(false)
                  setPwdOpen(true)
                }}
                className="flex w-full cursor-pointer items-center gap-2.5 px-3.5 py-1.5 text-[13px] text-text-secondary transition-colors hover:bg-surface-2 hover:text-text"
              >
                <KeyRound className="h-3.5 w-3.5" />
                修改密码
              </button>
              <button
                type="button"
                role="menuitem"
                onClick={handleLogout}
                disabled={loggingOut}
                className="flex w-full cursor-pointer items-center gap-2.5 px-3.5 py-1.5 text-[13px] text-text-secondary transition-colors hover:bg-status-error/10 hover:text-status-error"
              >
                <LogOut className="h-3.5 w-3.5" />
                {loggingOut ? '退出中…' : '退出登录'}
              </button>
            </div>
          </div>,
          document.body,
        )}

      <ChangePasswordDialog open={pwdOpen} onOpenChange={setPwdOpen} />
    </div>
  )
}
