import { useMemo, useState } from 'react'
import { Plus, Search } from 'lucide-react'
import type { ProviderConfig } from '@/api/types'
import { Badge } from '@/components/ui/badge'
import { cn } from '@/lib/utils'
import { TYPE_LABEL } from './providerForm'

interface ProviderListPanelProps {
  providers: ProviderConfig[]
  loading: boolean
  error: string
  selectedKey: string // 当前选中 provider 的 key；新建态传 '__new__'
  onSelect: (p: ProviderConfig) => void
  onNew: () => void
}

// 左列表面板：搜索 + 新建 + provider 行集合，量小（部署通常几个到十几个）故本地过滤，
// 不接服务端分页——对齐 AssignmentView/ConversationList 这类小数据量列表的做法，
// 而非套用 Scenario/Agent 那套面向大量条目的服务端翻页 ConfigListShell。
export function ProviderListPanel({
  providers,
  loading,
  error,
  selectedKey,
  onSelect,
  onNew,
}: ProviderListPanelProps) {
  const [query, setQuery] = useState('')

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase()
    if (!q) return providers
    return providers.filter(
      (p) => p.key.toLowerCase().includes(q) || p.default_model.toLowerCase().includes(q),
    )
  }, [providers, query])

  return (
    <div className="flex h-full min-h-0 flex-col border-r border-border">
      <header className="flex flex-shrink-0 items-center justify-between gap-3 px-4 py-3 pt-4">
        <div className="min-w-0">
          <h1 className="tac-prompt font-mono text-[15px] font-semibold text-text">模型部署</h1>
          <p className="mt-0.5 text-[12px] text-muted">provider 连接参数与能力标志</p>
        </div>
        <button
          type="button"
          onClick={onNew}
          aria-label="新建 provider"
          title="新建 provider"
          className="flex h-8 w-8 flex-shrink-0 items-center justify-center rounded-lg bg-accent text-white transition-all hover:bg-accent-hover hover:shadow-[var(--glow-accent-strong)]"
        >
          <Plus className="h-4 w-4" />
        </button>
      </header>

      <div className="flex-shrink-0 px-4 pb-3">
        <div className="relative">
          <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-faint" />
          <input
            type="search"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="搜索标识键 / 模型名"
            aria-label="搜索 provider"
            spellCheck={false}
            className="w-full rounded-lg border border-border bg-surface-2 py-1.5 pl-8 pr-2.5 text-[12.5px] text-text outline-none placeholder:text-faint focus:border-accent"
          />
        </div>
      </div>

      <ul className="flex min-h-0 flex-1 flex-col gap-0.5 overflow-y-auto px-2 pb-2">
        {loading ? (
          <li className="tac-cursor py-10 text-center font-mono text-[13px] text-muted">加载中</li>
        ) : error ? (
          <li className="py-10 text-center text-[13px] text-sev-critical">⚠ {error}</li>
        ) : filtered.length === 0 ? (
          <li className="py-10 text-center text-[13px] text-muted">
            {query.trim() ? '无匹配部署' : '暂无部署——点右上「+」接入第一个 provider'}
          </li>
        ) : (
          filtered.map((p) => (
            <li key={p.key}>
              <button
                type="button"
                onClick={() => onSelect(p)}
                className={cn(
                  'flex w-full flex-col gap-1 rounded-lg px-3 py-2.5 text-left transition-colors hover:bg-surface-2',
                  p.key === selectedKey && 'bg-surface-2 shadow-[inset_2px_0_0_var(--accent)]',
                  !p.enabled && 'opacity-55',
                )}
              >
                <div className="flex items-center justify-between gap-2">
                  <span className="flex min-w-0 items-center gap-1.5">
                    {!p.key_present && (
                      <span
                        aria-label="密钥未注入"
                        title="密钥未注入，调用会失败"
                        className="h-1.5 w-1.5 flex-shrink-0 rounded-full bg-sev-high"
                      />
                    )}
                    <span className="min-w-0 truncate text-[13.5px] font-medium text-text">{p.key}</span>
                  </span>
                  <Badge variant="outline" className="flex-shrink-0">
                    {TYPE_LABEL[p.type]}
                  </Badge>
                </div>
                <span className="truncate font-mono text-[11.5px] text-muted">{p.default_model}</span>
              </button>
            </li>
          ))
        )}
      </ul>
    </div>
  )
}
