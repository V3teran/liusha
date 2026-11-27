/**
 * 探索图工具栏：任务选择 + 刷新 + kind 计数 chips（点击即筛选，一物三用：
 * 计数即统计、色点即图例、点击即过滤）+「只看方向关系」开关 + 状态筛选 + 总量。
 */

import type { ReactNode } from 'react'
import { RefreshCw } from 'lucide-react'
import { cn } from '@/lib/utils'
import { listTasks } from '@/api/tasks'
import { EntityPicker, type PickerResult } from '@/components/ui/EntityPicker'
import { shortDateTime } from '@/lib/format'
import { taskStatusMeta } from '@/lib/taskStatus'
import { KIND_ORDER, KIND_PROFILES, STATE_META } from './kindTheme'
import { EDGE_LOOKS, type GraphStats } from './layout'
import { Select } from '@/components/ui/Select'
import { FilterBar } from '@/components/FilterBar'
import { Button } from '@/components/ui/Button'

/** 一次列出的任务候选上限：任务持续增长，靠关键词缩小范围。 */
const TASK_OPTION_LIMIT = 20

// 筛选胶囊（节点种类、方向关系）：激活态 = 淡底 + 同色描边，文字保持中性色——小号彩字在浅色主题保不住对比度
const CHIP = 'inline-flex h-8 items-center gap-1.5 rounded-lg border px-2.5 text-xs transition-colors'
const CHIP_IDLE = 'border-border bg-surface text-text-secondary hover:bg-surface-2 hover:text-text'
const activeTint = (color: string) => ({ borderColor: color + '66', background: color + '14' })

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
  directionEdgeCount,
  directionOnly,
  onDirectionOnly,
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
  /** 滋生与吸收两种边的条数（全量）：没有就不给「只看方向关系」的入口 */
  directionEdgeCount: number
  /** 只看方向关系：收缩到滋生、吸收两种边及它们两端的节点 */
  directionOnly: boolean
  onDirectionOnly: (v: boolean) => void
  stats: GraphStats
}) {
  return (
    <FilterBar
      label="探索图筛选"
      canReset={filterKind !== 'all' || filterState !== 'all' || directionOnly}
      onReset={() => {
        onFilterKind('all')
        onFilterState('all')
        onDirectionOnly(false)
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
            {/* 这两种边一次结果分析才添几条，整图里淹没在推导链与归属的扇形中；收起来才看得全。
                样本线即图例：琥珀短虚线是滋生，紫色长虚线是吸收 */}
            {directionEdgeCount > 0 && (
              <button
                type="button"
                onClick={() => onDirectionOnly(!directionOnly)}
                aria-pressed={directionOnly}
                title={directionOnly ? '回到整张图' : '只看结果催生方向、方向之间吸收的关系'}
                className={cn(CHIP, directionOnly ? 'font-semibold' : CHIP_IDLE)}
                style={directionOnly ? activeTint(EDGE_LOOKS.triggers.color) : undefined}
              >
                <span aria-hidden className="flex flex-col gap-[3px]">
                  {(['triggers', 'supersedes'] as const).map((look) => (
                    <svg key={look} width="18" height="3">
                      <line
                        x1="0"
                        y1="1.5"
                        x2="18"
                        y2="1.5"
                        stroke={EDGE_LOOKS[look].color}
                        strokeWidth={EDGE_LOOKS[look].width}
                        strokeDasharray={EDGE_LOOKS[look].dash}
                      />
                    </svg>
                  ))}
                </span>
                滋生 · 吸收
              </button>
            )}
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
