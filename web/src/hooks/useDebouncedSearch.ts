import { useEffect, useState } from 'react'

/** 搜索防抖时延：输入停顿后才发起检索，避免逐字请求。 */
export const SEARCH_DEBOUNCE_MS = 300

/**
 * 搜索词防抖：输入停顿 SEARCH_DEBOUNCE_MS 后才同步；清空立即生效（清空是明确的意图，
 * 不该让用户等一拍才看到全量）。服务端检索的搜索框统一经它。
 */
export function useDebouncedSearch(query: string): string {
  const [debounced, setDebounced] = useState(query)
  useEffect(() => {
    if (query === '') {
      setDebounced('')
      return
    }
    const t = setTimeout(() => setDebounced(query), SEARCH_DEBOUNCE_MS)
    return () => clearTimeout(t)
  }, [query])
  return debounced
}
