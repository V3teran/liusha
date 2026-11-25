import { useCallback, useEffect, useState } from 'react'

/** 搜索防抖时延：输入停顿后才发起检索，避免逐字请求。 */
export const SEARCH_DEBOUNCE_MS = 300

/**
 * 搜索词防抖的状态：输入停顿 SEARCH_DEBOUNCE_MS 后才同步；清空立即生效（清空是明确的意图，
 * 不该让用户等一拍才看到全量）。用于「词由页面自己持有、查询按词发请求」的页面（词不进 URL）。
 *
 *   - debounced：生效的词，查询按它发请求
 *   - pending：输入框里有还没生效的词
 *   - flush：不等防抖，现在就让输入框里的词生效（搜索钮与回车）
 */
export function useDebouncedSearchState(query: string) {
  const [debounced, setDebounced] = useState(query)
  useEffect(() => {
    if (query === '') {
      setDebounced('')
      return
    }
    const t = setTimeout(() => setDebounced(query), SEARCH_DEBOUNCE_MS)
    return () => clearTimeout(t)
  }, [query])
  const flush = useCallback(() => setDebounced(query), [query])
  return { debounced, pending: debounced !== query, flush }
}

/** 只要防抖后的词时用它（见 useDebouncedSearchState）。 */
export function useDebouncedSearch(query: string): string {
  return useDebouncedSearchState(query).debounced
}

/**
 * 搜索框的输入草稿与提交：用于「词要交给上层」的页面（筛选栏把词经 onChange 交给页面，页面写进
 * URL 或发请求）——输入停顿 SEARCH_DEBOUNCE_MS 后才把草稿交出去，不等防抖就发会逐字请求；
 * 清空立即提交。
 *
 * submit 现在就把草稿交出去（有还没生效的词时）：回车走它。
 *
 * 草稿跟着 value 走：上层改写了词（清空筛选、浏览器前进后退）时输入框即时回填。
 *
 * normalize 是交给上层之前对草稿做的规范化（如去掉首尾空白）。有没有未生效的词按规范化之后
 * 比较：否则输入「abc 」（带尾空格）会被当成还没生效，搜索钮一直显示「搜索」。
 */
export function useSearchDraft(
  value: string,
  onChange: (next: string) => void,
  normalize: (draft: string) => string = (d) => d,
) {
  const [draft, setDraft] = useState(value)
  useEffect(() => {
    setDraft(value)
  }, [value])
  /** 输入框里有还没生效的词：规范化后的草稿与当前生效的值不同 */
  const pending = normalize(draft) !== value
  useEffect(() => {
    if (!pending) return
    if (draft === '') {
      onChange('')
      return
    }
    const next = normalize(draft)
    const timer = setTimeout(() => onChange(next), SEARCH_DEBOUNCE_MS)
    return () => clearTimeout(timer)
  }, [draft, pending, onChange, normalize])
  // 回车：有还没生效的词就现在提交，不等防抖。重取最新数据是刷新钮的事（由页面交给搜索框）
  const submit = useCallback(() => {
    if (pending) onChange(normalize(draft))
  }, [draft, pending, onChange, normalize])
  return { draft, setDraft, submit, pending }
}
