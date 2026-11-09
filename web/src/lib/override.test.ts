import { describe, expect, it } from 'vitest'
import type { FieldOverride } from '@/api/types'
import { listChangeOf, setChangeOf, setDraftOf, toggleAll, toggleChecked } from './override'

const catalog = ['nmap', 'nuclei', 'sqlmap']

function ov(added: string[], removed: string[]): FieldOverride {
  return { kind: 'set', default: [], baseline: null, default_changed: false, matches_default: false, added, removed }
}

describe('setDraftOf', () => {
  it('出厂为全部且无删除：全部模式，勾满全集', () => {
    expect(setDraftOf(['*'], ['*'], undefined, catalog)).toEqual({ all: true, checked: catalog })
  })
  it('全部模式下删过某项：仍是全部模式，勾选为全集减删除', () => {
    const d = setDraftOf(['nmap', 'nuclei'], ['*'], ov([], ['sqlmap']), catalog)
    expect(d).toEqual({ all: true, checked: ['nmap', 'nuclei'] })
  })
  it('取消过 "*"：挑选模式，勾选即生效名单', () => {
    expect(setDraftOf(['nmap'], ['*'], ov(['nmap'], ['*']), catalog)).toEqual({ all: false, checked: ['nmap'] })
  })
})

describe('setChangeOf', () => {
  it('全部模式下取消一项 → 只提交 off 该项（以后新增的仍自动生效）', () => {
    const before = { all: true, checked: catalog }
    const after = toggleChecked(before, 'sqlmap')
    expect(setChangeOf(before, after, catalog)).toEqual({ on: [], off: ['sqlmap'] })
  })
  it('退出全部 → 先取消 "*"，再显式勾选界面上仍勾着的项', () => {
    const before = { all: true, checked: catalog }
    const after = toggleChecked(toggleAll(before, catalog), 'nuclei')
    expect(setChangeOf(before, after, catalog)).toEqual({ on: ['nmap', 'sqlmap'], off: ['*'] })
  })
  it('进入全部 → 勾选 "*" 并恢复全部项', () => {
    const before = { all: false, checked: ['nmap'] }
    const after = toggleAll(before, catalog)
    expect(setChangeOf(before, after, catalog)).toEqual({ on: ['*', ...catalog], off: [] })
  })
  it('挑选模式的增删与无变化', () => {
    const before = { all: false, checked: ['nmap'] }
    const after = toggleChecked(toggleChecked(before, 'nmap'), 'sqlmap')
    expect(setChangeOf(before, after, catalog)).toEqual({ on: ['sqlmap'], off: ['nmap'] })
    expect(setChangeOf(before, before, catalog)).toBeNull()
  })
})

describe('listChangeOf', () => {
  it('名单前后差异 → on/off；相同 → null', () => {
    expect(listChangeOf([404, 500], [500, 302])).toEqual({ on: [302], off: [404] })
    expect(listChangeOf(['a'], ['a'])).toBeNull()
  })
})
