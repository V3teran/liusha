// 「出厂默认 + 用户自定义」在前端的编辑逻辑（纯函数，便于单测）。
//
// 名单字段（工具、技能白名单）有两种模式：
//   - 全部（含以后新增）：出厂或用户勾选了 "*"。此模式下取消某项 = 「全部减此项」，
//     以后新增的项仍自动生效；
//   - 挑选：只含勾选的项。
// 提交以操作表达意图（SetChange：勾选 on / 取消 off，"*" 也是可勾选元素），后端先处理 "*"
// 再处理其余元素，因此模式切换时提交完整的勾选集，界面所见即提交所得。

import type { FieldOverride, SetChange } from '@/api/types'

export const WILDCARD = '*'

/** 名单字段的编辑态：是否「全部」模式 + 界面上勾选的项。 */
export interface SetDraft {
  all: boolean
  checked: string[]
}

function strings(list: unknown[] | undefined): string[] {
  return (list ?? []).filter((x): x is string => typeof x === 'string')
}

/**
 * 由生效名单、出厂名单与自定义记录推导编辑态。
 * 全部模式下勾选项 = 全集减去用户删掉的项（生效名单在无删除时只是 ["*"]，需按全集展开）。
 */
export function setDraftOf(
  effective: string[],
  defaults: string[],
  override: FieldOverride | undefined,
  catalog: string[],
): SetDraft {
  const added = strings(override?.added)
  const removed = strings(override?.removed)
  const all = (defaults.includes(WILDCARD) || added.includes(WILDCARD)) && !removed.includes(WILDCARD)
  if (all) {
    return { all, checked: catalog.filter((x) => !removed.includes(x)) }
  }
  return { all, checked: effective.filter((x) => x !== WILDCARD) }
}

/** 编辑态之间的差异 → 提交的操作；无差异返回 null。 */
export function setChangeOf(before: SetDraft, after: SetDraft, catalog: string[]): SetChange | null {
  const minus = (a: string[], b: string[]) => a.filter((x) => !b.includes(x))
  let change: SetChange
  if (!before.all && after.all) {
    // 进入「全部」：勾选 "*"，并恢复此前删掉的项；全部模式下没勾的项记为取消。
    change = { on: [WILDCARD, ...after.checked], off: minus(catalog, after.checked) }
  } else if (before.all && !after.all) {
    // 退出「全部」：取消 "*"，界面上仍勾着的项逐个勾选，成为显式名单。
    change = { on: [...after.checked], off: [WILDCARD] }
  } else {
    change = { on: minus(after.checked, before.checked), off: minus(before.checked, after.checked) }
  }
  return change.on.length === 0 && change.off.length === 0 ? null : change
}

/** 勾选或取消某项（不可变：返回新的编辑态）。 */
export function toggleChecked(draft: SetDraft, name: string): SetDraft {
  const checked = draft.checked.includes(name)
    ? draft.checked.filter((x) => x !== name)
    : [...draft.checked, name]
  return { ...draft, checked }
}

/** 切换「全部」模式：进入时勾满全集；退出时保留当前勾选，成为显式名单。 */
export function toggleAll(draft: SetDraft, catalog: string[]): SetDraft {
  return draft.all ? { all: false, checked: draft.checked } : { all: true, checked: [...catalog] }
}

/** 无「全部」概念的名单（如代理过滤规则）的差异 → 提交的操作；无差异返回 null。 */
export function listChangeOf<T>(before: T[], after: T[]): SetChange<T> | null {
  const on = after.filter((x) => !before.includes(x))
  const off = before.filter((x) => !after.includes(x))
  return on.length === 0 && off.length === 0 ? null : { on, off }
}

/** 已失效的新增项（引用的工具或技能已下线）。 */
export function staleOf(override: FieldOverride | undefined): string[] {
  return strings(override?.stale)
}
