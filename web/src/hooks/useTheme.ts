import { useCallback, useEffect, useSyncExternalStore } from 'react'

export type Theme = 'dark' | 'light'

const STORAGE_KEY = 'liusha-theme'
const SYSTEM_LIGHT_QUERY = '(prefers-color-scheme: light)'

// 主题是全局状态：各调用点经 useSyncExternalStore 读同一个来源、切换时通知全部订阅者
// （曾经每个调用点各持一份 useState，侧栏里切换后品牌徽记仍停在旧主题）。
//
// 取值优先级：用户主动选过的（localStorage）> 系统深浅偏好 > 深色。未选过时随系统
// 切换实时更新；主动切换一次即写入选择、此后不再跟随系统。index.html 的首屏脚本与此同口径。
const listeners = new Set<() => void>()

function notify() {
  listeners.forEach((fn) => fn())
}

function systemQuery(): MediaQueryList | null {
  return typeof window.matchMedia === 'function' ? window.matchMedia(SYSTEM_LIGHT_QUERY) : null
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener)
  const mq = systemQuery()
  mq?.addEventListener('change', listener)
  return () => {
    listeners.delete(listener)
    mq?.removeEventListener('change', listener)
  }
}

function readTheme(): Theme {
  const stored = localStorage.getItem(STORAGE_KEY)
  if (stored === 'dark' || stored === 'light') return stored
  return systemQuery()?.matches ? 'light' : 'dark'
}

function applyTheme(theme: Theme) {
  const root = document.documentElement
  // 冻结过渡 → 翻主题 → 下一帧解冻：切换成为单帧原子重绘，
  // 避免 body 即刻变色与其余表面 150–250ms 渐变拼贴出的闪烁。
  root.classList.add('theme-switching')
  root.classList.toggle('dark', theme === 'dark')
  requestAnimationFrame(() =>
    requestAnimationFrame(() => root.classList.remove('theme-switching')),
  )
}

/**
 * 深浅主题。首次访问跟随系统偏好（读不到按深色），用户切换后持久化到 localStorage。
 * 首次渲染前由 index.html 的内联脚本同步写 class，避免闪烁；这里只接管后续变化。
 */
export function useTheme() {
  const theme = useSyncExternalStore(subscribe, readTheme)

  useEffect(() => {
    applyTheme(theme)
  }, [theme])

  const toggle = useCallback(() => {
    localStorage.setItem(STORAGE_KEY, readTheme() === 'dark' ? 'light' : 'dark')
    notify()
  }, [])

  return { theme, toggle }
}
