import { useCallback, useEffect, useState } from 'react'

export type Theme = 'dark' | 'light'

const STORAGE_KEY = 'liusha-theme'

function readInitialTheme(): Theme {
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
  const [theme, setTheme] = useState<Theme>(readInitialTheme)

  useEffect(() => {
    applyTheme(theme)
    localStorage.setItem(STORAGE_KEY, theme)
  }, [theme])

  const toggle = useCallback(() => {
    setTheme((t) => (t === 'dark' ? 'light' : 'dark'))
  }, [])

  return { theme, toggle }
}
