import { useEffect, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { KeyRound, LogOut } from 'lucide-react'
import { logout as apiLogout } from '@/api/auth'
import { useAuthStore } from '@/stores/auth'
import { cn } from '@/lib/utils'
import { ChangePasswordDialog } from './ChangePasswordDialog'

/**
 * 侧栏底部账号菜单（个人头像）。
 *
 * 头像 = 用户名首字母的 accent 圆标；点击弹出向上菜单（自绘 popover：
 * 外点关闭 + Escape），提供「修改密码」与「退出登录」。侧栏收起态只显示头像圆标。
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
  const rootRef = useRef<HTMLDivElement>(null)

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

  // 外点关闭；Escape 关闭。菜单随开合挂卸监听，不留全局监听器。
  useEffect(() => {
    if (!open) return
    const onDown = (e: MouseEvent) => {
      if (rootRef.current && !rootRef.current.contains(e.target as Node)) setOpen(false)
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

  const initial = (username || '?').charAt(0).toUpperCase()

  return (
    <div ref={rootRef} className="relative">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-label={`账号 ${username}`}
        aria-expanded={open}
        title={collapsed ? username : undefined}
        className={cn(
          'flex h-8 w-8 items-center justify-center rounded-full bg-accent-soft text-[13px] font-bold text-accent',
          'ring-1 ring-accent/20 transition-all hover:bg-accent-softer hover:scale-105',
        )}
      >
        {initial}
      </button>

      {open && (
        <div className="absolute bottom-full right-0 mb-2 w-52 rounded-xl border border-border bg-surface py-1.5 shadow-lg">
          <div className="border-b border-border px-3.5 pb-2 pt-1.5">
            <div className="truncate text-[13px] font-semibold text-text">{username}</div>
            <div className="mt-0.5 text-[11px] text-text-tertiary">管理员</div>
          </div>
          <div className="py-1">
            <button
              type="button"
              onClick={() => {
                setOpen(false)
                setPwdOpen(true)
              }}
              className="flex w-full items-center gap-2.5 px-3.5 py-1.5 text-[13px] text-text-secondary transition-colors hover:bg-surface-2 hover:text-text"
            >
              <KeyRound className="h-3.5 w-3.5" />
              修改密码
            </button>
            <button
              type="button"
              onClick={handleLogout}
              disabled={loggingOut}
              className="flex w-full items-center gap-2.5 px-3.5 py-1.5 text-[13px] text-text-secondary transition-colors hover:bg-sev-critical/10 hover:text-sev-critical"
            >
              <LogOut className="h-3.5 w-3.5" />
              {loggingOut ? '退出中…' : '退出登录'}
            </button>
          </div>
        </div>
      )}

      <ChangePasswordDialog open={pwdOpen} onOpenChange={setPwdOpen} />
    </div>
  )
}
