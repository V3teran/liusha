import { useCallback, useEffect, useState } from 'react'
import { getSettings, patchSettings, resetSettings, type SettingsPatch } from '@/api/settings'
import type { SettingsByGroup, SettingsDetail, SettingsGroup } from '@/api/types'
import { listChangeOf } from '@/lib/override'

/** 草稿相对已保存值的改动 → PATCH 载荷：数值为新值，名单为 {on, off}。 */
export function settingsPatchOf<T extends object>(saved: T, draft: T): SettingsPatch<T> {
  const p: Record<string, unknown> = {}
  for (const key of Object.keys(draft) as Array<keyof T>) {
    const before = saved[key]
    const after = draft[key]
    if (Array.isArray(before) && Array.isArray(after)) {
      const change = listChangeOf<unknown>(before, after)
      if (change) p[key as string] = change
    } else if (before !== after) {
      p[key as string] = after
    }
  }
  return p as SettingsPatch<T>
}

/** 一组设置的编辑句柄。 */
export interface SettingSection<T> {
  detail: SettingsDetail<T> | null
  draft: T | null
  loading: boolean
  error: string
  saving: boolean
  saved: boolean
  isDirty: boolean
  resetting: string | null // 正在恢复的字段名，'all' = 整组
  patch: (p: Partial<T>) => void
  onSave: () => Promise<void>
  discard: () => void
  resetField: (field?: keyof T & string) => Promise<void>
}

// useSettingSection 封装一组设置的加载 / 编辑 / 保存 / 恢复出厂生命周期。
// 三组各持一份独立实例，互不影响（一组失败不阻塞其他）。
//   - discard：放弃未保存的编辑，回到已保存值；
//   - resetField：把某字段（不传则整组）恢复为出厂默认（即时的服务端操作，其它字段上
//     未保存的编辑保留）。
export function useSettingSection<G extends SettingsGroup>(
  group: G,
  onSaved?: () => void,
): SettingSection<SettingsByGroup[G]> {
  type T = SettingsByGroup[G]
  const [detail, setDetail] = useState<SettingsDetail<T> | null>(null)
  const [draft, setDraft] = useState<T | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [saving, setSaving] = useState(false)
  const [saved, setSaved] = useState(false)
  const [resetting, setResetting] = useState<string | null>(null)

  useEffect(() => {
    let alive = true
    getSettings(group)
      .then((d) => {
        if (!alive) return
        setDetail(d)
        setDraft(d.values)
      })
      .catch((e: unknown) => alive && setError(e instanceof Error ? e.message : '加载失败'))
      .finally(() => alive && setLoading(false))
    return () => {
      alive = false
    }
  }, [group])

  const patch = useCallback((p: Partial<T>) => {
    setSaved(false)
    setDraft((d) => (d ? { ...d, ...p } : d))
  }, [])

  const isDirty = !!draft && !!detail && JSON.stringify(draft) !== JSON.stringify(detail.values)

  const onSave = useCallback(async () => {
    if (!draft || !detail) return
    const changes = settingsPatchOf(detail.values, draft)
    if (Object.keys(changes).length === 0) return
    setSaving(true)
    setError('')
    try {
      const fresh = await patchSettings(group, changes)
      setDetail(fresh)
      setDraft(fresh.values)
      setSaved(true)
      onSaved?.()
    } catch (e) {
      setError(e instanceof Error ? e.message : '保存失败')
    } finally {
      setSaving(false)
    }
  }, [draft, detail, group, onSaved])

  const discard = useCallback(() => {
    setSaved(false)
    setDraft(detail ? detail.values : null)
  }, [detail])

  const resetField = useCallback(
    async (field?: keyof T & string) => {
      if (!detail || !draft) return
      setResetting(field ?? 'all')
      setError('')
      try {
        const fresh = await resetSettings(group, field)
        // 以新的已保存值为底，保留其它字段上未保存的编辑。
        const merged = { ...fresh.values }
        if (field) {
          for (const k of Object.keys(draft) as Array<keyof T>) {
            if (k !== field && JSON.stringify(draft[k]) !== JSON.stringify(detail.values[k])) merged[k] = draft[k]
          }
        }
        setDetail(fresh)
        setDraft(merged)
        onSaved?.()
      } catch (e) {
        setError(e instanceof Error ? e.message : '恢复失败')
      } finally {
        setResetting(null)
      }
    },
    [detail, draft, group, onSaved],
  )

  return { detail, draft, loading, error, saving, saved, isDirty, resetting, patch, onSave, discard, resetField }
}
