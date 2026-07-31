import { useCallback, useEffect, useRef, useState } from 'react'

const RESET_DELAY_MS = 1600

/**
 * 「点击复制 → 按钮短暂变 ✓ → 自动恢复」的通用状态机。
 * key 支持同一处存在多个复制按钮（如详情抽屉的多个 section），只有触发的那个 key 会短暂标记为已复制。
 * 卸载时清理挂起的 timeout，避免组件卸载后仍尝试 setState。
 */
export function useCopyToClipboard() {
  const [copiedKey, setCopiedKey] = useState('')
  const timerRef = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)

  useEffect(() => () => clearTimeout(timerRef.current), [])

  const copy = useCallback(async (key: string, text: string) => {
    try {
      await navigator.clipboard.writeText(text)
      setCopiedKey(key)
      clearTimeout(timerRef.current)
      timerRef.current = setTimeout(() => setCopiedKey((k) => (k === key ? '' : k)), RESET_DELAY_MS)
    } catch {
      // 剪贴板不可用（非 https / 无权限）：静默，不打断查看
    }
  }, [])

  return { copiedKey, copy }
}
