import { useEffect, useState } from 'react'
import { listRoles } from '@/api/client'
import type { Role } from '@/api/types'

interface RolePickerProps {
  value?: string
  onChange: (id: string) => void
  mode?: string
}

// 角色选择：挂载时拉 /roles，按 mode 过滤（主动扫描页只列 active），默认选过滤后第一个。
export function RolePicker({ value, onChange, mode }: RolePickerProps) {
  const [roles, setRoles] = useState<Role[]>([])

  useEffect(() => {
    let mounted = true
    void listRoles().then((rs) => {
      if (!mounted) return
      setRoles(rs)
      const filtered = mode ? rs.filter((r) => r.mode === mode) : rs
      if (!value && filtered[0]) onChange(filtered[0].id)
    })
    return () => {
      mounted = false
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const filtered = mode ? roles.filter((r) => r.mode === mode) : roles

  return (
    <select
      value={value ?? ''}
      onChange={(e) => onChange(e.target.value)}
      className="w-60 rounded-md border border-border bg-surface px-2 py-1 text-sm text-text outline-none focus:border-accent"
    >
      <option value="" disabled>
        选择场景角色
      </option>
      {filtered.map((r) => (
        <option key={r.id} value={r.id}>
          {r.name}
        </option>
      ))}
    </select>
  )
}
