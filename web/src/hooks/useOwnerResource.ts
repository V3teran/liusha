import { useCallback, useEffect, useRef, useState } from 'react'

export interface OwnerResource<T> {
  owner: string
  setOwner: (id: string) => void
  data: T | null
  loading: boolean
  error: string
  /** 后端 404：该 owner 非 active 模式 / 不存在，无此维度数据（passive owner 常态，非错误）。 */
  notActive: boolean
  reload: () => Promise<void>
}

/**
 * owner 维度只读资源的统一加载：选中的 owner 变化即重新拉取，集中管理 loading/error 状态。
 * fetchFn 抛 404（如 passive owner 无 sitemap）映射为 notActive，不当作错误展示。
 */
export function useOwnerResource<T>(fetchFn: (ownerID: string) => Promise<T>): OwnerResource<T> {
  const [owner, setOwner] = useState('')
  const [data, setData] = useState<T | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [notActive, setNotActive] = useState(false)
  const fetchFnRef = useRef(fetchFn)
  fetchFnRef.current = fetchFn

  const reload = useCallback(async () => {
    if (!owner) return
    setLoading(true)
    setError('')
    setNotActive(false)
    setData(null)
    try {
      setData(await fetchFnRef.current(owner))
    } catch (e) {
      const msg = e instanceof Error ? e.message : '加载失败'
      if (msg.includes('404')) setNotActive(true)
      else setError(msg)
    } finally {
      setLoading(false)
    }
  }, [owner])

  useEffect(() => {
    void reload()
  }, [reload])

  return { owner, setOwner, data, loading, error, notActive, reload }
}
