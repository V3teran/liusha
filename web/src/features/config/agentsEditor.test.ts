import { describe, expect, it } from 'vitest'
import type { AgentDetail } from '@/api/types'
import { draftOf, patchOf, rebaseDraft, type Catalogs } from './agentsEditor'

const catalogs: Catalogs = { function_tools: ['a', 'b'], cli_tools: ['nmap', 'nuclei'], skills: ['bac'] }

function detail(): AgentDetail {
  const agent = {
    code: 'planner',
    name: '规划者',
    charter: '章程',
    description: '摘要',
    function_tools: ['a'],
    cli_tools: ['*'],
    skills: [],
    max_iterations: 50,
    complexity: 'complex',
  }
  const { code: _c, name: _n, ...defaults } = agent
  return { agent, defaults, overrides: {}, prompt_archive: null, contract: '' }
}

describe('agentsEditor', () => {
  it('draftOf：「全部」按目录展开，挑选模式即生效名单', () => {
    const d = draftOf(detail(), catalogs)
    expect(d.cli_tools).toEqual({ all: true, checked: ['nmap', 'nuclei'] })
    expect(d.function_tools).toEqual({ all: false, checked: ['a'] })
  })

  it('patchOf：只含改动字段；名单为 {on, off}', () => {
    const base = draftOf(detail(), catalogs)
    const draft = { ...base, charter: '新章程', function_tools: { all: false, checked: ['a', 'b'] } }
    expect(patchOf(base, draft, catalogs)).toEqual({ charter: '新章程', function_tools: { on: ['b'], off: [] } })
    expect(patchOf(base, base, catalogs)).toEqual({})
  })

  it('rebaseDraft：恢复某项后以新基线为底，保留其它字段未保存的编辑', () => {
    const oldBase = draftOf(detail(), catalogs)
    const draft = { ...oldBase, description: '编辑中', complexity: 'simple' }
    const newBase = { ...oldBase, complexity: 'medium' }
    const merged = rebaseDraft(oldBase, draft, newBase, 'complexity')
    expect(merged.description).toBe('编辑中')
    expect(merged.complexity).toBe('medium')
  })
})
