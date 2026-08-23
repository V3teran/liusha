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
  const EMPTY = { method: '', contentType: '', statusClass: '', search: '', since: '', until: '' }

  it('无 URL 参数时给出空筛选与第 1 页', () => {
    const { result } = renderFilters()
    expect(result.current.filters).toEqual(EMPTY)
    expect(result.current.page).toBe(1)
  })

  it('从 URL search params 还原筛选与页码', () => {
    const { result } = renderFilters(
      '/traffic?method=POST&content_type=application/json&status=4&search=api.example.com*&since=2026-08-01T00:00:00.000Z&until=2026-08-07T00:00:00.000Z&page=3',
    )
    expect(result.current.filters).toEqual({
      method: 'POST',
      contentType: 'application/json',
      statusClass: '4',
      search: 'api.example.com*',
      since: '2026-08-01T00:00:00.000Z',
      until: '2026-08-07T00:00:00.000Z',
    })
    expect(result.current.page).toBe(3)
  })

  it('setFilters 回写 URL，并清掉非空字段、丢弃 page', () => {
    const { result } = renderFilters('/traffic?page=5')
    act(() => result.current.setFilters({ ...EMPTY, method: 'GET', contentType: 'text/html' }))

    const params = new URLSearchParams(result.current.search)
    expect(params.get('method')).toBe('GET')
    expect(params.get('content_type')).toBe('text/html')
    // 空值字段不落 URL
    expect(params.has('search')).toBe(false)
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
    const { result } = renderFilters('/traffic?method=POST&content_type=text/html&status=5&page=2')
    act(() => result.current.resetFilters())

    const params = new URLSearchParams(result.current.search)
    expect(params.has('method')).toBe(false)
    expect(params.has('content_type')).toBe(false)
    expect(params.has('status')).toBe(false)
    expect(params.has('page')).toBe(false)
    expect(result.current.filters).toEqual(EMPTY)
  })

  it('无 URL 参数时默认每页 50 条', () => {
    const { result } = renderFilters()
    expect(result.current.size).toBe(50)
  })

  it('从 URL 还原合法 size；非法值回退默认', () => {
    expect(renderFilters('/traffic?size=100').result.current.size).toBe(100)
    expect(renderFilters('/traffic?size=999').result.current.size).toBe(50)
  })

  it('setSize 写 size 参数并回到第一页；设回默认值时清除该参数', () => {
    const { result } = renderFilters('/traffic?page=3')
    act(() => result.current.setSize(100))

    let params = new URLSearchParams(result.current.search)
    expect(params.get('size')).toBe('100')
    expect(params.has('page')).toBe(false)
    expect(result.current.size).toBe(100)

    act(() => result.current.setSize(50))
    params = new URLSearchParams(result.current.search)
    expect(params.has('size')).toBe(false)
    expect(result.current.size).toBe(50)
  })
})
