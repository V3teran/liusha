import { useEffect, useMemo, useState } from 'react'
import { Plus, ShieldAlert, Trash2 } from 'lucide-react'
import { CredentialTable } from '@/features/credential/CredentialTable'
import { parseCredentialRawLine } from '@/features/credential/credentialColumns'
import type { CredentialTableMeta } from '@/features/credential/credentialColumns'
import type { CredentialEntry } from '@/api/types'
import {
  useAllCredentialRows,
  type CredentialRow,
  useDeleteCredentialHost,
  useDeleteCredentialIdentity,
  usePurgeCredentialSessions,
  useSaveCredentialIdentity,
} from '@/features/credential/useCredentialQueries'
import { PageSizeSelect } from '@/components/ui/PageSizeSelect'

/**
 * 凭证库页面：全部目标的全部凭证一张密集表（行=登录身份，明文直显）。
 * 行内编辑范式（Airtable/Sheets）：点击角色或凭证单元格原地变输入框，
 * Enter/失焦提交、Esc 取消——无任何弹层。主机与用户名锁定（换=删旧录新）。
 * 录入 = 点击「录入」在表头上方展开行内新行表单。
 *
 * 生命周期：无 TTL——会话身份由任务终态清理/清扫器回收；人工通道：单条凭证删、
 * 用户名删、清会话、整目标删（host 筛选选定后出现在筛选栏右侧）。
 */

// 分页按钮：图标化前后翻页（‹ ›），与流量/漏洞模块同款。
const PAGER_BTN =
  'inline-flex h-7 w-7 items-center justify-center rounded-md border border-border text-sm text-muted transition-colors hover:border-border-strong hover:text-text disabled:pointer-events-none disabled:opacity-35'

const FILTER_SELECT =
  'h-8 rounded-lg border border-border bg-surface px-2.5 text-sm text-text outline-none transition-all focus:border-accent focus:glow-accent'
const FILTER_INPUT =
  'h-8 w-52 rounded-lg border border-border bg-surface px-2.5 text-sm text-text outline-none transition-all placeholder:text-muted focus:border-accent focus:glow-accent'
const INLINE_INPUT =
  'h-8 rounded-md border border-border bg-surface px-2 text-sm text-text outline-none transition-all placeholder:text-muted focus:border-accent focus:glow-accent'

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
  const [hostFilter, setHostFilter] = useState('')
  const [search, setSearch] = useState('')
  const [page, setPage] = useState(1)
  const [size, setSize] = useState(20)
  const [edit, setEdit] = useState<CellEdit | null>(null)
  const [creating, setCreating] = useState(false)
  const [newRow, setNewRow] = useState<NewRow>(EMPTY_NEW_ROW)

  const { rows, hosts, isLoading, isError, error } = useAllCredentialRows()
  const saveMutation = useSaveCredentialIdentity()
  const deleteIdentity = useDeleteCredentialIdentity()
  const purgeSessions = usePurgeCredentialSessions()
  const deleteHost = useDeleteCredentialHost()

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase()
    return rows.filter((r) => {
      if (hostFilter && r.host !== hostFilter) return false
      if (q) {
        const creds = r.identity.credentials.map((c) => `${c.key}=${c.value}`).join(' ')
        const hay = `${r.host} ${r.identity.username} ${r.identity.role} ${creds}`.toLowerCase()
        if (!hay.includes(q)) return false
      }
      return true
    })
  }, [rows, hostFilter, search])

  const totalPages = Math.max(1, Math.ceil(filtered.length / size))
  useEffect(() => {
    setPage(1)
  }, [hostFilter, search, size])
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
    onBeginEdit: beginEdit,
    editDraft: edit,
    onDraftChange: (value) => setEdit((e) => (e ? { ...e, value } : e)),
    onCommitEdit: (value?: string) => commitEdit(value),
    onCancelEdit: () => setEdit(null),
    onDeleteIdentity: handleDeleteIdentity,
    onDeleteCred: deleteCred,
  }

  const hasFilter = !!(hostFilter || search)

  return (
    <div className="flex h-full min-h-0 flex-col">
      <header className="flex items-center justify-between border-b border-border bg-surface px-6 py-4">
        <div>
          <h1 className="text-base font-semibold text-text">凭证库</h1>
          <p className="mt-0.5 text-sm text-text-secondary">
            按目标归属的登录态与会话材料 · 永久存储（无过期）· 点击单元格直接编辑
          </p>
        </div>
        <div className="flex items-center gap-2">
          <button
            type="button"
            onClick={() => {
              setCreating((c) => !c)
              setNewRow(EMPTY_NEW_ROW)
            }}
            className="inline-flex h-9 items-center gap-2 rounded-lg bg-accent-soft px-4 text-sm font-semibold text-accent transition-all hover:bg-accent/10 hover:text-accent-hover"
          >
            <Plus className="h-4 w-4" /> 录入
          </button>
        </div>
      </header>

      <div className="flex min-h-0 flex-1 flex-col overflow-y-auto px-5.5 py-4">
        {isError ? (
          <div className="py-16 text-center text-sm text-sev-critical">
            ⚠ {error instanceof Error ? error.message : '加载失败'}
          </div>
        ) : (
          <div className="flex flex-col gap-3">
            {/* 录入新行（展开在筛选栏与表之间；单凭证行起手，更多凭证保存后点凭证单元格追加） */}
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
                  <button
                    type="button"
                    disabled={!newRowValid(newRow) || saveMutation.isPending}
                    onClick={commitCreate}
                    className="inline-flex h-8 items-center gap-1 rounded-md bg-accent-soft px-3 text-xs font-semibold text-accent hover:bg-accent/10 hover:text-accent-hover disabled:opacity-40"
                  >
                    保存
                  </button>
                  <button
                    type="button"
                    onClick={() => setCreating(false)}
                    className="h-8 rounded-md border border-border px-3 text-xs text-muted hover:text-text"
                  >
                    取消
                  </button>
                </div>
              </div>
            )}

            {/* 筛选栏 + 分页 */}
            <div className="flex flex-wrap items-center gap-2">
              <select
                className={FILTER_SELECT}
                value={hostFilter}
                onChange={(e) => setHostFilter(e.target.value)}
                aria-label="按主机筛选"
              >
                <option value="">全部主机</option>
                {hosts.map((h) => (
                  <option key={h.host} value={h.host}>
                    {h.host}（{h.usernames + h.sessions}）
                  </option>
                ))}
              </select>
              <input
                className={FILTER_INPUT}
                value={search}
                placeholder="搜索主机 / 用户名 / 键 / 值…"
                onChange={(e) => setSearch(e.target.value)}
              />
              {hostFilter && (
                <>
                  <button
                    type="button"
                    onClick={() => {
                      if (confirm(`清理 ${hostFilter} 的全部会话快照（录入凭证不动）？`)) {
                        purgeSessions.mutate(hostFilter)
                      }
                    }}
                    className="inline-flex h-8 items-center gap-1 rounded-lg border border-border px-2.5 text-xs text-muted transition-colors hover:text-text"
                  >
                    <ShieldAlert className="h-3.5 w-3.5" /> 清理会话
                  </button>
                  <button
                    type="button"
                    onClick={() => {
                      if (confirm(`删除 ${hostFilter} 的全部凭证？不可恢复。`)) {
                        deleteHost.mutate(hostFilter)
                        setHostFilter('')
                      }
                    }}
                    className="inline-flex h-8 items-center gap-1 rounded-lg border border-border px-2.5 text-xs text-muted transition-colors hover:text-status-error"
                  >
                    <Trash2 className="h-3.5 w-3.5" /> 删除主机
                  </button>
                </>
              )}
              <span className="ml-auto flex items-center gap-2 text-xs text-muted tabular-nums">
                共 {filtered.length} 条
                <PageSizeSelect value={size} onChange={setSize} />
                <button
                  type="button"
                  disabled={page <= 1}
                  onClick={() => setPage(page - 1)}
                  aria-label="上一页"
                  className={PAGER_BTN}
                >
                  <span aria-hidden>‹</span>
                </button>
                <span className="min-w-[54px] text-center">
                  <span className="font-semibold text-text">{page}</span> / {totalPages}
                </span>
                <button
                  type="button"
                  disabled={page >= totalPages}
                  onClick={() => setPage(page + 1)}
                  aria-label="下一页"
                  className={PAGER_BTN}
                >
                  <span aria-hidden>›</span>
                </button>
              </span>
            </div>

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
