import { render, screen, fireEvent } from '@testing-library/react'
import { describe, it, expect, vi } from 'vitest'
import { ConfigListShell, ConfigRow, type ServerPaging } from './ConfigListShell'

function cards(n: number) {
  return Array.from({ length: n }, (_, i) => <ConfigRow key={i} name={`item-${i}`} onClick={() => {}} />)
}

function paging(over: Partial<ServerPaging> = {}): ServerPaging {
  return { page: 1, totalPages: 2, count: 13, onPage: vi.fn(), size: 12, onSize: vi.fn(), ...over }
}

function renderShell(props: { n?: number; server?: ServerPaging; loading?: boolean; error?: string } = {}) {
  const n = props.n ?? 3
  return render(
    <ConfigListShell
      title="智能体"
      subtitle="sub"
      loading={props.loading ?? false}
      error={props.error ?? ''}
      empty={n === 0}
      emptyHint="暂无配置"
      onNew={vi.fn()}
      search={{ value: '', onChange: vi.fn(), placeholder: '搜索名称' }}
      server={props.server ?? paging()}
    >
      {cards(n)}
    </ConfigListShell>,
  )
}

describe('ConfigListShell', () => {
  it('渲染服务端给出的全部卡片（不在前端二次切页）', () => {
    renderShell({ n: 13 })
    expect(screen.getByText('item-0')).toBeInTheDocument()
    expect(screen.getByText('item-12')).toBeInTheDocument()
  })

  it('筛选栏右端显示统一分页器，翻页回调服务端分页', () => {
    const server = paging()
    renderShell({ server })
    expect(screen.getByRole('navigation', { name: '分页' })).toHaveTextContent('共 13 条')
    fireEvent.click(screen.getByRole('button', { name: '下一页' }))
    expect(server.onPage).toHaveBeenCalledWith(2)
  })

  it('搜索框位于筛选地标内', () => {
    renderShell()
    expect(screen.getByRole('search')).toContainElement(screen.getByPlaceholderText('搜索名称'))
  })

  it('空态显示提示、不渲染卡片网格', () => {
    renderShell({ n: 0, server: paging({ count: 0, totalPages: 1 }) })
    expect(screen.getByText('暂无配置')).toBeInTheDocument()
    expect(screen.queryByText('item-0')).toBeNull()
  })

  it('错误态显示错误信息（role=alert）', () => {
    renderShell({ error: '加载失败：网络错误' })
    expect(screen.getByRole('alert')).toHaveTextContent('加载失败：网络错误')
  })

  it('加载态以 status 地标宣告', () => {
    renderShell({ loading: true })
    expect(screen.getByRole('status')).toHaveTextContent('加载中')
  })

  it('页头「新建」按钮可用', () => {
    renderShell()
    expect(screen.getByRole('button', { name: '新建' })).toBeEnabled()
  })
})
