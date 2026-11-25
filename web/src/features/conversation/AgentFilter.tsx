import { FilterBar } from '@/components/FilterBar'
import { agentProfile } from '@/lib/agentColor'
import { cn } from '@/lib/utils'
import type { AgentActivity } from './threadRows'

interface AgentFilterProps {
  agents: AgentActivity[]
  /** 选中的智能体；null = 全部 */
  value: string | null
  onChange: (name: string | null) => void
}

/**
 * 按智能体筛选时间线：每个智能体一枚身份胶囊（图标与颜色同它的卡片头像），后面的数字是它在
 * 时间线上的条目数。点一枚只看它，再点一次回到全部。条目数与胶囊用同一份身份档案，
 * 认人和筛选是同一套视觉。
 */
export function AgentFilter({ agents, value, onChange }: AgentFilterProps) {
  return (
    <FilterBar
      label="按智能体筛选"
      canReset={value !== null}
      onReset={() => onChange(null)}
      className="px-4 py-2"
    >
      {agents.map((a) => {
        const profile = agentProfile(a.name)
        const Icon = profile.icon
        const active = value === a.name
        return (
          <button
            key={a.name}
            type="button"
            onClick={() => onChange(active ? null : a.name)}
            aria-pressed={active}
            title={active ? `清除「${profile.label}」筛选` : `只看${profile.label}`}
            className={cn(
              'inline-flex h-8 items-center gap-1.5 rounded-lg border px-2.5 text-xs transition-colors',
              active ? 'font-semibold' : 'border-border bg-surface text-text-secondary hover:bg-surface-2 hover:text-text',
            )}
            // 激活态 = 淡底 + 同色描边；文字保持中性色——小号彩字在浅色主题保不住对比度
            style={active ? { borderColor: profile.accent + '66', background: profile.accent + '14' } : undefined}
          >
            <Icon aria-hidden className="h-3.5 w-3.5" style={{ color: profile.accent }} />
            {profile.label}
            <span className="tabular-nums text-text-tertiary">{a.count}</span>
          </button>
        )
      })}
    </FilterBar>
  )
}
