import { describe, expect, it } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import { createColumnHelper } from '@tanstack/react-table'
import { DataTable } from './DataTable'

interface Row {
  id: string
  name: string
  count: number
}
const col = createColumnHelper<Row>()
// 单元格里不写任何对齐类：对齐只来自列声明
const columns = [
  col.accessor('name', { header: '名称', cell: (c) => <span className="block">{c.getValue()}</span> }),
  col.accessor('count', {
    header: '数量',
    meta: { align: 'right' },
    cell: (c) => <span className="block">{c.getValue()}</span>,
  }),
]

const side = (el: Element) => (/\btext-right\b/.test(el.className) ? 'right' : 'left')

// 全站列表的对齐只有一个规则、一个声明点：列定义用 meta.align 声明靠右，表头与单元格由表格外壳
// 一同对齐——文本、时间、徽章列靠左，数值列靠右。此前对齐写在各列的单元格里、表头另靠声明，
// 两处各写一遍就会错位（「表头靠左、值靠右」，或同一个 Tokens 列在两张表里两种对齐）。
describe('列表的表头与内容同侧', () => {
  it('DataTable：靠右由声明决定，表头与单元格一起靠右；没声明的都靠左', () => {
    render(
      <DataTable<Row>
        rows={[{ id: 'a', name: '甲', count: 3 }]}
        columns={columns}
        loading={false}
        emptyNoun="数据"
        emptyHint=""
      />,
    )
    const table = screen.getByRole('table')
    expect(within(table).getAllByRole('columnheader').map(side)).toEqual(['left', 'right'])
    expect(within(table).getAllByRole('cell').map(side)).toEqual(['left', 'right'])
  })
})

const sources = import.meta.glob('/src/**/*.tsx', { query: '?raw', import: 'default', eager: true }) as Record<
  string,
  string
>
const files = Object.entries(sources).filter(([path]) => !path.endsWith('.test.tsx'))
const COLUMN_FILES = '/src/features/'

describe('对齐只写在列声明里', () => {
  it('列定义里不写文字对齐类（text-right / text-left / text-center），由 meta.align 声明', () => {
    const bad = files
      .filter(([path]) => /\/[a-zA-Z]+Columns\.tsx$/.test(path) && path.startsWith(COLUMN_FILES))
      .flatMap(([path, src]) =>
        [...src.matchAll(/\btext-(right|left|center)\b/g)].map((m) => `${path}: ${m[0]}`),
      )
    expect(bad).toEqual([])
  })

  it('表头按声明靠右的表格外壳，单元格也按同一份声明靠右（两处同改，不会只剩一处）', () => {
    // 声明式对齐在表头与单元格各出现一次；只出现在表头 = 值没跟上
    const shells = files.filter(([, src]) => src.includes("meta?.align === 'right' && 'text-right'"))
    expect(shells.map(([path]) => path.replace('/src/', '')).sort()).toEqual([
      'components/DataTable.tsx',
      'features/credential/CredentialTable.tsx',
    ])
    for (const [path, src] of shells) {
      expect(src.split("meta?.align === 'right' && 'text-right'").length - 1, path).toBe(2)
    }
  })
})
