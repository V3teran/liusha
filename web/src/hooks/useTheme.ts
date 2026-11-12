import { useCallback, useEffect, useSyncExternalStore } from 'react'

export type Theme = 'dark' | 'light'

const STORAGE_KEY = 'liusha-theme'

// 主题是全局状态，localStorage 是唯一事实源：各调用点经 useSyncExternalStore 读它、
// 切换时写它并通知全部订阅者。曾经每个调用点各持一份 useState，侧栏里切换主题后
// 品牌徽记等其它调用点仍停在旧主题。
const listeners = new Set<() => void>()

function subscribe(listener: () => void): () => void {
  listeners.add(listener)
  return () => listeners.delete(listener)
}

function readTheme(): Theme {
  const stored = localStorage.getItem(STORAGE_KEY)
  if (stored === 'dark' || stored === 'light') return stored
  return 'dark'
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
 * 深浅主题切换。默认深色（石墨深空），持久化到 localStorage。
 * 首次渲染前由 index.html 的内联脚本同步写 class，避免闪烁；这里只接管后续切换。
 */
export function useTheme() {
  const theme = useSyncExternalStore(subscribe, readTheme)

  useEffect(() => {
    applyTheme(theme)
  }, [theme])

  const toggle = useCallback(() => {
    localStorage.setItem(STORAGE_KEY, readTheme() === 'dark' ? 'light' : 'dark')
    listeners.forEach((notify) => notify())
  }, [])

  return { theme, toggle }
}
