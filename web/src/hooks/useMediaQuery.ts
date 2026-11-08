import { useCallback, useSyncExternalStore } from 'react'

/**
 * 订阅 CSS 媒体查询，首帧即返回当前结果（同步读取，无先错后对的布局闪烁）。
 * 环境不支持 matchMedia（jsdom / 非浏览器）时返回 fallback。
 */
export function useMediaQuery(query: string, fallback = false): boolean {
  const supported = typeof window !== 'undefined' && typeof window.matchMedia === 'function'

  const subscribe = useCallback(
    (onChange: () => void) => {
      if (!supported) return () => {}
      const mql = window.matchMedia(query)
      mql.addEventListener('change', onChange)
      return () => mql.removeEventListener('change', onChange)
    },
    [query, supported],
  )

  const getSnapshot = () => (supported ? window.matchMedia(query).matches : fallback)

  return useSyncExternalStore(subscribe, getSnapshot, () => fallback)
}
