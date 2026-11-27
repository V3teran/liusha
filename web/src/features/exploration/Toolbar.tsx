/**
 * 探索图工具栏：任务选择 + 刷新 + kind 计数 chips（点击即筛选，一物三用：
 * 计数即统计、色点即图例、点击即过滤）+ 状态筛选（只列这张图里有的状态）+ 总量。
 */

import type { ReactNode } from 'react'
import { RefreshCw } from 'lucide-react'
import { cn } from '@/lib/utils'
import { listTasks } from '@/api/tasks'
import { EntityPicker, type PickerResult } from '@/components/ui/EntityPicker'
import { shortDateTime } from '@/lib/format'
import { taskStatusMeta } from '@/lib/taskStatus'
import { KIND_ORDER, KIND_PROFILES, stateOptions } from './kindTheme'
import type { GraphStats } from './layout'
import { Select } from '@/components/ui/Select'
import { FilterBar } from '@/components/FilterBar'
import { Button } from '@/components/ui/Button'

/** 一次列出的任务候选上限：任务持续增长，靠关键词缩小范围。 */
const TASK_OPTION_LIMIT = 20

// 筛选胶囊（节点种类）：激活态 = 淡底 + 同色描边，文字保持中性色——小号彩字在浅色主题保不住对比度
const CHIP = 'inline-flex h-8 items-center gap-1.5 rounded-lg border px-2.5 text-xs transition-colors'
const CHIP_IDLE = 'border-border bg-surface text-text-secondary hover:bg-surface-2 hover:text-text'

/**
 * 任务候选：按目标或批次名由服务端检索（与任务页搜索同一口径），最新创建的在前；
 * 次要信息是创建时间与状态，区分目标相同的任务。
 */
async function fetchTaskOptions(q: string): Promise<PickerResult> {
  const { items, total } = await listTasks(1, TASK_OPTION_LIMIT, q ? { search: q } : undefined)
  return {
    options: items.map((t) => ({
      value: t.id,
      label: t.goal,
      detail: `${shortDateTime(t.created_at)} 创建 · ${taskStatusMeta(t.status).label}`,
    })),
    hasMore: total > items.length,
  }
}

export function Toolbar({
  scopeId,
  scopeLabel,
  onTaskChange,
  scopeJumps,
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
  /** 当前范围（任务或对话）的 ID 与显示名（「任务：目标」/「对话：标题」） */
  scopeId: string
  scopeLabel?: string
  onTaskChange: (id: string) => void
  /** 当前任务的跳转（任务、对话、发现、模型调用）：紧跟在选择框后，属于选中的任务而不是整个页面 */
  scopeJumps?: ReactNode
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
      canReset={filterKind !== 'all' || filterState !== 'all'}
      onReset={() => {
        onFilterKind('all')
        onFilterState('all')
      }}
      trailing={
        showFilters ? (
          <span className="whitespace-nowrap font-mono text-xs text-text-tertiary">
            {stats.total} 节点 · {stats.edgeCount} 边
          </span>
        ) : undefined
      }
    >
      <EntityPicker
        value={scopeId}
        valueLabel={scopeLabel}
        onChange={onTaskChange}
        fetchOptions={fetchTaskOptions}
        queryKey={['exploration', 'task-options']}
        emptyLabel="选择任务"
        searchPlaceholder="搜索任务目标、批次名"
        ariaLabel="选择任务"
        className="w-full sm:w-72"
      />
      {scopeJumps}

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
                  className={cn(CHIP, active ? 'font-semibold' : CHIP_IDLE)}
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
            {stateOptions(stats.byState, filterState).map((option) => (
              <option key={option.value} value={option.value}>
                {option.label}（{option.count}）
              </option>
            ))}
          </Select>
        </>
      )}
    </FilterBar>
  )
}
