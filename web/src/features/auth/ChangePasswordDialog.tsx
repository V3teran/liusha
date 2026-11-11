import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import * as Dialog from '@radix-ui/react-dialog'
import { Eye, EyeOff, X } from 'lucide-react'
import { changePassword } from '@/api/auth'
import { useAuthStore } from '@/stores/auth'
import { byteLength, cn } from '@/lib/utils'
import { Button } from '@/components/ui/Button'
import { FIELD_LABEL_CLASS, INPUT_CLASS } from '@/components/ui/control'

/**
 * 修改密码弹层。
 *
 * 成功后服务端吊销该账号全部会话（含当前）——这里展示确认文案后引导回登录页，
 * 用新密码重新认证。这是有意的强失效语义：凭证变更后旧会话一律不再可信。
 */


/**
 * 展示用错误文案：剥掉 client 层的状态码前缀；非 HTTP 错误给统一连接提示。
 *（与 LoginPage 各持一份：全站就近自包含的既有风格，抽象两处反而绕。）
 */
function displayError(err: Error): string {
  const m = err.message.match(/^(?:GET|POST|PUT|PATCH|DELETE) \d+(?:: )?/)
  return m ? err.message.slice(m[0].length) : '无法连接到服务器，请检查网络或服务状态'
}

interface ChangePasswordDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
}

export function ChangePasswordDialog({ open, onOpenChange }: ChangePasswordDialogProps) {
  const navigate = useNavigate()
  const clear = useAuthStore((s) => s.clear)

  const [current, setCurrent] = useState('')
  const [next, setNext] = useState('')
  const [confirm, setConfirm] = useState('')
  const [reveal, setReveal] = useState(false)
  const [error, setError] = useState('')
  const [success, setSuccess] = useState(false)
  const [busy, setBusy] = useState(false)

  // 每次打开重置表单（含成功态）。
  useEffect(() => {
    if (open) {
      setCurrent('')
      setNext('')
      setConfirm('')
      setReveal(false)
      setError('')
      setSuccess(false)
      setBusy(false)
    }
  }, [open])

  function submit(e: React.FormEvent) {
    e.preventDefault()
    if (busy || success) return
    setError('')
    if (!current || !next || !confirm) {
      setError('请填写全部字段')
      return
    }
    // 长度按字节计，与后端 bcrypt 口径一致（上限 72 字节）。
    if (byteLength(next) < 8) {
      setError('新密码至少 8 位')
      return
    }
    if (byteLength(next) > 72) {
      setError('新密码至多 72 字节')
      return
    }
    if (next === current) {
      setError('新密码不能与当前密码相同')
      return
    }
    if (next !== confirm) {
      setError('两次输入的新密码不一致')
      return
    }
    setBusy(true)
    changePassword(current, next)
      .then(() => {
        setSuccess(true)
        // 全部会话已被服务端吊销：短暂展示结果后回登录页重新认证。
        window.setTimeout(() => {
          clear()
          navigate('/login', { replace: true })
        }, 1500)
      })
      .catch((err: Error) => setError(displayError(err)))
      .finally(() => setBusy(false))
  }

  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Portal>
        <Dialog.Overlay className="app-overlay" />
        <Dialog.Content className="tac-modal fixed left-1/2 top-1/2 w-[440px] max-w-[calc(100vw-48px)] -translate-x-1/2 -translate-y-1/2 rounded-xl border border-border bg-surface shadow-lg outline-none">
          <div className="flex items-center justify-between border-b border-border px-5 py-3.5">
            <Dialog.Title className="text-sm font-semibold text-text">修改密码</Dialog.Title>
            <Dialog.Close asChild>
              <button
                type="button"
                aria-label="关闭"
                className="rounded-md p-1 text-text-tertiary transition-colors hover:bg-surface-2 hover:text-text"
              >
                <X className="h-4 w-4" />
              </button>
            </Dialog.Close>
          </div>

          <form className="flex flex-col gap-4 px-5 py-4" onSubmit={submit}>
            {(
              [
                ['current', '当前密码', current, setCurrent, 'current-password'],
                ['next', '新密码', next, setNext, 'new-password'],
                ['confirm', '确认新密码', confirm, setConfirm, 'new-password'],
              ] as const
            ).map(([id, label, value, setValue, autoComplete]) => (
              <div key={id}>
                <label htmlFor={`pwd-${id}`} className={cn(FIELD_LABEL_CLASS, 'mb-1.5 block')}>
                  {label}
                </label>
                <div className="relative">
                  <input
                    id={`pwd-${id}`}
                    type={reveal ? 'text' : 'password'}
                    className={cn(INPUT_CLASS, 'pr-9')}
                    autoComplete={autoComplete}
                    value={value}
                    onChange={(e) => setValue(e.target.value)}
                    placeholder={id === 'next' ? '至少 8 位' : undefined}
                  />
                  {id === 'next' && (
                    <button
                      type="button"
                      onClick={() => setReveal((v) => !v)}
                      aria-label={reveal ? '隐藏密码' : '显示密码'}
                      className="absolute right-2.5 top-1/2 -translate-y-1/2 text-text-tertiary transition-colors hover:text-text"
                    >
                      {reveal ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
                    </button>
                  )}
                </div>
              </div>
            ))}

            {error && (
              <div className="text-xs text-status-error" role="alert">
                ⚠ {error}
              </div>
            )}
            {success && <div className="text-xs text-status-success">✓ 密码已修改，全部会话已下线，正在前往登录页…</div>}

            <div className="mt-1 flex justify-end gap-2.5">
              <Dialog.Close asChild>
                <Button variant="secondary">取消</Button>
              </Dialog.Close>
              <Button type="submit" disabled={busy || success}>
                {busy ? '提交中…' : '确认修改'}
              </Button>
            </div>
          </form>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  )
}
