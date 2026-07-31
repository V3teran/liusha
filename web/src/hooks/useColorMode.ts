import { useEffect, useState } from 'react'

export type ColorMode = 'dark' | 'light'

function readMode(): ColorMode {
  return document.documentElement.classList.contains('dark') ? 'dark' : 'light'
}

/**
 * 读当前深浅色模式，并在 <html> 的 class 变化时实时跟随。
 *
 * useTheme 用 class（.dark）表达主题，且切换点（AppShell）与本页不共享 state；
 * 故这里不复用 useTheme 的返回值（那样切主题时本页不会重渲染），改为直接观察
 * documentElement 的 class 变化——任何来源（AppShell 切换 / index.html 首屏内联脚本）
 * 改了主题都能被 React Flow 的 colorMode 立即接住，修掉 Controls/MiniMap 在暗色下显白块。
 */
export function useColorMode(): ColorMode {
  const [mode, setMode] = useState<ColorMode>(readMode)

  useEffect(() => {
    const obs = new MutationObserver(() => setMode(readMode()))
    obs.observe(document.documentElement, { attributes: true, attributeFilter: ['class'] })
    return () => obs.disconnect()
  }, [])

  return mode
}
