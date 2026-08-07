import { useCallback, useEffect, useState } from 'react'

// useSettingSection 封装单组旋钮的加载/编辑/保存生命周期：
// 首屏拉取填充 draft，编辑走 patch（不可变合并），保存成功回填并亮「已保存」。
// 三组各持一份独立实例，互不影响（一组失败不阻塞其他）。
export function useSettingSection<T>(
  load: () => Promise<T>,
  save: (v: T) => Promise<T>,
) {
  const [draft, setDraft] = useState<T | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [saving, setSaving] = useState(false)
  const [saved, setSaved] = useState(false)

  useEffect(() => {
    let alive = true
    load()
      .then((v) => alive && setDraft(v))
      .catch((e) => alive && setError(e instanceof Error ? e.message : '加载失败'))
      .finally(() => alive && setLoading(false))
    return () => {
      alive = false
    }
  }, [load])

  // 编辑即清「已保存」提示，避免旧状态残留误导。
  const patch = useCallback((p: Partial<T>) => {
    setSaved(false)
    setDraft((d) => (d ? { ...d, ...p } : d))
  }, [])

  const onSave = useCallback(async () => {
    if (!draft) return
    setSaving(true)
    setError('')
    try {
      const fresh = await save(draft)
      setDraft(fresh)
      setSaved(true)
    } catch (e) {
      setError(e instanceof Error ? e.message : '保存失败')
    } finally {
      setSaving(false)
    }
  }, [draft, save])

  return { draft, loading, error, saving, saved, patch, onSave }
}
