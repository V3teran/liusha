import { Children, useEffect, useMemo, useState, type ReactNode } from 'react'
import { ChevronLeft, ChevronRight, Plus, Search } from 'lucide-react'

// 每页卡片数：4 列 × 3 行的整屏网格。超过才出翻页条，数据少时零干扰。
const PAGE_SIZE = 12

// 服务端分页描述：父组件已按页取数，外壳只负责渲染翻页条并回调换页。
export interface ServerPaging {
  page: number // 1-based 当前页
  totalPages: number
  count: number // 总条数（跨页）
  onPage: (page: number) => void // 换页（传 1-based 目标页）
}

// 头部搜索框描述：受控输入，值与回调由父组件持有（配合服务端过滤）。
export interface SearchBox {
  value: string
  onChange: (v: string) => void
  placeholder?: string
}

interface ConfigListShellProps {
  title: string
  subtitle: string // 一句话说明该资源是什么
  loading: boolean
  error: string
  empty: boolean
  emptyHint: string
  onNew?: () => void // 缺省 = 不渲染「新建」（只读资源如工具目录）
  children: ReactNode // 卡片集合（每个资源一张 ConfigRow）
  search?: SearchBox // 传入 = 头部渲染搜索框
  server?: ServerPaging // 传入 = 服务端分页（外壳不再客户端切片）；缺省 = 客户端分页
  headerExtra?: ReactNode // 头部标题行右侧额外控件（如卡片/表格视图切换）
}

// 配置管理三页共用的列表外壳：头部（标题 + 说明 + 搜索 + 新建）+ 加载/错误/空态 + 卡片网格 + 分页。
// 两种分页模式：
//   - server 传入：父组件按页取数，外壳原样渲染当前页 children，翻页回调父组件重取。
//   - server 缺省：客户端分页，外壳按 PAGE_SIZE 切片 children。
export function ConfigListShell({
  title,
  subtitle,
  loading,
  error,
  empty,
  emptyHint,
  onNew,
  children,
  search,
  server,
  headerExtra,
}: ConfigListShellProps) {
  const items = useMemo(() => Children.toArray(children), [children])

  // 客户端分页态（仅 server 缺省时启用）。
  const clientTotalPages = Math.max(1, Math.ceil(items.length / PAGE_SIZE))
  const [clientPage, setClientPage] = useState(0)

  // 数据量变化（重载/增删）后夹紧页码，避免停在已不存在的空页。
  useEffect(() => {
    setClientPage((p) => Math.min(p, clientTotalPages - 1))
  }, [clientTotalPages])

  const pageItems = server ? items : items.slice(clientPage * PAGE_SIZE, clientPage * PAGE_SIZE + PAGE_SIZE)

  // 分页条参数：server 模式用父组件的 1-based 值；client 模式用内部 0-based 转 1-based。
  const showPager = server ? server.totalPages > 1 : clientTotalPages > 1

  return (
    <div className="flex h-full min-h-0 flex-col">
      <header className="flex items-center justify-between gap-4 border-b border-border px-6 py-4">
        <div className="min-w-0">
          <h1 className="tac-prompt font-mono text-[15px] font-semibold text-text">{title}</h1>
          <p className="mt-0.5 text-[12.5px] text-muted">{subtitle}</p>
        </div>
        <div className="flex flex-shrink-0 items-center gap-3">
          {search && (
            <div className="relative">
              <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted" />
              <input
                type="search"
                value={search.value}
                spellCheck={false}
                placeholder={search.placeholder ?? '搜索'}
                onChange={(e) => search.onChange(e.target.value)}
                className="w-52 rounded-md border border-border bg-surface py-1.5 pl-8 pr-2.5 text-[13px] text-text outline-none transition-shadow placeholder:text-faint focus:border-accent focus:shadow-[var(--glow-accent)]"
              />
            </div>
          )}
          {headerExtra}
          {onNew && (
            <button
              type="button"
              onClick={onNew}
              className="flex items-center gap-1.5 rounded-lg bg-accent px-3.5 py-1.5 text-[13px] text-white transition-all hover:bg-accent-hover hover:shadow-[var(--glow-accent-strong)]"
            >
              <Plus className="h-4 w-4" />
              新建
            </button>
          )}
        </div>
      </header>

      <div className="min-h-0 flex-1 overflow-y-auto p-6">
        {loading ? (
          <div className="tac-cursor py-16 text-center font-mono text-[13.5px] text-muted">加载中</div>
        ) : error ? (
          <div className="py-16 text-center font-mono text-[13.5px] text-sev-critical">⚠ {error}</div>
        ) : empty ? (
          <div className="py-16 text-center text-[13.5px] text-muted">{emptyHint}</div>
        ) : (
          <div className="grid grid-cols-[repeat(auto-fill,minmax(300px,1fr))] gap-4">{pageItems}</div>
        )}
      </div>

      {showPager &&
        (server ? (
          <PagerBar
            page={server.page - 1}
            totalPages={server.totalPages}
            count={server.count}
            onPage={(p) => server.onPage(p + 1)}
          />
        ) : (
          <PagerBar
            page={clientPage}
            totalPages={clientTotalPages}
            count={items.length}
            onPage={setClientPage}
          />
        ))}
    </div>
  )
}

// 翻页条：上一页/下一页 + 「第 x / y 页」。page 为 0-based。仅在多于一页时挂出。
function PagerBar({
  page,
  totalPages,
  count,
  onPage,
}: {
  page: number
  totalPages: number
  count: number
  onPage: (p: number) => void
}) {
  const btn =
    'flex items-center gap-1 rounded-md border border-border px-2.5 py-1 text-[12.5px] text-text transition-colors hover:border-accent/60 hover:bg-surface-2 disabled:cursor-not-allowed disabled:opacity-40 disabled:hover:border-border disabled:hover:bg-transparent'
  return (
    <div className="flex items-center justify-between border-t border-border px-6 py-2.5">
      <span className="font-mono text-[12px] text-muted">共 {count} 项</span>
      <div className="flex items-center gap-2">
        <button type="button" onClick={() => onPage(page - 1)} disabled={page === 0} className={btn}>
          <ChevronLeft className="h-3.5 w-3.5" />
          上一页
        </button>
        <span className="font-mono text-[12.5px] text-muted">
          第 {page + 1} / {totalPages} 页
        </span>
        <button
          type="button"
          onClick={() => onPage(page + 1)}
          disabled={page >= totalPages - 1}
          className={btn}
        >
          下一页
          <ChevronRight className="h-3.5 w-3.5" />
        </button>
      </div>
    </div>
  )
}

// 配置卡片：整卡可点开编辑抽屉。名称为主（大字），描述为次（多行省略），右上徽章/元信息。
// 不展示 code（内部标识符），面向用户只见名称与描述。
export function ConfigRow({
  name,
  description,
  onClick,
  right,
  dimmed = false,
  active = false,
}: {
  name: string
  description?: string // 次要说明，两行截断
  onClick: () => void
  right?: ReactNode // 右侧徽章/元信息
  dimmed?: boolean // disabled 项淡显
  active?: boolean // 选中态（主从视图预留）：荧光竖条 + 描边
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={
        'flex min-h-[120px] flex-col gap-2 rounded-xl border bg-surface px-5 py-4 text-left transition-all ' +
        (active
          ? 'tac-row-active'
          : 'border-border hover:border-accent/60 hover:bg-surface-2 hover:shadow-[var(--glow-accent)]') +
        (dimmed ? ' opacity-55' : '')
      }
    >
      <div className="flex items-start justify-between gap-2">
        <span className="min-w-0 flex-1 truncate text-[15px] font-semibold text-text">{name}</span>
        {right && <div className="flex-shrink-0">{right}</div>}
      </div>
      <p className="tac-clamp-2 min-w-0 text-[12.5px] leading-relaxed text-muted">
        {description || '暂无描述'}
      </p>
    </button>
  )
}
