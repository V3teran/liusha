/**
 * 探索图工具栏：对话选择 + 刷新 + kind 计数 chips（点击即筛选，一物三用：
 * 计数即统计、色点即图例、点击即过滤）+ 状态筛选 + 总量。
 */

import { RefreshCw } from 'lucide-react'
import { cn } from '@/lib/utils'
import { listConversations } from '@/api/conversations'
import { EntityPicker, type PickerResult } from '@/components/ui/EntityPicker'
import { shortDateTime } from '@/lib/format'
import { sourceLabel } from '@/lib/source'
import { KIND_ORDER, KIND_PROFILES, STATE_META } from './kindTheme'
import type { GraphStats } from './layout'
import { Select } from '@/components/ui/Select'
import { FilterBar } from '@/components/FilterBar'
import { Button } from '@/components/ui/Button'

/**
 * 对话候选：按标题、所属任务的目标或 ID 前缀由服务端检索、限量（对话会持续增长，不一次列全）；
 * 次要信息是最近更新时间与来源，区分同名对话。
 */
async function fetchConversationOptions(q: string): Promise<PickerResult> {
  const { conversations, hasMore } = await listConversations({ search: q, limit: 20 })
  return {
    options: conversations.map((c) => ({
      value: c.id,
      label: c.title || c.id,
      detail: `${shortDateTime(c.updated_at)} 更新 · ${sourceLabel(c.source || 'direct')}`,
    })),
    hasMore,
  }
}

export function Toolbar({
  conversationId,
  conversationLabel,
  onConversationChange,
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
  conversationId: string
  conversationLabel?: string
  onConversationChange: (id: string) => void
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
      <EntityPicker
        value={conversationId}
        valueLabel={conversationLabel}
        onChange={onConversationChange}
        fetchOptions={fetchConversationOptions}
        queryKey={['exploration', 'conversation-options']}
        emptyLabel="选择对话"
        searchPlaceholder="搜索对话标题、任务目标、ID"
        ariaLabel="选择对话"
        className="w-full sm:w-72"
      />

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
