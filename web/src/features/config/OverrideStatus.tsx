import { useState } from 'react'
import type { FieldOverride } from '@/api/types'
import { hunks, lineDiff } from '@/lib/lineDiff'
import { staleOf } from '@/lib/override'

// 字段的自定义状态条：渲染在字段控件下方，说明「这一项是否偏离出厂、出厂是否已更新」，
// 并提供对比与恢复默认。未自定义的字段不渲染任何东西——出厂即默认，不加噪音。
//
// 对比的重点是「从你修改时到现在，出厂改了什么」：用户据此决定合并出厂的改进，
// 还是继续用自己的版本。基线未知（升级前的修改）时只能对比当前出厂与你的版本。

interface OverrideStatusProps {
  state?: FieldOverride
  current: unknown // 当前生效值（已保存的）
  onReset?: () => void
  busy?: boolean
  long?: boolean // 长文本：对比以逐行差异呈现
  format?: (v: unknown) => string
}

const show = (v: unknown) => (Array.isArray(v) ? v.join(', ') || '（空）' : String(v ?? ''))

export function OverrideStatus({ state, current, onReset, busy = false, long = false, format = show }: OverrideStatusProps) {
  const [comparing, setComparing] = useState(false)
  if (!state) return null

  const isSet = state.kind === 'set'
  const stale = staleOf(state)
  const unknownBaseline = !isSet && state.baseline === null

  return (
    <div className="flex flex-col gap-2 rounded-md border border-accent/25 bg-accent/5 px-3 py-2 text-xs">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
        <span className="font-semibold text-accent">已自定义</span>
        {isSet && (
          <span className="text-text-secondary">
            相对出厂新增 {state.added?.length ?? 0} 项 · 删除 {state.removed?.length ?? 0} 项
          </span>
        )}
        {unknownBaseline && <span className="text-text-secondary">升级前的修改</span>}
        {state.default_changed && <span className="font-semibold text-status-warning">出厂已更新</span>}
        {state.matches_default && <span className="text-text-secondary">当前与出厂相同</span>}
        {stale.length > 0 && <span className="text-status-warning">已失效：{stale.join('、')}</span>}
        <span className="ml-auto flex gap-2">
          {!isSet && (
            <button
              type="button"
              className="text-text-secondary underline-offset-2 hover:text-accent hover:underline"
              aria-expanded={comparing}
              onClick={() => setComparing((c) => !c)}
            >
              {comparing ? '收起对比' : '对比出厂'}
            </button>
          )}
          {onReset && (
            <button
              type="button"
              disabled={busy}
              className="text-text-secondary underline-offset-2 hover:text-accent hover:underline disabled:opacity-50"
              onClick={onReset}
            >
              {busy ? '恢复中…' : '恢复默认'}
            </button>
          )}
        </span>
      </div>
      {comparing && !isSet && (
        <Compare state={state} current={current} long={long} format={format} />
      )}
    </div>
  )
}

function Compare({
  state,
  current,
  long,
  format,
}: {
  state: FieldOverride
  current: unknown
  long: boolean
  format: (v: unknown) => string
}) {
  if (!long) {
    return (
      <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-text-secondary">
        {state.default_changed && (
          <>
            <dt>你修改时的出厂值</dt>
            <dd className="font-mono text-text">{format(state.baseline)}</dd>
          </>
        )}
        <dt>当前出厂值</dt>
        <dd className="font-mono text-text">{format(state.default)}</dd>
        <dt>你的值</dt>
        <dd className="font-mono text-text">{format(current)}</dd>
      </dl>
    )
  }
  const def = String(state.default ?? '')
  return (
    <div className="flex flex-col gap-3">
      {state.default_changed && (
        <DiffBlock title="出厂的变化（你修改时 → 现在）" before={String(state.baseline ?? '')} after={def} />
      )}
      <DiffBlock title="你的版本相对当前出厂" before={def} after={String(current ?? '')} />
    </div>
  )
}

function DiffBlock({ title, before, after }: { title: string; before: string; after: string }) {
  const lines = hunks(lineDiff(before, after))
  return (
    <section aria-label={title}>
      <p className="mb-1 font-semibold text-text-secondary">{title}</p>
      {lines.length === 0 ? (
        <p className="text-text-tertiary">无差异</p>
      ) : (
        <pre className="max-h-72 overflow-auto rounded border border-border bg-background p-2 font-mono text-xs leading-5">
          {lines.map((l, i) =>
            l === null ? (
              <div key={i} className="text-text-tertiary">
                ⋯
              </div>
            ) : (
              <div
                key={i}
                className={
                  l.kind === 'add'
                    ? 'bg-status-success/10 text-status-success'
                    : l.kind === 'del'
                      ? 'bg-status-error/10 text-status-error line-through decoration-status-error/40'
                      : 'text-text-secondary'
                }
              >
                {l.kind === 'add' ? '+ ' : l.kind === 'del' ? '− ' : '  '}
                {l.text}
              </div>
            ),
          )}
        </pre>
      )}
    </section>
  )
}
