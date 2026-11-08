import { SearchInput } from '@/components/SearchInput'
import { PageHeader } from '@/components/PageHeader'
import { useEffect, useMemo, useState } from 'react'
import { Plus } from 'lucide-react'
import { CredentialTable } from '@/features/credential/CredentialTable'
import { parseCredentialRawLine } from '@/features/credential/credentialColumns'
import type { CredentialTableMeta } from '@/features/credential/credentialColumns'
import type { CredentialEntry } from '@/api/types'
import {
  useAllCredentialRows,
  type CredentialRow,
  useDeleteCredentialIdentity,
  useSaveCredentialIdentity,
} from '@/features/credential/useCredentialQueries'
import { PaginationToolbar } from '@/components/ui/PaginationToolbar'
import { Button } from '@/components/ui/Button'
import { controlClass } from '@/components/ui/control'
import { FilterBar } from '@/components/FilterBar'

/**
 * 凭证库页面：全部目标的全部凭证一张密集表（行=登录身份，明文直显）。
 * 行内编辑范式（Airtable/Sheets）：点击角色或凭证单元格原地变输入框，
 * Enter/失焦提交、Esc 取消——无任何弹层。主机与用户名锁定（换=删旧录新）。
 * 录入 = 点击「录入」在表头上方展开行内新行表单。
 *
 * 生命周期：无 TTL——会话身份由任务终态清理/清扫器回收；人工通道：单条凭证删、
 * 用户名删、清会话、整目标删（host 筛选选定后出现在筛选栏右侧）。
 */

const INLINE_INPUT = controlClass('sm')

/** 行内编辑草稿（单元格级：一个单元格一个草稿，提交即 PUT 整个身份）。 */
interface CellEdit {
  rowId: string
  field: 'role' | 'cred' | 'cred-add'
  credIndex?: number
  value: string
}

/** 录入新行草稿（表头上方的展开表单）。 */
interface NewRow {
  host: string
  username: string
  role: string
  raw: string
}

const EMPTY_NEW_ROW: NewRow = { host: '', username: '', role: '', raw: '' }

function newRowValid(n: NewRow): boolean {
  return (
    n.host.trim() !== '' &&
    n.username.trim() !== '' &&
    !n.username.trim().startsWith('task:') &&
    parseCredentialRawLine(n.raw) !== null
  )
}

export default function CredentialsPage() {
  const [search, setSearch] = useState('')
  const [page, setPage] = useState(1)
  const [size, setSize] = useState(20)
  const [edit, setEdit] = useState<CellEdit | null>(null)
  const [creating, setCreating] = useState(false)
  const [newRow, setNewRow] = useState<NewRow>(EMPTY_NEW_ROW)

  const { rows, isLoading, isError, error } = useAllCredentialRows()
  const saveMutation = useSaveCredentialIdentity()
  const deleteIdentity = useDeleteCredentialIdentity()

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase()
    return rows.filter((r) => {
      if (q) {
        const creds = r.identity.credentials.map((c) => `${c.key}=${c.value}`).join(' ')
        const hay = `${r.host} ${r.identity.username} ${r.identity.role} ${creds}`.toLowerCase()
        if (!hay.includes(q)) return false
      }
      return true
    })
  }, [rows, search])

  const totalPages = Math.max(1, Math.ceil(filtered.length / size))
  useEffect(() => {
    setPage(1)
  }, [search, size])
  useEffect(() => {
    if (page > totalPages) setPage(totalPages)
  }, [page, totalPages])

  const pageRows = useMemo(
    () => filtered.slice((page - 1) * size, page * size),
    [filtered, page, size],
  )

  // ─── 行内编辑：以 rowId 反查行 → 改字段 → PUT 整个身份（后端按用户名 upsert）───
  const findRow = (rowId: string) => rows.find((r) => r.rowId === rowId) ?? null

  const saveIdentity = (host: string, username: string, role: string, credentials: CredentialEntry[]) => {
    saveMutation.mutate({ host, username, role, credentials })
  }

  const beginEdit = (rowId: string, field: 'role' | 'cred' | 'cred-add', credIndex?: number) => {
    const row = findRow(rowId)
    if (!row) return
    const id = row.identity
    if (field === 'role') {
      setEdit({ rowId, field, value: id.role })
    } else if (field === 'cred' && credIndex !== undefined) {
      const c = id.credentials[credIndex]
      if (!c) return
      setEdit({ rowId, field, credIndex, value: c.type === 'headers' ? `${c.key}: ${c.value}` : `[${c.type}] ${c.key}: ${c.value}` })
    } else {
      setEdit({ rowId, field: 'cred-add', value: '' })
    }
  }

  const commitEdit = (raw?: string) => {
    if (!edit) return
    const value = (raw ?? edit.value).trim()
    const row = findRow(edit.rowId)
    setEdit(null)
    if (!row) return
    const id = row.identity
    if (edit.field === 'role') {
      if (value === id.role) return
      saveIdentity(row.host, id.username, value, id.credentials)
      return
    }
    const parsed = parseCredentialRawLine(value)
    if (!parsed) return // 无法解析的原始行：视作取消（已有值未受损）
    if (edit.field === 'cred' && edit.credIndex !== undefined) {
      const creds = id.credentials.map((c, i) => (i === edit.credIndex ? parsed : { ...c }))
      saveIdentity(row.host, id.username, id.role, creds)
    } else {
      saveIdentity(row.host, id.username, id.role, [...id.credentials.map((c) => ({ ...c })), parsed])
    }
  }

  const deleteCred = (row: CredentialRow, credIndex: number) => {
    const id = row.identity
    if (id.credentials.length <= 1) {
      // 最后一条：等同删除该用户名（确认后走单删，语义一致）
      handleDeleteIdentity(row.host, id.username)
      return
    }
    saveIdentity(row.host, id.username, id.role, id.credentials.filter((_, i) => i !== credIndex).map((c) => ({ ...c })))
  }

  const handleDeleteIdentity = (host: string, username: string) => {
    if (!confirm(`删除用户名 ${username} 的全部凭证？不可恢复。`)) return
    deleteIdentity.mutate({ host, username })
  }

  const commitCreate = () => {
    if (!newRowValid(newRow)) return
    const parsed = parseCredentialRawLine(newRow.raw)!
    saveMutation.mutate(
      {
        host: newRow.host.trim(),
        username: newRow.username.trim(),
        role: newRow.role.trim(),
        credentials: [parsed],
      },
      {
        onSuccess: () => {
          setCreating(false)
          setNewRow(EMPTY_NEW_ROW)
        },
      },
    )
  }

  const meta: CredentialTableMeta = {
    startIndex: (page - 1) * size,
    onBeginEdit: beginEdit,
    editDraft: edit,
    onDraftChange: (value) => setEdit((e) => (e ? { ...e, value } : e)),
    onCommitEdit: (value?: string) => commitEdit(value),
    onCancelEdit: () => setEdit(null),
    onDeleteIdentity: handleDeleteIdentity,
    onDeleteCred: deleteCred,
  }

  const hasFilter = !!search

  return (
    <div className="flex h-full min-h-0 flex-col">
      <PageHeader title="凭证库" subtitle="按目标归属的登录态与会话材料">
        {/* 主创建动作与全站新建同位（头部右上）；筛选行只承担搜索/分页 */}
        <Button
          variant={creating ? 'secondary' : 'primary'}
          onClick={() => {
            setCreating((c) => !c)
            setNewRow(EMPTY_NEW_ROW)
          }}
        >
          <Plus strokeWidth={2.4} />
          {creating ? '取消录入' : '录入'}
        </Button>
      </PageHeader>

      <FilterBar
        label="凭证筛选"
        canReset={hasFilter}
        onReset={() => setSearch('')}
        trailing={
          <PaginationToolbar
            page={page}
            pageSize={size}
            total={filtered.length}
            totalPages={totalPages}
            onPageChange={setPage}
            onPageSizeChange={setSize}
          />
        }
      >
        <SearchInput
          className="w-64"
          value={search}
          onChange={setSearch}
          placeholder="搜索主机 / 用户名 / 键 / 值…"
          ariaLabel="搜索凭证"
        />
      </FilterBar>

      <div className="flex min-h-0 flex-1 flex-col overflow-y-auto px-6 py-4">
        {isError ? (
          <div className="py-16 text-center text-sm text-sev-critical">
            ⚠ {error instanceof Error ? error.message : '加载失败'}
          </div>
        ) : (
          <div className="flex flex-col gap-3">
            {/* 录入新行（展开在表格上方；单凭证行起手，更多凭证保存后点凭证单元格追加） */}
            {creating && (
              <div className="grid grid-cols-[minmax(120px,1.2fr)_minmax(90px,0.8fr)_minmax(70px,0.6fr)_minmax(240px,3fr)_auto] items-center gap-2 rounded-lg border border-dashed border-border px-3 py-2">
                <input
                  autoFocus
                  className={INLINE_INPUT + ' font-mono'}
                  placeholder="目标 host:port"
                  value={newRow.host}
                  onChange={(e) => setNewRow({ ...newRow, host: e.target.value })}
                />
                <input
                  className={INLINE_INPUT + ' font-mono'}
                  placeholder="用户名"
                  value={newRow.username}
                  onChange={(e) => setNewRow({ ...newRow, username: e.target.value })}
                />
                <input
                  className={INLINE_INPUT}
                  placeholder="角色"
                  value={newRow.role}
                  onChange={(e) => setNewRow({ ...newRow, role: e.target.value })}
                />
                <input
                  className={INLINE_INPUT + ' font-mono'}
                  placeholder='Cookie: PHPSESSID=…（或 [query] token: xxx）'
                  value={newRow.raw}
                  onKeyDown={(e) => e.key === 'Enter' && commitCreate()}
                  onChange={(e) => setNewRow({ ...newRow, raw: e.target.value })}
                />
                <div className="flex items-center gap-1">
                  <Button size="sm" disabled={!newRowValid(newRow) || saveMutation.isPending} onClick={commitCreate}>
                    保存
                  </Button>
                  <Button size="sm" variant="secondary" onClick={() => setCreating(false)}>
                    取消
                  </Button>
                </div>
              </div>
            )}

            <CredentialTable
              rows={pageRows}
              loading={isLoading}
              hasFilter={hasFilter}
              selectedRowId={null}
              meta={meta}
              onRowClick={() => {}}
            />
          </div>
        )}
      </div>
    </div>
  )
}
