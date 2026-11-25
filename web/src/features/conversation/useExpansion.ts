import { useMemo, useState } from 'react'

/**
 * 时间线上各折叠处的展开状态。集中在时间线持有，不放在各折叠处自己的组件里：一轮从在途变成
 * 已出终稿时，它的折叠组与里面的调用卡会换位置重挂（从流式卡挪进终稿卡），组件本地的状态
 * 随之丢失——正在读的展开内容会在那一瞬折回去。
 */
export interface Expansion {
  isFoldOpen(key: string): boolean
  toggleFold(key: string): void
  isCallOpen(seq: number): boolean
  toggleCall(seq: number): void
}

function toggled<T>(set: ReadonlySet<T>, item: T): ReadonlySet<T> {
  const next = new Set(set)
  if (!next.delete(item)) next.add(item)
  return next
}

export function useExpansion(): Expansion {
  const [folds, setFolds] = useState<ReadonlySet<string>>(() => new Set())
  const [calls, setCalls] = useState<ReadonlySet<number>>(() => new Set())
  return useMemo(
    () => ({
      isFoldOpen: (key: string) => folds.has(key),
      toggleFold: (key: string) => setFolds((prev) => toggled(prev, key)),
      isCallOpen: (seq: number) => calls.has(seq),
      toggleCall: (seq: number) => setCalls((prev) => toggled(prev, seq)),
    }),
    [folds, calls],
  )
}
