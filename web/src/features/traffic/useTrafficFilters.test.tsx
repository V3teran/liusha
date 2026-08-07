import { describe, expect, it } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import { MemoryRouter, useLocation } from 'react-router-dom'
import type { ReactNode } from 'react'
import { useTrafficFilters } from './useTrafficFilters'

// 用 MemoryRouter 提供 useSearchParams 上下文，并挂一个 useLocation 探针把当前 URL search 暴露出来，
// 断言筛选/翻页确实回写进 URL（web/patterns.md「筛选/分页应作为 URL state」）。
function renderFilters(initialEntry = '/traffic') {
  const wrapper = ({ children }: { children: ReactNode }) => (
    <MemoryRouter initialEntries={[initialEntry]}>{children}</MemoryRouter>
  )
  return renderHook(
    () => {
      const state = useTrafficFilters()
      const search = useLocation().search
      return { ...state, search }
    },
    { wrapper },
  )
}

describe('useTrafficFilters', () => {
  it('无 URL 参数时给出空筛选与第 1 页', () => {
    const { result } = renderFilters()
    expect(result.current.filters).toEqual({ host: '', method: '', path: '', statusMin: 0, statusMax: 0 })
    expect(result.current.page).toBe(1)
  })

  it('从 URL search params 还原筛选与页码', () => {
    const { result } = renderFilters('/traffic?host=api.example.com&method=POST&path=/v1/*&status_min=400&status_max=499&page=3')
    expect(result.current.filters).toEqual({
      host: 'api.example.com',
      method: 'POST',
      path: '/v1/*',
      statusMin: 400,
      statusMax: 499,
    })
    expect(result.current.page).toBe(3)
  })

  it('setFilters 回写 URL，并清掉非空字段、丢弃 page', () => {
    const { result } = renderFilters('/traffic?page=5')
    act(() => result.current.setFilters({ host: 'h1', method: 'GET', path: '', statusMin: 500, statusMax: 0 }))

    const params = new URLSearchParams(result.current.search)
    expect(params.get('host')).toBe('h1')
    expect(params.get('method')).toBe('GET')
    expect(params.get('status_min')).toBe('500')
    // 空值字段不落 URL
    expect(params.has('path')).toBe(false)
    expect(params.has('status_max')).toBe(false)
    // 筛选变化回到第一页：page 被清除
    expect(params.has('page')).toBe(false)
    expect(result.current.page).toBe(1)
  })

  it('setPage>1 写 page 参数，setPage(1) 清除它', () => {
    const { result } = renderFilters()
    act(() => result.current.setPage(4))
    expect(new URLSearchParams(result.current.search).get('page')).toBe('4')
    expect(result.current.page).toBe(4)

    act(() => result.current.setPage(1))
    expect(new URLSearchParams(result.current.search).has('page')).toBe(false)
    expect(result.current.page).toBe(1)
  })

  it('resetFilters 清空所有筛选参数', () => {
    const { result } = renderFilters('/traffic?host=h1&method=POST&status_min=400&page=2')
    act(() => result.current.resetFilters())

    const params = new URLSearchParams(result.current.search)
    expect(params.has('host')).toBe(false)
    expect(params.has('method')).toBe(false)
    expect(params.has('status_min')).toBe(false)
    expect(params.has('page')).toBe(false)
    expect(result.current.filters).toEqual({ host: '', method: '', path: '', statusMin: 0, statusMax: 0 })
  })
})
