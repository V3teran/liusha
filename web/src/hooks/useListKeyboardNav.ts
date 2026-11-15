import { useCallback } from 'react'
import type { HistoryOptions } from './useSelectedParam'

/**
 * 列表键盘导航：↑/↓ 移动行选中（首尾钳制，无选中时落第一行），Esc 清除选中。
 * rows 需按展示顺序提供且每行有 id；返回的 onListKeyDown 挂在列表容器上。
 * 逐行移动是浏览不是导航，以 replace 写入（不在历史里堆一串中间行）；Esc 关闭是一步操作。
 */
export function useListKeyboardNav<I extends string | number>(
  rows: Array<{ id: I }>,
  selectedId: I | null,
  setSelectedId: (id: I | null, opts?: HistoryOptions) => void,
) {
  const moveSelection = useCallback(
    (delta: number) => {
      if (rows.length === 0) return
      const idx = rows.findIndex((r) => r.id === selectedId)
      const next = idx < 0 ? 0 : Math.min(rows.length - 1, Math.max(0, idx + delta))
      setSelectedId(rows[next].id, { replace: true })
    },
    [rows, selectedId, setSelectedId],
  )

  const onListKeyDown = useCallback(
    (e: React.KeyboardEvent) => {
      if (e.key === 'ArrowDown') {
        e.preventDefault()
        moveSelection(1)
      } else if (e.key === 'ArrowUp') {
        e.preventDefault()
        moveSelection(-1)
      } else if (e.key === 'Escape' && selectedId != null) {
        e.preventDefault()
        setSelectedId(null)
      }
    },
    [moveSelection, selectedId, setSelectedId],
  )

  return { moveSelection, onListKeyDown }
}
