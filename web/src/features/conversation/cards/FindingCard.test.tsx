import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { FindingCard } from './FindingCard'

describe('FindingCard', () => {
  it('渲染分级徽章 + 标题 + 目标（severity 单一真相源配色）', () => {
    render(<FindingCard summary="反射型 XSS：search 参数未过滤" severity="high" host="example.com" target="https://example.com/search" />)
    expect(screen.getByText('HIGH')).toBeTruthy()
    expect(screen.getByText('反射型 XSS：search 参数未过滤')).toBeTruthy()
    expect(screen.getByText(/example.com/)).toBeTruthy()
  })

  it('severity 缺省按 info 兜底，标题缺省占位', () => {
    render(<FindingCard summary="" severity="" />)
    expect(screen.getByText('INFO')).toBeTruthy()
    expect(screen.getByText('(无标题)')).toBeTruthy()
  })
})
