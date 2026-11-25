import { SearchInput } from '@/components/SearchInput'
import { PageHeader } from '@/components/PageHeader'
import { useEffect, useState } from 'react'
import { Plus } from 'lucide-react'
import { CredentialTable } from '@/features/credential/CredentialTable'
import { parseCredentialRawLine } from '@/features/credential/credentialColumns'
import type { CredentialTableMeta } from '@/features/credential/credentialColumns'
import type { CredentialEntry } from '@/api/types'
import {
  useCredentialList,
  type CredentialRow,
  useDeleteCredentialIdentity,
  useSaveCredentialIdentity,
} from '@/features/credential/useCredentialQueries'
import { PaginationToolbar } from '@/components/ui/PaginationToolbar'
import { Button } from '@/components/ui/Button'
import { controlClass } from '@/components/ui/control'
import { FilterBar } from '@/components/FilterBar'
import { ErrorState } from '@/components/ui/ErrorState'
import { confirmAction } from '@/components/ui/confirm'
import { useUrlFilterFields, useUrlPageState } from '@/hooks/useUrlPageState'
import { useSearchDraft } from '@/hooks/useDebouncedSearch'

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

export function CredentialsPage() {
  // 搜索词与分页进 URL（刷新、后退、分享都保持）；检索、排序、分页都在服务端。
  const { page, size, setPage, setSize } = useUrlPageState()
  const { filters, setFilters, resetFilters } = useUrlFilterFields({ search: 'q' })
  // 关键词停顿后写入 URL（打字不是导航：改写当前历史）；交给 URL 的词去掉首尾空白
  const search = useSearchDraft(filters.search, (next) => setFilters({ search: next }, { replace: true }), (d) => d.trim())
  const [edit, setEdit] = useState<CellEdit | null>(null)
  const [creating, setCreating] = useState(false)
  const [newRow, setNewRow] = useState<NewRow>(EMPTY_NEW_ROW)

  const { query, rows, total } = useCredentialList(filters.search, page, size)
  const saveMutation = useSaveCredentialIdentity()
  const deleteIdentity = useDeleteCredentialIdentity()

  const totalPages = Math.max(1, Math.ceil(total / size))
  // 页码越界（删掉末页最后一条、链接里的页码过大）：退回最后一页，系统校正不新增历史
  useEffect(() => {
    if (query.isSuccess && page > totalPages) setPage(totalPages, { replace: true })
    // eslint-disable-next-line react-hooks/exhaustive-deps -- setPage 每次渲染新建，只看页码与总页数
  }, [query.isSuccess, page, totalPages])

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

  const handleDeleteIdentity = async (host: string, username: string) => {
    const ok = await confirmAction({
      title: '删除凭证',
      message: `删除用户名 ${username} 在 ${host} 的全部凭证？不可恢复。`,
      confirmLabel: '确认删除',
      danger: true,
    })
    if (!ok) return
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

  const hasFilter = filters.search !== ''

  return (
    <div className="flex h-full min-h-0 flex-col">
      <PageHeader title="凭证库">
        {/* 主创建动作与全站新建同位（头部右上）；筛选行只承担搜索/分页 */}
        {/* 录入中由行内「保存 / 取消」收尾：页头不再兼任取消（加号配「取消」语义自相矛盾）。 */}
        <Button
          disabled={creating}
          onClick={() => {
            setCreating(true)
            setNewRow(EMPTY_NEW_ROW)
          }}
        >
          <Plus />
          录入
        </Button>
      </PageHeader>

      <FilterBar
        label="凭证筛选"
        canReset={hasFilter}
        onReset={() => {
          search.setDraft('')
          resetFilters()
        }}
        trailing={
          <PaginationToolbar
            page={page}
            pageSize={size}
            total={total}
            totalPages={totalPages}
            onPageChange={setPage}
            onPageSizeChange={setSize}
          />
        }
      >
        <SearchInput
          className="w-64"
          value={search.draft}
          onChange={search.setDraft}
          onSubmit={search.submit}
          onRefresh={() => void query.refetch()}
          placeholder="搜索主机 / 用户名 / 键 / 值…"
          ariaLabel="搜索凭证"
        />
      </FilterBar>

      <div className="flex min-h-0 flex-1 flex-col overflow-y-auto px-6 py-4">
        {query.isError ? (
          <ErrorState
            message={query.error instanceof Error ? query.error.message : undefined}
            onRetry={() => void query.refetch()}
          />
        ) : (
          <div className="flex flex-col gap-3">
            {/* 录入新行（展开在表格上方；单凭证行起手，更多凭证保存后点凭证单元格追加） */}
            {creating && (
              <div
                role="group"
                aria-label="录入凭证"
                className="grid grid-cols-1 items-center gap-2 rounded-lg border border-dashed border-border px-3 py-2 sm:grid-cols-3 lg:grid-cols-[minmax(120px,1.2fr)_minmax(90px,0.8fr)_minmax(70px,0.6fr)_minmax(240px,3fr)_auto]"
              >
                <input
                  autoFocus
                  className={INLINE_INPUT + ' font-mono'}
                  placeholder="目标 host:port"
                  aria-label="目标主机"
                  value={newRow.host}
                  onChange={(e) => setNewRow({ ...newRow, host: e.target.value })}
                />
                <input
                  className={INLINE_INPUT + ' font-mono'}
                  placeholder="用户名"
                  aria-label="用户名"
                  value={newRow.username}
                  onChange={(e) => setNewRow({ ...newRow, username: e.target.value })}
                />
                <input
                  className={INLINE_INPUT}
                  placeholder="角色"
                  aria-label="角色"
                  value={newRow.role}
                  onChange={(e) => setNewRow({ ...newRow, role: e.target.value })}
                />
                <input
                  className={INLINE_INPUT + ' font-mono sm:col-span-3 lg:col-span-1'}
                  placeholder='Cookie: PHPSESSID=…（或 [query] token: xxx）'
                  aria-label="凭证"
                  value={newRow.raw}
                  onKeyDown={(e) => e.key === 'Enter' && commitCreate()}
                  onChange={(e) => setNewRow({ ...newRow, raw: e.target.value })}
                />
                <div className="flex items-center gap-1 sm:col-span-3 lg:col-span-1">
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
              rows={rows}
              loading={query.isPending}
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
