import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { createColumnHelper } from '@tanstack/react-table'
import { DataTable } from './DataTable'

interface Row {
  id: string
  name: string
}
const col = createColumnHelper<Row>()
const columns = [col.accessor('name', { header: '名称', cell: (c) => <button type="button">复制 {c.getValue()}</button> })]

// 可选行表格走 WAI-ARIA grid 模式：表格本身是 grid，行保持原生 row 语义——
// 选中态 aria-selected 在 row 上合法，单元格内允许放交互控件（复制按钮等）。
// role="button" 行既不允许 aria-selected，也不允许嵌套可聚焦元素（axe critical/serious）。
describe('DataTable 无障碍语义', () => {
  it('可点击行：table 为具名 grid，行为 row 且携带 aria-selected，不再是 button', () => {
    render(
      <DataTable<Row>
        rows={[{ id: 'a', name: 'A' }, { id: 'b', name: 'B' }]}
        columns={columns as never}
        loading={false}
        emptyText="数据"
        selectedId="b"
        onRowClick={vi.fn()}
        ariaLabel="测试列表"
      />,
    )
    const grid = screen.getByRole('grid', { name: '测试列表' })
    const rows = Array.from(grid.querySelectorAll('tbody tr'))
    expect(rows).toHaveLength(2)
    for (const r of rows) expect(r.getAttribute('role')).toBeNull()
    expect(rows[1].getAttribute('aria-selected')).toBe('true')
    expect(rows[0].getAttribute('aria-selected')).toBe('false')
  })

  it('不可点击：普通 table，行无选中语义', () => {
    render(<DataTable<Row> rows={[{ id: 'a', name: 'A' }]} columns={columns as never} loading={false} emptyText="数据" />)
    expect(screen.queryByRole('grid')).toBeNull()
    expect(screen.getByRole('table')).toBeTruthy()
  })
})
