import { createColumnHelper } from '@tanstack/react-table'
import { Plus, Trash2, X } from 'lucide-react'
import { CopyButton } from '@/components/CopyButton'
import { useCopyToClipboard } from '@/hooks/useCopyToClipboard'
import { dateOnly, clockTime } from '@/lib/format'
import type { CredentialEntry } from '@/api/types'
import type { CredentialRow } from './useCredentialQueries'

const columnHelper = createColumnHelper<CredentialRow>()

/** 会话任务状态 → 短标签（列表密度：一词，不占列宽）。 */
const TASK_STATUS_SHORT: Record<string, string> = {
  active: '进行中',
  completed: '已完成',
  aborted: '已终止',
  failed: '失败',
  gone: '任务已删',
}

/** 凭证条目 → 原始 HTTP 形态行：headers 型是请求头原文（`Cookie: value`）；
 * query/body 型带位置前缀标注（它们不是请求头，是注入参数）。 */
export function credentialRawLine(c: CredentialEntry): string {
  if (c.type === 'headers') return `${c.key}: ${c.value}`
  return `[${c.type}] ${c.key}: ${c.value}`
}

// ─── 行内编辑（Airtable/Sheets 范式：点击单元格即编辑，无弹层）─────────────
// 编辑回调经 table.options.meta 注入（react-table 惯用法）。
// 主机与用户名锁定（换=删旧录新），可编辑的只有角色与凭证条目。

export interface CredentialTableMeta {
  /** 单元格进入编辑态（rowId + 字段定位；凭证编辑带条目下标）。 */
  onBeginEdit: (rowId: string, field: 'role' | 'cred' | 'cred-add', credIndex?: number) => void
  /** 编辑中草稿的暂存值（页面持有；空串=无编辑）。 */
  editDraft: { rowId: string; field: 'role' | 'cred' | 'cred-add'; credIndex?: number; value: string } | null
  /** 草稿变更。 */
  onDraftChange: (value: string) => void
  /** 确认提交（角色=新文本；凭证=新原始行；新增=原始行）。 */
  onCommitEdit: (value?: string) => void
  /** 取消编辑。 */
  onCancelEdit: () => void
  /** 删除用户名的全部凭证。 */
  onDeleteIdentity: (host: string, username: string) => void
  /** 删除单条凭证（credIndex）。 */
  onDeleteCred: (row: CredentialRow, credIndex: number) => void
}

/** 行内编辑输入框：Enter 提交、Esc 取消、失焦提交；自动聚焦全选。 */
function InlineInput({
  initial,
  placeholder,
  onCommit,
  onCancel,
  mono = true,
}: {
  initial: string
  placeholder?: string
  onCommit: (v: string) => void
  onCancel: () => void
  mono?: boolean
}) {
  return (
    <input
      autoFocus
      defaultValue={initial}
      placeholder={placeholder}
      onFocus={(e) => e.target.select()}
      onBlur={(e) => onCommit(e.target.value.trim())}
      onKeyDown={(e) => {
        if (e.key === 'Enter') {
          e.preventDefault()
          onCommit(e.currentTarget.value.trim())
        } else if (e.key === 'Escape') {
          e.preventDefault()
          onCancel()
        }
      }}
      className={
        'h-7 w-full rounded-md border border-accent/60 bg-surface px-2 text-sm text-text outline-none focus:border-accent focus:glow-accent ' +
        (mono ? 'font-mono' : '')
      }
    />
  )
}

/** 解析原始行为 CredentialEntry：`Cookie: a=b` → headers.Cookie；`[query] k: v` → query。 */
export function parseCredentialRawLine(line: string): CredentialEntry | null {
  const t = line.trim()
  if (!t) return null
  let type = 'headers'
  let rest = t
  const m = t.match(/^\[(headers|query|body)\]\s*(.*)$/i)
  if (m) {
    type = m[1].toLowerCase()
    rest = m[2]
  }
  const ci = rest.indexOf(':')
  if (ci <= 0) return null
  return { type, key: rest.slice(0, ci).trim(), value: rest.slice(ci + 1).trim() }
}

/** 主机列：存储键原样（CanonicalHost 归一的 host[:port]，Burp 的 Host 列同款语义）。 */
const addressCol = columnHelper.accessor('host', {
  id: 'host',
  header: '主机',
  size: 150,
  cell: (ctx) => <CopyableMono value={ctx.getValue()} />,
})

/** 用户名列：登录用户名（锁定不可编辑——换用户名是删旧录新）+ 悬停复制。 */
const usernameCol = columnHelper.accessor('identity.username', {
  header: '用户名',
  size: 110,
  cell: (ctx) => {
    const row = ctx.row.original
    return (
      <div className="flex min-w-0 flex-col gap-px">
        <CopyableMono value={row.identity.username} />
        {row.identity.kind === 'session' && row.identity.task_status && (
          <span className="text-xs text-muted opacity-75">
            会话 · {TASK_STATUS_SHORT[row.identity.task_status] ?? row.identity.task_status}
          </span>
        )}
      </div>
    )
  },
})

/** 角色列：点击即编辑（行内，Enter 提交）。 */
const roleCol = columnHelper.accessor('identity.role', {
  header: '角色',
  size: 80,
  cell: (ctx) => {
    const row = ctx.row.original
    const meta = ctx.table.options.meta as CredentialTableMeta
    const draft = meta.editDraft
    const editing = draft?.rowId === row.rowId && draft.field === 'role'
    if (editing) {
      return (
        <InlineInput
          initial={draft.value}
          placeholder="admin"
          mono={false}
          onCommit={(v) => meta.onCommitEdit(v)}
          onCancel={meta.onCancelEdit}
        />
      )
    }
    return (
      <span
        role="button"
        tabIndex={0}
        title="点击编辑角色"
        onClick={() => meta.onBeginEdit(row.rowId, 'role')}
        onKeyDown={(e) => e.key === 'Enter' && meta.onBeginEdit(row.rowId, 'role')}
        className="block min-w-0 cursor-text truncate rounded px-1 py-0.5 text-xs text-muted hover:bg-surface-2 hover:text-text"
      >
        {ctx.getValue() || <span className="opacity-50">—</span>}
      </span>
    )
  },
})

/** 凭证列（主列）：每条一行原始 HTTP 形态，点击行进入编辑；尾部 + 新增；
 * 每条悬停出删除。 */
const valuesCol = columnHelper.accessor('identity.credentials', {
  id: 'values',
  header: '凭证',
  size: 420,
  cell: (ctx) => {
    const row = ctx.row.original
    const meta = ctx.table.options.meta as CredentialTableMeta
    const draft = meta.editDraft
    const editingThis =
      draft?.rowId === row.rowId && (draft.field === 'cred' || draft.field === 'cred-add')
    const { copiedKey, copy } = useCopyToClipboard()
    const creds = ctx.getValue() ?? []
    const all = creds.map(credentialRawLine).join('\n')

    if (editingThis) {
      // 编辑态：编辑中的条目渲染输入框（原始行格式），其余条目保持只读行
      return (
        <div className="space-y-1">
          {creds.map((c, i) => {
            if (draft!.field === 'cred' && draft!.credIndex === i) {
              return (
                <InlineInput
                  key={i}
                  initial={draft!.value}
                  placeholder="Cookie: PHPSESSID=…"
                  onCommit={(v) => meta.onCommitEdit(v)}
                  onCancel={meta.onCancelEdit}
                />
              )
            }
            const line = credentialRawLine(c)
            return (
              <div key={i} className="truncate font-mono text-xs leading-[1.6] text-muted opacity-60" title={line}>
                {line}
              </div>
            )
          })}
          {draft!.field === 'cred-add' && (
            <InlineInput
              initial={draft!.value}
              placeholder="Cookie: PHPSESSID=… / [query] token: xxx"
              onCommit={(v) => meta.onCommitEdit(v)}
              onCancel={meta.onCancelEdit}
            />
          )}
        </div>
      )
    }

    return (
      <div className="group/cell flex min-w-0 items-start gap-1">
        <div className="min-w-0 flex-1">
          {creds.length === 0 && <span className="text-xs text-muted">—</span>}
          {creds.map((c, i) => {
            const line = credentialRawLine(c)
            return (
              <div key={i} className="flex items-center gap-1">
                <span
                  role="button"
                  tabIndex={0}
                  title="点击编辑此凭证（原始 HTTP 行）"
                  onClick={() => meta.onBeginEdit(row.rowId, 'cred', i)}
                  onKeyDown={(e) => e.key === 'Enter' && meta.onBeginEdit(row.rowId, 'cred', i)}
                  className="block min-w-0 flex-1 cursor-text truncate rounded px-1 font-mono text-xs leading-[1.6] text-text hover:bg-surface-2"
                >
                  {line}
                </span>
                <button
                  type="button"
                  aria-label={`删除凭证 ${c.key}`}
                  onClick={(e) => {
                    e.stopPropagation()
                    meta.onDeleteCred(row, i)
                  }}
                  className="flex-shrink-0 rounded p-0.5 text-muted opacity-0 transition-opacity hover:text-status-error group-hover/cell:opacity-100"
                >
                  <X className="h-3 w-3" />
                </button>
              </div>
            )
          })}
          <button
            type="button"
            aria-label="新增凭证"
            title="新增一条凭证（原始 HTTP 行）"
            onClick={() => meta.onBeginEdit(row.rowId, 'cred-add')}
            className="mt-0.5 inline-flex items-center gap-0.5 rounded px-1 text-xs text-accent opacity-0 transition-opacity hover:underline group-hover/cell:opacity-100"
          >
            <Plus className="h-3 w-3" /> 添加
          </button>
        </div>
        {creds.length > 0 && (
          <CopyButton
            copied={copiedKey === 'cell'}
            onClick={() => copy('cell', all)}
            className="flex-shrink-0 opacity-0 transition-opacity group-hover/cell:opacity-100"
          />
        )}
      </div>
    )
  },
})

/** 创建时间列。 */
const createdCol = columnHelper.accessor('identity.created_at', {
  header: '创建时间',
  size: 96,
  cell: (ctx) => {
    const iso = ctx.getValue()
    if (!iso) return <span className="text-xs text-muted">—</span>
    return (
      <span className="flex flex-col gap-px whitespace-nowrap font-mono text-xs text-text" title={iso}>
        <span>{dateOnly(iso)}</span>
        <em className="block text-xs font-normal not-italic text-muted opacity-75">{clockTime(iso)}</em>
      </span>
    )
  },
})

/** 修改时间列。 */
const updatedCol = columnHelper.accessor('identity.updated_at', {
  header: '修改时间',
  size: 96,
  cell: (ctx) => {
    const iso = ctx.getValue()
    if (!iso) return <span className="text-xs text-muted">—</span>
    return (
      <span className="flex flex-col gap-px whitespace-nowrap font-mono text-xs text-text" title={iso}>
        <span>{dateOnly(iso)}</span>
        <em className="block text-xs font-normal not-italic text-muted opacity-75">{clockTime(iso)}</em>
      </span>
    )
  },
})

/** 操作列：删除该用户名的全部凭证。 */
const actionCol = columnHelper.display({
  id: 'actions',
  header: '',
  size: 40,
  cell: (ctx) => {
    const row = ctx.row.original
    return (
      <button
        type="button"
        aria-label={`删除用户名 ${row.identity.username} 的凭证`}
        title={`删除用户名 ${row.identity.username} 的全部凭证`}
        onClick={(e) => {
          e.stopPropagation()
          ;(ctx.table.options.meta as CredentialTableMeta).onDeleteIdentity(row.host, row.identity.username)
        }}
        className="rounded p-1.5 text-muted transition-colors hover:bg-surface-hover hover:text-status-error"
      >
        <Trash2 className="h-4 w-4" />
      </button>
    )
  },
})

export function buildCredentialColumns() {
  return [addressCol, usernameCol, roleCol, valuesCol, createdCol, updatedCol, actionCol]
}

/** CopyableMono：等宽文本单元格——截断 + hover 全文 + 悬停复制按钮（流量表同形态）。 */
function CopyableMono({ value }: { value: string }) {
  const { copiedKey, copy } = useCopyToClipboard()
  const text = value || '—'
  return (
    <span className="group/cell flex items-center gap-1">
      <span className="min-w-0 flex-1 truncate font-mono text-sm text-text" title={value || undefined}>
        {text}
      </span>
      {value && (
        <CopyButton
          copied={copiedKey === 'cell'}
          onClick={() => copy('cell', value)}
          className="flex-shrink-0 opacity-0 transition-opacity group-hover/cell:opacity-100"
        />
      )}
    </span>
  )
}

export { TASK_STATUS_SHORT }
