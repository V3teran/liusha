import { useCallback, useMemo, useState } from 'react'

/**
 * 草稿编辑状态机（Agents/Skills/Providers/Settings 四处同形态的单一实现）：
 * draft + baseline 快照 → dirty/isDirty 驱动保存可用、reset 回滚、收起/切换拦截。
 *
 * dirty 判定语义：JSON 深比对（编辑对象是平坦的配置结构，字段级 diff 在各自
 * 页面按「只提交脏字段」的需要另行计算——钩子只负责布尔判定与快照管理）。
 *
 * open(initial)：打开编辑器并拍基线快照；close()：清空（调用方负责 dirty 拦截）。
 */
export function useDirtyDraft<T>(isEmpty: (d: T) => boolean = () => false) {
  const [draft, setDraft] = useState<T | null>(null)
  const [baseline, setBaseline] = useState<T | null>(null)

  const open = useCallback((initial: T) => {
    setDraft(initial)
    setBaseline(initial)
  }, [])

  const close = useCallback(() => {
    setDraft(null)
    setBaseline(null)
  }, [])

  const patch = useCallback((p: Partial<T>) => {
    setDraft((d) => (d ? { ...d, ...p } : d))
  }, [])

  const isDirty = useMemo(
    () =>
      !!draft &&
      !!baseline &&
      !isEmpty(draft) &&
      JSON.stringify(draft) !== JSON.stringify(baseline),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [draft, baseline],
  )

  /** 保存成功后的新基线（服务端权威值回填）。 */
  const commit = useCallback((fresh: T) => {
    setDraft(fresh)
    setBaseline(fresh)
  }, [])

  const reset = useCallback(() => setDraft(baseline), [baseline])

  return { draft, setDraft, patch, open, close, isDirty, commit, reset, baseline }
}
