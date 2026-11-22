import { useCallback, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { restoreScenario } from '@/api/config'
import { Badge } from '@/components/ui/Badge'
import { DeletedBuiltins } from '@/features/config/DeletedBuiltins'
import { ConfigListShell, ConfigRow } from '@/features/config/ConfigListShell'
import { useScenariosPagedList } from '@/features/config/useConfigQueries'
import { ScenarioEditor } from '@/features/scenarios/ScenarioEditor'
import { SCENARIOS_KEY, useDeletedScenarios } from '@/features/scenarios/useScenarioQueries'
import { useSelectedParam, type HistoryOptions } from '@/hooks/useSelectedParam'

// 场景页（/config/scenarios）：列表 + 编辑抽屉。场景是任务所处情境的说明——下发时选定并快照，
// 整份拼在四个智能体系统提示词的末尾。打开的场景写在 ?selected=。

export function ScenariosPage() {
  const list = useScenariosPagedList()
  const [selected, select] = useSelectedParam()
  const [creating, setCreating] = useState(false)
  const qc = useQueryClient()
  const deleted = useDeletedScenarios()

  const closeEditor = useCallback(
    (opts?: HistoryOptions) => {
      if (creating) setCreating(false)
      else select('', opts)
    },
    [creating, select],
  )

  const editing = creating ? null : selected

  return (
    <>
      <ConfigListShell
        onRetry={list.reload}
        title="场景"
        loading={list.loading}
        error={list.error}
        empty={list.rows.length === 0}
        noun="场景"
        emptyHint="点击「新建」添加自定义场景。"
        search={{ value: list.query, onChange: list.setQuery, placeholder: '搜索名称 / 标识 / 说明' }}
        server={{
          page: list.page,
          totalPages: list.totalPages,
          count: list.total,
          onPage: list.setPage,
          size: list.size,
          onSize: list.setSize,
        }}
        onNew={() => setCreating(true)}
        headerExtra={
          <DeletedBuiltins
            noun="场景"
            items={deleted.data ?? []}
            onRestore={async (code) => {
              await restoreScenario(code)
              await qc.invalidateQueries({ queryKey: SCENARIOS_KEY })
            }}
          />
        }
      >
        {list.rows.map((sc) => (
          <ConfigRow
            key={sc.code}
            name={sc.name}
            description={sc.description}
            dimmed={!sc.enabled}
            onClick={() => select(sc.code)}
            right={
              <div className="flex items-center gap-2">
                {sc.is_default && <Badge variant="outline">缺省</Badge>}
                {sc.is_builtin ? <Badge variant="outline">内置</Badge> : <Badge variant="outline">自建</Badge>}
                {sc.customized.length > 0 && <Badge variant="outline">已自定义</Badge>}
                {sc.shadows_builtin && <Badge variant="outline">遮蔽同名内置</Badge>}
                {!sc.enabled && <Badge variant="outline">已停用</Badge>}
              </div>
            }
          />
        ))}
      </ConfigListShell>

      {(creating || selected) && (
        <ScenarioEditor key={creating ? '\u0000new' : selected} code={editing} onClose={closeEditor} />
      )}
    </>
  )
}
