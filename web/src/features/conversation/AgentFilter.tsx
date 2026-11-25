import { Wrench } from 'lucide-react'
import { FilterBar } from '@/components/FilterBar'
import { agentProfile } from '@/lib/agentColor'
import { cn } from '@/lib/utils'
import type { AgentActivity } from './threadRows'

interface AgentFilterProps {
  agents: AgentActivity[]
  /** 选中的智能体；一个都没选 = 全部 */
  value: readonly string[]
  onChange: (names: string[]) => void
}

/**
 * 按智能体筛选时间线：每个智能体一枚身份胶囊（图标与颜色同它的卡片头像），可以多选——选中的
 * 留在时间线上，其余智能体的卡隐去。胶囊上的两个数是它在时间线上的条目数和工具调用数。
 * 条目数与胶囊用同一份身份档案，认人和筛选是同一套视觉。
 */
export function AgentFilter({ agents, value, onChange }: AgentFilterProps) {
  return (
    <FilterBar
      // 两个数的含义写在区域名里：胶囊自己的名字就是屏幕上的文字（语音控制照着念选得中）
      label="按智能体筛选（可多选；每个胶囊上依次是条目数和工具调用数）"
      canReset={value.length > 0}
      onReset={() => onChange([])}
      className="px-4 py-2"
    >
      {agents.map((a) => {
        const profile = agentProfile(a.name)
        const Icon = profile.icon
        const active = value.includes(a.name)
        return (
          <button
            key={a.name}
            type="button"
            onClick={() => onChange(active ? value.filter((name) => name !== a.name) : [...value, a.name])}
            aria-pressed={active}
            title={`${profile.label}：${a.count} 条，工具调用 ${a.toolCalls} 次`}
            className={cn(
              'inline-flex h-8 items-center gap-1.5 rounded-lg border px-2.5 text-xs transition-colors',
              active ? 'font-semibold' : 'border-border bg-surface text-text-secondary hover:bg-surface-2 hover:text-text',
            )}
            // 激活态 = 淡底 + 同色描边；文字保持中性色——小号彩字在浅色主题保不住对比度
            style={active ? { borderColor: profile.accent + '66', background: profile.accent + '14' } : undefined}
          >
            <Icon aria-hidden className="h-3.5 w-3.5" style={{ color: profile.accent }} />
            {profile.label}
            {/* 各段在视觉上靠间距分开，读屏的名字里补上看不见的文字：两个数才分得清哪个是哪个 */}
            <span className="sr-only"> </span>
            <span className="tabular-nums text-text-tertiary">{a.count}</span>
            <span className="inline-flex items-center gap-0.5 tabular-nums text-text-tertiary">
              <Wrench aria-hidden className="h-3 w-3" />
              <span className="sr-only">，工具调用 </span>
              {a.toolCalls}
            </span>
          </button>
        )
      })}
    </FilterBar>
  )
}
