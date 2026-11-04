import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Eye, EyeOff } from 'lucide-react'
import * as authApi from '@/api/auth'
import { useAuthStore } from '@/stores/auth'
import { cn } from '@/lib/utils'

/**
 * 登录页（AppShell 之外的独立路由）。
 *
 * 会话是 HttpOnly Cookie，前端只拿到用户名。初始管理员由 api 首启时用
 * env 密码建立（无建号端点），这里只做登录。
 * 视觉遵循品牌体系：徽记按主题取正负形成对（深底 mark-white / 浅底 mark-black），
 * 输入框 / 按钮 / 错误内联反馈均用全站既有 token 与类名，不另造样式。
 */

const INPUT_CLASS =
  'h-9 w-full rounded-lg border border-border bg-surface px-3 text-sm text-text outline-none transition-all placeholder:text-text-tertiary focus:border-accent focus:glow-accent'

/**
 * 展示用错误文案：剥掉 client 层的状态码前缀；非 HTTP 错误（网络不可达/
 * 后端未启动）给统一提示，不把浏览器原生报错裸露给用户。
 */
function displayError(err: Error): string {
  const m = err.message.match(/^(?:GET|POST|PUT|PATCH|DELETE) \d+(?:: )?/)
  return m ? err.message.slice(m[0].length) : '无法连接到服务器，请检查网络或服务状态'
}

export function LoginPage() {
  const navigate = useNavigate()
  const setAccount = useAuthStore((s) => s.setAccount)

  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [reveal, setReveal] = useState(false)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  // 已登录直进主页（未登录的 me() 401 静默落在登录页）。
  useEffect(() => {
    let alive = true
    authApi
      .me()
      .then(() => {
        if (alive) navigate('/', { replace: true })
      })
      .catch(() => undefined)
    return () => {
      alive = false
    }
  }, [navigate])

  function submit(e: React.FormEvent) {
    e.preventDefault()
    if (busy) return
    setError('')
    if (!username || !password) {
      setError('请填写用户名和密码')
      return
    }
    setBusy(true)
    authApi
      .login(username, password)
      .then((me) => {
        setAccount(me.username)
        navigate('/', { replace: true })
      })
      .catch((err: Error) => setError(displayError(err)))
      .finally(() => setBusy(false))
  }

  return (
    <div className="relative flex min-h-screen items-center justify-center overflow-hidden bg-background px-4">
      {/* 战术辉光背景：主题 token，浅色主题下近乎不可见，深色主题点缀氛围 */}
      <div
        aria-hidden
        className="pointer-events-none absolute left-1/2 top-1/3 h-[420px] w-[680px] -translate-x-1/2 -translate-y-1/2 rounded-full opacity-60"
        style={{ background: 'radial-gradient(closest-side, var(--glow-accent), transparent)' }}
      />
      <div className="relative w-full max-w-[400px] rounded-2xl border border-border bg-surface p-8 shadow-lg">
        {/* 品牌区：应用图标（黑白形态，即浅色系形态——黑剪影必配浅底，容器为
            #ffffff→#e7eaef 近白斜向渐变）+ 等宽品牌字。
            深色主题（默认）下呈现为深卡面上的一枚浅色亮图标，浅色主题下融入卡面
            ——资产自带 rx=28 圆角容器与 8% 发丝描边（专为浅底边缘分离而设），
            外面不再加 ring/rounded，避免双边缘。侧栏维持按主题切换剪影：
            那是功能尺度的 chrome，此处是品牌 hero。 */}
        <div className="flex flex-col items-center gap-3">
          <img src="/brand/app-icon.svg" alt="liusha" className="h-12 w-12" />
          <div className="text-center">
            <span className="brand-mark block font-mono text-xl font-bold leading-tight tracking-tight">liusha</span>
            <span className="mt-0.5 block text-xs text-text-tertiary">LLM 智能化渗透测试平台</span>
          </div>
        </div>

        <form className="mt-7 flex flex-col gap-4" onSubmit={submit}>
          <div>
            <label htmlFor="login-username" className="mb-1.5 block text-[12.5px] font-semibold text-text">
              用户名
            </label>
            <input
              id="login-username"
              name="username"
              className={INPUT_CLASS}
              autoComplete="username"
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              placeholder="用户名"
              autoFocus
            />
          </div>
          <div>
            <label htmlFor="login-password" className="mb-1.5 block text-[12.5px] font-semibold text-text">
              密码
            </label>
            <div className="relative">
              <input
                id="login-password"
                name="password"
                type={reveal ? 'text' : 'password'}
                className={cn(INPUT_CLASS, 'pr-9')}
                autoComplete="current-password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                placeholder="密码"
              />
              <button
                type="button"
                onClick={() => setReveal((v) => !v)}
                aria-label={reveal ? '隐藏密码' : '显示密码'}
                className="absolute right-2.5 top-1/2 -translate-y-1/2 text-text-tertiary transition-colors hover:text-text"
              >
                {reveal ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
              </button>
            </div>
          </div>

          {error && (
            <div className="text-xs text-sev-critical" role="alert">
              ⚠ {error}
            </div>
          )}

          <button
            type="submit"
            disabled={busy}
            className={cn(
              'mt-1 h-9.5 rounded-lg bg-accent-soft text-sm font-semibold text-accent transition-all',
              'hover:bg-accent-softer hover:outline hover:outline-1 hover:outline-accent-soft',
              busy && 'cursor-default opacity-45 hover:bg-accent-soft hover:outline-none',
            )}
          >
            {busy ? '登录中…' : '登录'}
          </button>
        </form>

        <p className="mt-5 text-center text-[11.5px] leading-relaxed text-text-tertiary">
          会话有效期 7 天，修改密码后全端重新登录
        </p>
      </div>
    </div>
  )
}
