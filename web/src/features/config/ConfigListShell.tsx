import type { ReactNode } from 'react'
import { Plus } from 'lucide-react'

interface ConfigListShellProps {
  title: string
  subtitle: string // 一句话说明该资源是什么
  loading: boolean
  error: string
  empty: boolean
  emptyHint: string
  onNew: () => void
  children: ReactNode // 列表主体（行）
}

// 配置管理三页共用的列表外壳：头部（标题 + 说明 + 新建）+ 加载/错误/空态 + 列表容器。
// 各页只管把「行」作为 children 注入，状态分支与骨架统一在此。
export function ConfigListShell({
  title,
  subtitle,
  loading,
  error,
  empty,
  emptyHint,
  onNew,
  children,
}: ConfigListShellProps) {
  return (
    <div className="flex h-full min-h-0 flex-col">
      <header className="flex items-center justify-between border-b border-border px-6 py-4">
        <div>
          <h1 className="text-[15px] font-semibold text-text">{title}</h1>
          <p className="mt-0.5 text-[12.5px] text-muted">{subtitle}</p>
        </div>
        <button
          type="button"
          onClick={onNew}
          className="flex items-center gap-1.5 rounded-lg bg-accent px-3.5 py-1.5 text-[13px] text-white transition-colors hover:bg-accent-hover"
        >
          <Plus className="h-4 w-4" />
          新建
        </button>
      </header>

      <div className="min-h-0 flex-1 overflow-y-auto p-6">
        {loading ? (
          <div className="py-16 text-center text-[13.5px] text-muted">加载中…</div>
        ) : error ? (
          <div className="py-16 text-center text-[13.5px] text-sev-critical">⚠ {error}</div>
        ) : empty ? (
          <div className="py-16 text-center text-[13.5px] text-muted">{emptyHint}</div>
        ) : (
          <div className="flex flex-col gap-2">{children}</div>
        )}
      </div>
    </div>
  )
}

// 列表行：整行可点开编辑抽屉，展示 code（等宽）+ name + 右侧 meta/徽章。
export function ConfigRow({
  code,
  name,
  onClick,
  right,
  dimmed = false,
}: {
  code: string
  name: string
  onClick: () => void
  right?: ReactNode // 右侧徽章/元信息
  dimmed?: boolean // disabled 项淡显
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={
        'flex items-center gap-3 rounded-xl border border-border bg-surface px-4 py-3 text-left transition-colors hover:border-border-strong hover:bg-surface-2' +
        (dimmed ? ' opacity-55' : '')
      }
    >
      <code className="flex-shrink-0 rounded bg-surface-2 px-1.5 py-0.5 font-mono text-[11.5px] text-muted">
        {code}
      </code>
      <span className="min-w-0 flex-1 truncate text-[13.5px] text-text">{name}</span>
      {right}
    </button>
  )
}
