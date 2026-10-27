// AgentAssignSection - 「装配到智能体」胶囊区块（工具/技能详情共用）：
// 列出全量智能体，已装配（involved）者 accent 高亮，点击即装/卸；saving 集合中的
// 胶囊禁用防重复点。装配态由调用方随详情下发（后端单一权威），本组件不自算。
import type { ToolAgent } from '@/api/types'

export function AgentAssignSection({
  agents,
  savingCode,
  onToggle,
}: {
  agents: ToolAgent[]
  savingCode: string | null
  onToggle: (code: string, next: boolean) => void
}) {
  const involvedCount = agents.filter((a) => a.involved).length

  return (
    <section>
      <p className="mb-2 flex items-baseline gap-1.5 text-xs font-medium text-muted">
        装配到智能体
        <span className="font-normal text-faint">
          已装配 {involvedCount}/{agents.length}
        </span>
        <span className="font-normal text-faint">· 点击胶囊装/卸</span>
      </p>
      {agents.length === 0 ? (
        <p className="text-sm text-muted">暂无智能体</p>
      ) : (
        <div className="flex flex-wrap gap-2">
          {agents.map((a) => {
            const saving = savingCode === a.code
            return (
              <button
                key={a.code}
                type="button"
                disabled={saving}
                aria-pressed={a.involved}
                onClick={() => onToggle(a.code, !a.involved)}
                className={
                  'rounded-md border px-2.5 py-1 text-sm transition-colors disabled:opacity-50 ' +
                  (a.involved
                    ? 'border-accent bg-accent/15 text-accent'
                    : 'border-border bg-background text-muted hover:border-accent/50 hover:text-text')
                }
              >
                {a.name}
              </button>
            )
          })}
        </div>
      )}
    </section>
  )
}
