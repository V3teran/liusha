// AgentAssignSection - 「装配到智能体」胶囊区块（工具/技能详情共用）：
// 列出全量智能体，已装配（involved）者为选中态，点击即装/卸；写入中的胶囊转圈
// 并禁用防重复点。装配态由调用方随详情下发（后端单一权威），本组件不自算。
import type { ToolAgent } from '@/api/types'
import { ToggleChip } from '@/components/ui/ToggleChip'

export function AgentAssignSection({
  agents,
  savingCode,
  onToggle,
}: {
  agents: ToolAgent[]
  savingCode: string | null
  onToggle: (code: string, next: boolean) => void
}) {
  return (
    <section>
      <h3 className="mb-2 text-xs font-medium text-text-secondary">装配到智能体</h3>
      {agents.length === 0 ? (
        <p className="text-sm text-text-secondary">暂无智能体</p>
      ) : (
        <div className="flex flex-wrap gap-2">
          {agents.map((a) => {
            const saving = savingCode === a.code
            return (
              <ToggleChip key={a.code} pressed={a.involved} busy={saving} onClick={() => onToggle(a.code, !a.involved)}>
                {a.name}
              </ToggleChip>
            )
          })}
        </div>
      )}
    </section>
  )
}
