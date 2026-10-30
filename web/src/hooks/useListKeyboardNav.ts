import { useCallback } from 'react'

/**
 * 列表键盘导航：↑/↓ 移动行选中（首尾钳制，无选中时落第一行），Esc 清除选中。
 * rows 需按展示顺序提供且每行有 id；返回的 onListKeyDown 挂在列表容器上。
 */
export function useListKeyboardNav<I extends string | number>(
  rows: Array<{ id: I }>,
  selectedId: I | null,
  setSelectedId: (id: I | null) => void,
) {
  const moveSelection = useCallback(
    (delta: number) => {
      if (rows.length === 0) return
      const idx = rows.findIndex((r) => r.id === selectedId)
      const next = idx < 0 ? 0 : Math.min(rows.length - 1, Math.max(0, idx + delta))
      setSelectedId(rows[next].id)
    },
    [rows, selectedId],
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
    [moveSelection, selectedId],
  )

  return { moveSelection, onListKeyDown }
}
