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

  /**
   * 服务端只落定了部分字段（如清除密钥）：把这些字段同时写入草稿与基线，其余字段的
   * 未保存修改保持未保存。when 不满足时不动（如响应到达时已切换到别的对象）。
   */
  const commitFields = useCallback((p: Partial<T>, when: (d: T) => boolean = () => true) => {
    setDraft((d) => (d && when(d) ? { ...d, ...p } : d))
    setBaseline((b) => (b && when(b) ? { ...b, ...p } : b))
  }, [])

  /**
   * 服务端改写了已保存值（如恢复出厂）：基线换成 next，草稿按响应到达时的最新值经 merge
   * 重算——请求在途期间的编辑由 merge 决定保留，不按发起时的旧快照覆盖。
   */
  const rebase = useCallback((next: T, merge: (cur: T) => T) => {
    setBaseline(next)
    setDraft((d) => (d ? merge(d) : next))
  }, [])

  const reset = useCallback(() => setDraft(baseline), [baseline])

  return { draft, setDraft, patch, open, close, isDirty, commitFields, rebase, reset, baseline }
}
