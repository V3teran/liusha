import { useMemo } from 'react'
import { useSearchParams } from 'react-router-dom'
import { DEFAULT_PAGE_SIZE, PAGE_SIZE_OPTIONS } from '@/lib/pageSize'

// URL 分页/筛选状态的单一实现（此前 task/traffic/finding/llm-audit 四个
// hook 各自复制同一套 numOr 解析、size 白名单校验、setOrDel 写回、reset——
// 语义漂移已实际发生：useFindingFilters 漏了 resetFilters）。
//
// 用法：领域 hook 定义「字段名 → URL 参数名」映射并组合两个原语，
// 对外仍暴露各自类型的 filters facade（URL 结构不变，历史链接不受影响）。

function numOr(raw: string | null, fallback: number): number {
  const n = Number(raw)
  return Number.isFinite(n) && n > 0 ? n : fallback
}

/** URL 分页状态：page（1-based）/ size（白名单校验）。 */
export function useUrlPageState() {
  const [searchParams, setSearchParams] = useSearchParams()

  const page = useMemo(() => numOr(searchParams.get('page'), 1), [searchParams])
  const size = useMemo(() => {
    const raw = numOr(searchParams.get('size'), DEFAULT_PAGE_SIZE)
    return (PAGE_SIZE_OPTIONS as readonly number[]).includes(raw) ? raw : DEFAULT_PAGE_SIZE
  }, [searchParams])

  const setPage = (next: number) => {
    setSearchParams((prev) => {
      const p = new URLSearchParams(prev)
      if (next > 1) p.set('page', String(next))
      else p.delete('page')
      return p
    })
  }
  // 换每页条数：回到第一页（否则「第 3 页 · 100 条/页」可能落在数据末尾之外）。
  const setSize = (next: number) => {
    setSearchParams((prev) => {
      const p = new URLSearchParams(prev)
      // 设回默认值时删除参数（干净 URL 语义）
      if (next !== DEFAULT_PAGE_SIZE) p.set('size', String(next))
      else p.delete('size')
      p.delete('page')
      return p
    })
  }
  return { page, size, setPage, setSize }
}

/**
 * URL 筛选字段组：fields 是「字段名 → URL 参数名」映射（字段名与领域类型
 * 属性一致，参数名允许 snake_case 等历史约定）。空串一律从 URL 删除。
 */
export function useUrlFilterFields<K extends string>(fields: Record<K, string>) {
  const [searchParams, setSearchParams] = useSearchParams()
  const keys = Object.keys(fields) as K[]

  const filters = useMemo(() => {
    const out = {} as Record<K, string>
    for (const k of keys) out[k] = searchParams.get(fields[k]) ?? ''
    return out
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [searchParams])

  // 筛选写回/重置自带「回到第一页」（page 一并清除）——领域侧不要再组合
  // setPage(1)：同 tick 连发两次 setSearchParams 会基于过期 location 互相覆盖。
  const setFilters = (next: Partial<Record<K, string>>) => {
    setSearchParams((prev) => {
      const p = new URLSearchParams(prev)
      for (const k of keys) {
        const v = next[k]
        if (v) p.set(fields[k], v)
        else p.delete(fields[k])
      }
      p.delete('page')
      return p
    })
  }

  const resetFilters = () => {
    setSearchParams((prev) => {
      const p = new URLSearchParams(prev)
      for (const k of keys) p.delete(fields[k])
      p.delete('page')
      return p
    })
  }

  return { filters: filters as Record<K, string>, setFilters, resetFilters }
}
