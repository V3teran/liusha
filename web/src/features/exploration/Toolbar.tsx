/**
 * 探索图工具栏：会话搜索 + 刷新 + kind 计数 chips（点击即筛选，一物三用：
 * 计数即统计、色点即图例、点击即过滤）+ 状态筛选 + 总量。
 */

import { RefreshCw, Search } from 'lucide-react'
import { cn } from '@/lib/utils'
import { KIND_ORDER, KIND_PROFILES, STATE_META } from './kindTheme'
import type { GraphStats } from './layout'
import { Select } from '@/components/ui/Select'
import { FilterBar } from '@/components/FilterBar'
import { Button } from '@/components/ui/Button'
import { controlClass } from '@/components/ui/control'

export function Toolbar({
  conversationInput,
  onConversationInput,
  onConversationSubmit,
  disabled,
  refreshing,
  onRefresh,
  showFilters,
  filterKind,
  onFilterKind,
  filterState,
  onFilterState,
  stats,
}: {
  conversationInput: string
  onConversationInput: (v: string) => void
  onConversationSubmit: () => void
  disabled: boolean
  refreshing: boolean
  onRefresh: () => void
  showFilters: boolean
  filterKind: string
  onFilterKind: (v: string) => void
  filterState: string
  onFilterState: (v: string) => void
  stats: GraphStats
}) {
  return (
    <FilterBar
      label="探索图筛选"
      trailing={
        showFilters ? (
          <span className="whitespace-nowrap font-mono text-xs text-text-tertiary">
            {stats.total} 节点 · {stats.edgeCount} 边
          </span>
        ) : undefined
      }
    >
      <div className="relative">
        <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-text-tertiary" />
        <input
          type="text"
          value={conversationInput}
          onChange={(e) => onConversationInput(e.target.value)}
          onKeyDown={(e) => e.key === 'Enter' && onConversationSubmit()}
          placeholder="输入对话 ID，回车加载"
          aria-label="对话 ID"
          className={controlClass('sm', 'w-72 pl-9 font-mono')}
          disabled={disabled}
        />
      </div>

      <Button
        variant="secondary"
        size="sm"
        onClick={onRefresh}
        disabled={disabled || refreshing}
        title="刷新"
        aria-label="刷新"
        className="w-8 px-0"
      >
        <RefreshCw className={cn(refreshing && 'animate-spin')} />
      </Button>

      {showFilters && (
        <>
          <div className="flex items-center gap-1.5">
            {KIND_ORDER.map((kind) => {
              const profile = KIND_PROFILES[kind]
              const count = stats.byKind[kind] ?? 0
              const active = filterKind === kind
              return (
                <button
                  key={kind}
                  onClick={() => onFilterKind(active ? 'all' : kind)}
                  aria-pressed={active}
                  title={active ? `清除「${profile.label}」筛选` : `只看${profile.label}节点`}
                  className={cn(
                    'inline-flex h-8 items-center gap-1.5 rounded-lg border px-2.5 text-xs transition-all',
                    active ? 'font-semibold' : 'border-border bg-surface text-text-secondary hover:bg-surface-2 hover:text-text',
                  )}
                  // 激活态 = 淡底 + 同色描边 + 色点；文字保持中性色——小号彩字在浅色主题保不住对比度
                  style={active ? { borderColor: profile.accent + '66', background: profile.accent + '14' } : undefined}
                >
                  <span className="h-1.5 w-1.5 rounded-full" style={{ background: profile.accent }} />
                  {count} {profile.label}
                </button>
              )
            })}
          </div>

          <Select
            value={filterState}
            onChange={(e) => onFilterState(e.target.value)}
            size="sm"
            aria-label="按状态筛选节点"
          >
            <option value="all">全部状态</option>
            {Object.entries(STATE_META).map(([value, meta]) => (
              <option key={value} value={value}>
                {meta.label}
              </option>
            ))}
          </Select>
        </>
      )}
    </FilterBar>
  )
}
