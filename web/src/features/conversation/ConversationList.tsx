import { SearchInput } from '@/components/SearchInput'
import { useDebouncedSearch } from '@/hooks/useDebouncedSearch'
import type { HistoryOptions } from '@/hooks/useSelectedParam'
import { useCallback, useEffect, useImperativeHandle, useMemo, useRef, useState, forwardRef } from 'react'
import { NavLink, useSearchParams } from 'react-router-dom'
import { Bug, ChevronLeft, ChevronRight, Plus, RefreshCw } from 'lucide-react'
import { deleteConversation, listConversations, renameConversation } from '@/api/conversations'
import type { Conversation } from '@/api/types'
import { ConvMenu } from './ConvMenu'
import { fullTime, relativeTime } from '@/lib/format'
import { taskStatusMeta } from '@/lib/taskStatus'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/Button'
import { PageSizeSelect } from '@/components/ui/PageSizeSelect'
import { EmptyState } from '@/components/ui/EmptyState'
import { ErrorState } from '@/components/ui/ErrorState'
import { toast } from '@/components/ui/toast'
import { confirmAction } from '@/components/ui/confirm'

export interface ConversationListHandle {
  refresh: () => Promise<void>
}

interface ConversationListProps {
  activeId?: string
  source?: string
  allowNew?: boolean
  /** 无数据时的空态：名词与说明（数据从哪来）；搜索无结果统一为「没有匹配的X」 */
  emptyState: { noun: string; hint: string }
  /** 首次加载成功且当前无选中时，自动打开最新一条会话（一次性，不劫持后续交互）。 */
  autoSelect?: boolean
  /** 打开对话：用户点选新增一条历史；进页自动打开最新对话传 replace */
  onSelect: (convID: string, opts?: HistoryOptions) => void
  onNew: () => void
  onDeleted: (convID: string) => void
}

// 每页条数：侧栏高度有限，配合翻页没必要拉大页；与后端默认对齐（parseLimit 默认也是 30）。
const PAGE_SIZE_OPTIONS = [30, 50, 100]
const DEFAULT_PAGE_SIZE = 30

// 标题：去「我要扫描」前缀 + 截取 host 让列表更易读；空则回退短 id（智能标题由后端回填）。
function displayTitle(c: Conversation): string {
  const raw = (c.title || '').replace(/^我要扫描\s*/, '').trim()
  if (!raw) return c.id.slice(0, 8)
  const host = raw.match(/https?:\/\/([^/\s]+)/)?.[1]
  return host ? host + raw.replace(/https?:\/\/[^/\s]+/, '').slice(0, 24) : raw.slice(0, 40)
}

// 悬停 tooltip：展示完整标题（仅去「我要扫描」前缀，不截断）。
function fullTitle(c: Conversation): string {
  return (c.title || '').replace(/^我要扫描\s*/, '').trim() || c.id
}

const TASK_ACTIVE_HINT = '任务进行中，无法删除。请先在对话页停止任务，删除前先等任务结束。'

// 会话侧栏：按页拉会话（offset 翻页，见 api/client.ts listConversations 注释），点击向上抛选中 ID；
// 顶部「新对话」抛 new。列表项展示序号 + 真实状态点 + 标题 + 相对时间，hover 出 ⋯ 更多菜单
// （删除/重命名），当前选中高亮。source 过滤（direct/proxy）下沉到服务端——分页边界建立在已过滤集合上。
export const ConversationList = forwardRef<ConversationListHandle, ConversationListProps>(
  function ConversationList(
    { activeId, source, allowNew = true, emptyState, autoSelect, onSelect, onNew, onDeleted },
    ref,
  ) {
    const [searchParams, setSearchParams] = useSearchParams()
    const size = useMemo(() => {
      const raw = Number(searchParams.get('size'))
      return (PAGE_SIZE_OPTIONS as readonly number[]).includes(raw) ? raw : DEFAULT_PAGE_SIZE
    }, [searchParams])
    const setSize = (n: number) =>
      setSearchParams((prev) => {
        const p = new URLSearchParams(prev)
        if (n !== DEFAULT_PAGE_SIZE) p.set('size', String(n))
        else p.delete('size')
        return p
      })
    const [items, setItems] = useState<Conversation[]>([])
    const [offset, setOffset] = useState(0)
    const [hasMore, setHasMore] = useState(false)
    const [total, setTotal] = useState(0)
    const [loading, setLoading] = useState(false)
    const [loadError, setLoadError] = useState('')
    const [query, setQuery] = useState('')
    // 关键词在服务端检索（覆盖全部会话，分页与总数同一口径），停顿后生效
    const debouncedQuery = useDebouncedSearch(query.trim())
    // 当前展示的数据是哪个关键词检索出来的：计数与空态跟随它，而不是跟随已生效的关键词——
    // 新结果回来之前屏上仍是旧数据，不能把旧的总数标成「匹配 N 条」。
    const [shownQuery, setShownQuery] = useState('')
    // 正在打开「⋯」菜单的对话：菜单开着时暂停轮询刷新（不在操作中途换掉列表）
    const [menuOpen, setMenuOpen] = useState('')
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
    // 请求序号：只有最新一次 load 的结果可以落地——切来源/每页条数、轮询与翻页重叠时，
    // 迟到的旧响应不得覆盖新列表（否则界面显示错来源或跳回旧页）。
    const reqSeqRef = useRef(0)
    const load = useCallback(
      async (o: number) => {
        const seq = ++reqSeqRef.current
        setLoading(true)
        try {
          const { conversations, hasMore: hm, total: tt } = await listConversations({
            limit: size,
            offset: o,
            source: source ?? '',
            search: debouncedQuery,
          })
          if (seq !== reqSeqRef.current) return
          setItems(conversations)
          setHasMore(hm)
          setTotal(tt)
          setShownQuery(debouncedQuery)
          setOffset(o)
          setLoadError('')
        } catch (e) {
          if (seq !== reqSeqRef.current) return
          // 拉取失败必须与「暂无对话」区分——否则网络抖动伪装成空列表
          setLoadError(e instanceof Error ? e.message : '加载失败')
        } finally {
          if (seq === reqSeqRef.current) setLoading(false)
        }
      },
      [source, size, debouncedQuery],
    )

    // 供外部（父组件）调用的 refresh：语义是「有新变化，给我最新视图」——回第 1 页，
    // 与「刷新列表」按钮/内部轮询（停留当前页，不打断浏览）区分开。
    const refreshFromTop = useCallback(() => load(0), [load])
    useImperativeHandle(ref, () => ({ refresh: refreshFromTop }), [refreshFromTop])

    useEffect(() => {
      void load(0)
      // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [source, size, debouncedQuery])

    // 进入页面默认打开最新会话：首次成功加载且没有任何选中（无 ?conversation 深链接、
    // 用户没点过）时选第一条——一次性消费，之后轮询刷新/「新对话」态都不再劫持。
    const autoSelectedRef = useRef(false)
    useEffect(() => {
      if (!autoSelect || autoSelectedRef.current || loading || loadError) return
      if (items.length === 0) return
      autoSelectedRef.current = true
      if (!activeId) onSelect(items[0].id, { replace: true })
      // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [items, loading, loadError, activeId, autoSelect])

    // 自适应轮询：仅当当前页存在「进行中(active)」会话时每 8s 刷新（停留当前页，不跳页）——
    // 感知后台会话跑完/状态变化。全部终态则不轮询；菜单开着时跳过（不打断操作）。
    useEffect(() => {
      const timer = window.setInterval(() => {
        if (menuOpenRef.current) return
        if (itemsRef.current.some((c) => c.run_status === 'active')) void load(offsetRef.current)
      }, 8000)
      return () => clearInterval(timer)
    }, [load])


    const pageNo = Math.floor(offset / size) + 1
    const totalPages = Math.max(1, Math.ceil(total / size))
    const prevPage = () => {
      if (offset <= 0 || loading) return
      void load(Math.max(0, offset - size))
    }
    const nextPage = () => {
      if (!hasMore || loading) return
      void load(offset + size)
    }

    const onDelete = async (c: Conversation) => {
      setMenuOpen('')
      if (deleting) return
      // 活跃扫描不许删（业界惯例：先停后删，防「删了会话、扫描脱缰、UI 再停不掉」的孤儿）。
      if (c.run_status === 'active') {
        toast.error(TASK_ACTIVE_HINT)
        return
      }
      const ok = await confirmAction({
        title: '删除对话',
        message: `删除对话「${displayTitle(c)}」？对话和消息会删除，扫描成果（发现、探索图）保留。`,
        confirmLabel: '确认删除',
        danger: true,
      })
      if (!ok) return
      setDeleting(c.id)
      try {
        await deleteConversation(c.id)
        setItems((prev) => prev.filter((x) => x.id !== c.id))
        onDeleted(c.id)
      } catch (e) {
        toast.error(e instanceof Error && e.message === 'TASK_ACTIVE' ? TASK_ACTIVE_HINT : '删除对话失败，请重试')
      } finally {
        setDeleting('')
      }
    }

    const startRename = (c: Conversation) => {
      setMenuOpen('')
      setRenamingId(c.id)
      setRenameText(fullTitle(c))
    }
    const cancelRename = () => {
      setRenamingId('')
      setRenameText('')
    }
    const setTitle = (convID: string, title: string) => {
      setItems((prev) => prev.map((x) => (x.id === convID ? { ...x, title } : x)))
    }
    const commitRename = async (c: Conversation) => {
      const next = renameText.trim()
      setRenamingId('')
      const prev = c.title || ''
      if (next === fullTitle(c)) return // 无变化
      setTitle(c.id, next) // 乐观更新
      try {
        await renameConversation(c.id, next)
      } catch {
        setTitle(c.id, prev) // 回滚
        toast.error('重命名失败，已恢复原标题')
      }
    }

    return (
      <aside className="flex h-full min-h-0 flex-col gap-2">
        {/* 人工下发/代理捕获 tab：按来源（source=direct/proxy）切换，两个 tab 是同一视图组件的两条路由。 */}
        <div className="flex flex-shrink-0 gap-1 rounded-lg bg-surface-2 p-0.5">
          <NavLink
            to="/conversations/direct"
            className={({ isActive }) =>
              cn(
                'flex-1 rounded-md py-1.5 text-center text-sm font-semibold text-text-secondary transition-colors hover:text-text',
                isActive && 'bg-surface text-accent shadow-sm',
              )
            }
          >
            人工下发
          </NavLink>
          <NavLink
            to="/conversations/proxy"
            className={({ isActive }) =>
              cn(
                'flex-1 rounded-md py-1.5 text-center text-sm font-semibold text-text-secondary transition-colors hover:text-text',
                isActive && 'bg-surface text-accent shadow-sm',
              )
            }
          >
            代理捕获
          </NavLink>
        </div>
        {/* 发起入口只在可发起的来源出现（代理捕获没有）；刷新跟着搜索框走，两种来源同一位置 */}
        {allowNew && (
          <Button className="w-full flex-shrink-0" onClick={onNew}>
            <Plus />
            新对话
          </Button>
        )}
        <div className="flex flex-shrink-0 items-center gap-1.5 border-b border-border pb-2.5">
          <SearchInput
            className="min-w-0 flex-1"
            value={query}
            onChange={setQuery}
            placeholder="搜索对话标题、任务目标、ID…"
            ariaLabel="搜索对话"
          />
          <Button variant="secondary" className="w-9 flex-shrink-0 px-0" title="刷新列表" aria-label="刷新列表" onClick={() => void load(offset)}>
            <RefreshCw />
          </Button>
        </div>
        {items.length > 0 && (
          <div className="flex-shrink-0 px-0.5 pb-0.5 font-mono text-xs text-text-secondary">
            {/* 区间+总数：一行同时表达「看到哪了」与「总量」（搜索时是匹配结果的区间与总数） */}
            {`第 ${offset + 1}–${offset + items.length} 条 · ${shownQuery ? '匹配' : '共'} ${total} 条`}
          </div>
        )}
        {items.length === 0 ? null : (
        <ul className="flex min-h-0 flex-1 flex-col gap-0.5 overflow-y-auto pr-0.5">
          {items.map((c) => {
            const meta = taskStatusMeta(c.run_status)
            return (
              <li
                key={c.id}
                onClick={() => renamingId !== c.id && onSelect(c.id)}
                className={cn(
                  'group flex flex-shrink-0 items-start gap-2 rounded-lg px-2.5 py-2.5 cursor-pointer transition-colors hover:bg-surface-2',
                  c.id === activeId && 'bg-surface-2 shadow-[inset_2px_0_0_var(--accent)]',
                )}
              >
                <span
                  className={cn(
                    'mt-[7px] h-2 w-2 flex-shrink-0 rounded-full',
                    // 进行中只让状态点呼吸；文字胶囊恒定不透明（脉冲会把文字对比度拉到 AA 以下）
                    meta.key === 'active' && !c.run_paused && 'motion-safe:animate-pulse',
                  )}
                  style={{ background: meta.color }}
                  title={meta.label}
                />
                <div className="min-w-0 flex-1">
                  {renamingId === c.id ? (
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
                      className="w-full rounded-md border border-accent bg-surface px-1.5 py-0.5 text-sm max-sm:text-base text-text outline-none"
                    />
                  ) : (
                    <div className="flex items-center justify-between gap-2">
                      <span className="truncate text-sm font-medium text-text" title={fullTitle(c)}>
                        {displayTitle(c)}
                      </span>
                      <span
                        className="flex-shrink-0 whitespace-nowrap font-mono text-xs text-text-tertiary"
                        title={fullTime(c.created_at)}
                      >
                        {relativeTime(c.created_at)}
                      </span>
                    </div>
                  )}
                  <div className="mt-1.5 flex flex-wrap items-center gap-1.5">
                    <span
                      className="inline-flex items-center rounded-full px-1.5 py-0.5 text-xs font-semibold leading-none"
                      style={{ color: meta.color, background: `color-mix(in srgb, ${meta.color} 16%, var(--surface))` }}
                    >
                      {meta.label}
                    </span>
                    {!!c.finding_count && (
                      <span
                        className="inline-flex items-center gap-1 rounded-full px-1.5 py-0.5 text-xs font-semibold leading-none text-sev-high"
                        style={{ background: 'color-mix(in srgb, var(--sev-high) 16%, var(--surface))' }}
                        title={`${c.finding_count} 个发现`}
                      >
                        <Bug className="h-3 w-3" />
                        {c.finding_count}
                      </span>
                    )}
                  </div>
                </div>
                <ConvMenu
                  conv={c}
                  deleting={deleting}
                  onOpenChange={(open) => setMenuOpen(open ? c.id : '')}
                  onRename={startRename}
                  onDelete={onDelete}
                />
              </li>
            )
          })}
        </ul>
        )}
        {/* 加载失败只提示一处：已有的列表保留（刷新失败不清空屏幕），提示与重试排在它下面 */}
        {loadError && (
          <ErrorState
            title="对话列表加载失败"
            message={loadError}
            onRetry={() => void load(offsetRef.current)}
            className={items.length > 0 ? 'py-6' : 'min-h-0 flex-1'}
          />
        )}
        {!loadError && !loading && items.length === 0 && (
          /* 空态撑满列表区：空数据也是一等界面状态 */
          shownQuery ? (
            <EmptyState kind="no-match" scope="search" noun={emptyState.noun} className="min-h-0 flex-1" />
          ) : (
            <EmptyState kind="empty" noun={emptyState.noun} hint={emptyState.hint} className="min-h-0 flex-1" />
          )
        )}
        {/* 翻页：offset 分页（非 keyset）。常显（单页时按钮禁用）——「第 X / Y 页」总页数
            来自后端同口径 count，翻页能力始终可见可发现。 */}
        {items.length > 0 && (
          <div className="flex flex-shrink-0 items-center gap-1.5 pt-0.5">
            <button
              type="button"
              aria-label="上一页"
              disabled={offset <= 0 || loading}
              onClick={prevPage}
              className="flex h-7 w-7 items-center justify-center rounded-md border border-border bg-surface-2 text-text-secondary transition-colors hover:border-border-strong hover:text-text disabled:cursor-not-allowed disabled:opacity-40"
            >
              <ChevronLeft className="h-3.5 w-3.5" />
            </button>
            <span className="whitespace-nowrap font-mono text-xs text-text-secondary">
              第 {pageNo}/{totalPages} 页
            </span>
            <button
              type="button"
              aria-label="下一页"
              disabled={!hasMore || loading}
              onClick={nextPage}
              className="flex h-7 w-7 items-center justify-center rounded-md border border-border bg-surface-2 text-text-secondary transition-colors hover:border-border-strong hover:text-text disabled:cursor-not-allowed disabled:opacity-40"
            >
              <ChevronRight className="h-3.5 w-3.5" />
            </button>
            <PageSizeSelect value={size} onChange={setSize} className="ml-auto" />
          </div>
        )}
      </aside>
    )
  },
)
