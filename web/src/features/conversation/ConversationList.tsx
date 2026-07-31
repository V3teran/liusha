import { useCallback, useEffect, useImperativeHandle, useMemo, useRef, useState, forwardRef } from 'react'
import { createPortal } from 'react-dom'
import { NavLink } from 'react-router-dom'
import { Bug, ChevronLeft, ChevronRight, Clock3, Pencil, Plus, RefreshCw, Search, Trash2 } from 'lucide-react'
import { deleteConversation, listConversations, renameConversation } from '@/api/client'
import type { Conversation } from '@/api/types'
import { fullTime, relativeTime } from '@/lib/format'
import { scanStatusMeta } from '@/lib/scanStatus'
import { cn } from '@/lib/utils'

export interface ConversationListHandle {
  refresh: () => Promise<void>
}

interface ConversationListProps {
  activeId?: string
  mode?: string
  allowNew?: boolean
  emptyHint?: string
  heading?: string
  onSelect: (convID: string) => void
  onNew: () => void
  onDeleted: (convID: string) => void
}

// 每页条数：侧栏高度有限，配合翻页没必要拉大页；与后端默认对齐（parseLimit 默认也是 30）。
const PAGE_SIZE = 30

// 标题：去「我要扫描」前缀 + 截取 host 让列表更易读；空则回退短 id（智能标题由后端回填）。
function displayTitle(c: Conversation): string {
  const raw = (c.Title || '').replace(/^我要扫描\s*/, '').trim()
  if (!raw) return c.ID.slice(0, 8)
  const host = raw.match(/https?:\/\/([^/\s]+)/)?.[1]
  return host ? host + raw.replace(/https?:\/\/[^/\s]+/, '').slice(0, 24) : raw.slice(0, 40)
}

// 悬停 tooltip：展示完整标题（仅去「我要扫描」前缀，不截断）。
function fullTitle(c: Conversation): string {
  return (c.Title || '').replace(/^我要扫描\s*/, '').trim() || c.ID
}

const SCAN_ACTIVE_HINT = '扫描进行中，无法删除。请先在对话页点「停止扫描」，停止后再删除。'

// 头部主控件共用尺寸：「+ 新会话」按钮 与 passive 下不可点的「流量批次」占位保持同一盒模型，
// 两个 tab 切换时头部高度、字重、圆角完全一致，不做「按钮 vs 文字」的降级展示。
const HEADER_CONTROL = 'flex-1 inline-flex items-center justify-center gap-1.5 rounded-lg px-3 py-2.5 text-[13.5px] font-semibold transition-colors'

// 会话侧栏：按页拉会话（offset 翻页，见 api/client.ts listConversations 注释），点击向上抛选中 ID；
// 顶部「+ 新会话」抛 new。列表项展示序号 + 真实状态点 + 标题 + 相对时间，hover 出 ⋯ 更多菜单
// （删除/重命名），当前选中高亮。mode 过滤下沉到服务端——分页边界建立在已过滤集合上。
export const ConversationList = forwardRef<ConversationListHandle, ConversationListProps>(
  function ConversationList(
    { activeId, mode, allowNew = true, emptyHint, heading, onSelect, onNew, onDeleted },
    ref,
  ) {
    const [items, setItems] = useState<Conversation[]>([])
    const [offset, setOffset] = useState(0)
    const [hasMore, setHasMore] = useState(false)
    const [loading, setLoading] = useState(false)
    const [query, setQuery] = useState('')
    const [menuOpen, setMenuOpen] = useState('')
    const [menuConv, setMenuConv] = useState<Conversation | null>(null)
    const [menuStyle, setMenuStyle] = useState<{ top: string; left: string }>({ top: '0px', left: '0px' })
    const [deleting, setDeleting] = useState('')
    const [renamingId, setRenamingId] = useState('')
    const [renameText, setRenameText] = useState('')
    const menuOpenRef = useRef(menuOpen)
    menuOpenRef.current = menuOpen
    const itemsRef = useRef(items)
    itemsRef.current = items
    const offsetRef = useRef(offset)
    offsetRef.current = offset

    // load：拉某一页（o=offset）。翻页 / 轮询 / 手动刷新按钮共用，只是各自传入的 o 不同。
    const load = useCallback(
      async (o: number) => {
        setLoading(true)
        try {
          const { conversations, hasMore: hm } = await listConversations(PAGE_SIZE, o, mode ?? '')
          setItems(conversations)
          setHasMore(hm)
          setOffset(o)
        } finally {
          setLoading(false)
        }
      },
      [mode],
    )

    // 供外部（父组件）调用的 refresh：语义是「有新变化，给我最新视图」——回第 1 页，
    // 与「刷新列表」按钮/内部轮询（停留当前页，不打断浏览）区分开。
    const refreshFromTop = useCallback(() => load(0), [load])
    useImperativeHandle(ref, () => ({ refresh: refreshFromTop }), [refreshFromTop])

    useEffect(() => {
      void load(0)
      // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [mode])

    // 自适应轮询：仅当当前页存在「进行中(active)」会话时每 8s 刷新（停留当前页，不跳页）——
    // 感知后台会话跑完/状态变化。全部终态则不轮询；菜单开着时跳过（不打断操作）。
    useEffect(() => {
      const timer = window.setInterval(() => {
        if (menuOpenRef.current) return
        if (itemsRef.current.some((c) => c.RunStatus === 'active')) void load(offsetRef.current)
      }, 8000)
      return () => clearInterval(timer)
    }, [load])

    // 点外部关闭菜单。
    useEffect(() => {
      const close = () => {
        setMenuOpen('')
        setMenuConv(null)
      }
      document.addEventListener('click', close)
      return () => document.removeEventListener('click', close)
    }, [])

    // 搜索：仅在当前页内按标题（去前缀后）+ id 前缀过滤——分页后不再一次性拉全量，
    // 搜索范围随之收窄到当前页（这个数据量级下，配合 30 条/页，多数场景首页就够用）。
    const filtered = useMemo(() => {
      const q = query.trim().toLowerCase()
      if (!q) return items
      return items.filter((c) => fullTitle(c).toLowerCase().includes(q) || c.ID.toLowerCase().startsWith(q))
    }, [items, query])

    const pageNo = Math.floor(offset / PAGE_SIZE) + 1
    const prevPage = () => {
      if (offset <= 0 || loading) return
      void load(Math.max(0, offset - PAGE_SIZE))
    }
    const nextPage = () => {
      if (!hasMore || loading) return
      void load(offset + PAGE_SIZE)
    }

    const toggleMenu = (c: Conversation, ev: React.MouseEvent) => {
      ev.stopPropagation()
      if (menuOpen === c.ID) {
        setMenuOpen('')
        setMenuConv(null)
        return
      }
      const r = (ev.currentTarget as HTMLElement).getBoundingClientRect()
      setMenuStyle({ top: `${r.bottom + 4}px`, left: `${Math.max(8, r.right - 150)}px` })
      setMenuConv(c)
      setMenuOpen(c.ID)
    }

    const onDelete = async (c: Conversation, ev: React.MouseEvent) => {
      ev.stopPropagation()
      setMenuOpen('')
      if (deleting) return
      // 活跃扫描不许删（业界惯例：先停后删，防「删了会话、扫描脱缰、UI 再停不掉」的孤儿）。
      if (c.RunStatus === 'active') {
        window.alert(SCAN_ACTIVE_HINT)
        return
      }
      if (!window.confirm(`删除对话「${displayTitle(c)}」？\n对话和消息会删除，扫描成果（漏洞/图）保留。`)) return
      setDeleting(c.ID)
      try {
        await deleteConversation(c.ID)
        setItems((prev) => prev.filter((x) => x.ID !== c.ID))
        onDeleted(c.ID)
      } catch (e) {
        window.alert(e instanceof Error && e.message === 'SCAN_ACTIVE' ? SCAN_ACTIVE_HINT : '删除失败，请重试')
      } finally {
        setDeleting('')
      }
    }

    const startRename = (c: Conversation, ev: React.MouseEvent) => {
      ev.stopPropagation()
      setMenuOpen('')
      setMenuConv(null)
      setRenamingId(c.ID)
      setRenameText(fullTitle(c))
    }
    const cancelRename = () => {
      setRenamingId('')
      setRenameText('')
    }
    const setTitle = (convID: string, title: string) => {
      setItems((prev) => prev.map((x) => (x.ID === convID ? { ...x, Title: title } : x)))
    }
    const commitRename = async (c: Conversation) => {
      const next = renameText.trim()
      setRenamingId('')
      const prev = c.Title || ''
      if (next === fullTitle(c)) return // 无变化
      setTitle(c.ID, next) // 乐观更新
      try {
        await renameConversation(c.ID, next)
      } catch {
        setTitle(c.ID, prev) // 回滚
        window.alert('重命名失败，请重试')
      }
    }

    return (
      <aside className="flex h-full min-h-0 flex-col gap-2">
        {/* 主动/被动 tab：对话模块内部切换，两个 tab 是同一视图组件的两条路由。 */}
        <div className="flex flex-shrink-0 gap-1 rounded-[9px] bg-surface-2 p-0.5">
          <NavLink
            to="/conversations/active"
            className={({ isActive }) =>
              cn(
                'flex-1 rounded-[7px] py-1.5 text-center text-[12.5px] font-semibold text-muted transition-colors hover:text-text',
                isActive && 'bg-surface text-accent shadow-sm',
              )
            }
          >
            主动
          </NavLink>
          <NavLink
            to="/conversations/passive"
            className={({ isActive }) =>
              cn(
                'flex-1 rounded-[7px] py-1.5 text-center text-[12.5px] font-semibold text-muted transition-colors hover:text-text',
                isActive && 'bg-surface text-accent shadow-sm',
              )
            }
          >
            被动
          </NavLink>
        </div>
        <div className="flex flex-shrink-0 items-stretch gap-1.5">
          {allowNew ? (
            <button type="button" onClick={onNew} className={cn(HEADER_CONTROL, 'bg-accent text-white hover:bg-accent-hover')}>
              <Plus className="h-4 w-4" />
              新对话
            </button>
          ) : (
            <span
              aria-disabled="true"
              className={cn(HEADER_CONTROL, 'cursor-default border border-border bg-surface-2 text-muted')}
            >
              {heading || '对话'}
            </span>
          )}
          <button
            type="button"
            title="刷新列表"
            aria-label="刷新列表"
            onClick={() => void load(offset)}
            className="flex w-9 flex-shrink-0 items-center justify-center rounded-lg border border-border bg-surface-2 text-muted transition-colors hover:border-border-strong hover:text-text"
          >
            <RefreshCw className="h-4 w-4" />
          </button>
        </div>
        <div className="relative flex-shrink-0">
          <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-faint" />
          <input
            type="search"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="搜索本页对话…"
            aria-label="搜索对话"
            spellCheck={false}
            className="w-full rounded-lg border border-border bg-surface-2 py-1.5 pl-8 pr-2.5 text-xs text-text outline-none placeholder:text-muted focus:border-accent"
          />
        </div>
        {items.length > 0 && (
          <div className="flex-shrink-0 px-0.5 pb-0.5 font-mono text-[11px] text-muted">
            {query.trim() ? `本页 ${filtered.length}/${items.length} 条匹配` : `第 ${pageNo} 页 · 本页 ${items.length} 条`}
          </div>
        )}
        <ul className="flex min-h-0 flex-1 flex-col gap-0.5 overflow-y-auto pr-0.5">
          {filtered.map((c, idx) => {
            const meta = scanStatusMeta(c.RunStatus)
            return (
              <li
                key={c.ID}
                onClick={() => renamingId !== c.ID && onSelect(c.ID)}
                className={cn(
                  'group flex flex-shrink-0 items-start gap-2 rounded-lg px-2.5 py-2.5 cursor-pointer transition-colors hover:bg-surface-2',
                  c.ID === activeId && 'bg-surface-2 shadow-[inset_2px_0_0_var(--accent)]',
                )}
              >
                <span className="mt-0.5 w-4 flex-shrink-0 text-center font-mono text-[10.5px] leading-[18px] text-faint">
                  {offset + idx + 1}
                </span>
                <span
                  className={cn(
                    'mt-[7px] h-2 w-2 flex-shrink-0 rounded-full',
                    meta.key === 'active' && 'animate-pulse',
                  )}
                  style={{ background: meta.color }}
                  title={meta.label}
                />
                <div className="min-w-0 flex-1">
                  {renamingId === c.ID ? (
                    <input
                      autoFocus
                      value={renameText}
                      onChange={(e) => setRenameText(e.target.value)}
                      maxLength={80}
                      onClick={(e) => e.stopPropagation()}
                      onFocus={(e) => e.currentTarget.select()}
                      onKeyDown={(e) => {
                        if (e.key === 'Enter') {
                          e.preventDefault()
                          void commitRename(c)
                        } else if (e.key === 'Escape') {
                          e.preventDefault()
                          cancelRename()
                        }
                      }}
                      onBlur={() => void commitRename(c)}
                      className="w-full rounded-md border border-accent bg-surface px-1.5 py-0.5 text-[13px] text-text outline-none"
                    />
                  ) : (
                    <div className="flex items-center justify-between gap-2">
                      <span className="truncate text-[13px] font-medium text-text" title={fullTitle(c)}>
                        {displayTitle(c)}
                      </span>
                      <span
                        className="flex-shrink-0 whitespace-nowrap font-mono text-[10.5px] text-faint"
                        title={fullTime(c.CreatedAt)}
                      >
                        {relativeTime(c.CreatedAt)}
                      </span>
                    </div>
                  )}
                  <div className="mt-1.5 flex flex-wrap items-center gap-1.5">
                    <span
                      className="inline-flex items-center rounded-full px-1.5 py-0.5 text-[10.5px] font-semibold leading-none"
                      style={{ color: meta.color, background: `color-mix(in srgb, ${meta.color} 16%, transparent)` }}
                    >
                      {meta.label}
                    </span>
                    {!!c.FindingCount && (
                      <span
                        className="inline-flex items-center gap-1 rounded-full px-1.5 py-0.5 text-[10.5px] font-semibold leading-none text-sev-high"
                        style={{ background: 'color-mix(in srgb, var(--sev-high) 16%, transparent)' }}
                        title={`${c.FindingCount} 个漏洞`}
                      >
                        <Bug className="h-3 w-3" />
                        {c.FindingCount}
                      </span>
                    )}
                  </div>
                </div>
                <button
                  type="button"
                  title="更多"
                  aria-label="更多"
                  onClick={(ev) => toggleMenu(c, ev)}
                  className="mt-0.5 flex h-6 w-6 flex-shrink-0 items-center justify-center rounded-md text-muted opacity-0 hover:bg-surface group-hover:opacity-100 focus:opacity-100"
                >
                  <span className="text-base leading-none">⋯</span>
                </button>
              </li>
            )
          })}
          {filtered.length === 0 && (
            <li className="cursor-default justify-center text-xs text-muted">
              {query.trim() ? '本页无匹配对话' : emptyHint || '暂无对话'}
            </li>
          )}
        </ul>
        {/* 翻页：offset 分页（非 keyset），仅当有上一页或下一页时才露出，避免单页时的多余控件。 */}
        {(offset > 0 || hasMore) && (
          <div className="flex flex-shrink-0 items-center justify-between gap-2 pt-0.5">
            <button
              type="button"
              aria-label="上一页"
              disabled={offset <= 0 || loading}
              onClick={prevPage}
              className="flex h-7 w-7 items-center justify-center rounded-md border border-border bg-surface-2 text-muted transition-colors hover:border-border-strong hover:text-text disabled:cursor-not-allowed disabled:opacity-40"
            >
              <ChevronLeft className="h-3.5 w-3.5" />
            </button>
            <span className="font-mono text-[11px] text-muted">第 {pageNo} 页</span>
            <button
              type="button"
              aria-label="下一页"
              disabled={!hasMore || loading}
              onClick={nextPage}
              className="flex h-7 w-7 items-center justify-center rounded-md border border-border bg-surface-2 text-muted transition-colors hover:border-border-strong hover:text-text disabled:cursor-not-allowed disabled:opacity-40"
            >
              <ChevronRight className="h-3.5 w-3.5" />
            </button>
          </div>
        )}
        {/* ⋯ 菜单挂到 body：fixed 视口坐标定位，不被 ul overflow 裁剪 */}
        {menuConv &&
          createPortal(
            <div
              className="fixed z-[1000] min-w-[148px] rounded-lg border border-border bg-surface p-1 shadow-lg"
              style={menuStyle}
              onClick={(e) => e.stopPropagation()}
            >
              <button
                type="button"
                onClick={(ev) => startRename(menuConv, ev)}
                className="flex w-full items-center gap-2 rounded-md px-2.5 py-1.5 text-left text-xs text-text hover:bg-surface-2"
              >
                <Pencil className="h-3.5 w-3.5" />
                重命名
              </button>
              {menuConv.RunStatus === 'active' ? (
                <button
                  type="button"
                  disabled
                  title="扫描进行中，先在对话页「停止扫描」，停止后再删除"
                  className="flex w-full cursor-default items-center gap-2 rounded-md px-2.5 py-1.5 text-left text-xs text-muted opacity-50"
                >
                  <Clock3 className="h-3.5 w-3.5" />
                  扫描中 · 先停止再删
                </button>
              ) : (
                <button
                  type="button"
                  disabled={deleting === menuConv.ID}
                  onClick={(ev) => void onDelete(menuConv, ev)}
                  className="flex w-full items-center gap-2 rounded-md px-2.5 py-1.5 text-left text-xs text-text hover:bg-red-500/15 hover:text-red-500 disabled:opacity-50"
                >
                  <Trash2 className="h-3.5 w-3.5" />
                  删除对话
                </button>
              )}
            </div>,
            document.body,
          )}
      </aside>
    )
  },
)
