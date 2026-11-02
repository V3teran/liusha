import { describe, expect, it } from 'vitest'
import { nodeTitle, nodeChips } from './kindTheme'
import type { GraphNode } from '@/api/explorationgraph'

function node(o: Partial<GraphNode> = {}): GraphNode {
  return {
    id: 'n-obj',
    task_id: 't-1',
    kind: 'objective',
    content: {},
    source_type: 'user',
    source_id: 'u',
    created_at: '2026-06-10T00:00:00Z',
    updated_at: '2026-06-10T00:00:00Z',
    ...o,
  }
}

describe('kindTheme（kind 视觉档案与内容提取）', () => {
  it('nodeTitle 按 kind 提取主文本', () => {
    expect(nodeTitle(node({ kind: 'objective', content: { description: '攻破 DVWA' } }))).toBe('攻破 DVWA')
    expect(nodeTitle(node({ id: 'n-act', kind: 'action', content: { instruction: '扫描登录页' } }))).toBe('扫描登录页')
    expect(nodeTitle(node({ id: 'n-obs', kind: 'observation', content: { statement: '发现弱口令' } }))).toBe('发现弱口令')
    expect(nodeTitle(node({ id: 'n-res', kind: 'result', content: { summary: 'SQLi 已坐实' } }))).toBe('SQLi 已坐实')
  })

  it('nodeTitle 主字段缺失时逐级回落，content 非对象时返回空串', () => {
    expect(nodeTitle(node({ kind: 'objective', content: { title: '回退标题' } }))).toBe('回退标题')
    expect(nodeTitle(node({ kind: 'objective', content: null }))).toBe('')
    expect(nodeTitle(node({ kind: 'action', content: '纯字符串载荷' }))).toBe('')
  })

  it('nodeChips：action 状态/类型/P0-P1 优先级上卡', () => {
    const chips = nodeChips(
      node({ id: 'n-act', kind: 'action', state: 'running', priority: 'high', content: { type: 'reconnaissance' } }),
    )
    expect(chips.state?.label).toBe('执行中')
    expect(chips.category).toBe('侦察')
    expect(chips.priority?.code).toBe('P1')
    expect(chips.severity).toBeUndefined()
    expect(chips.confidence).toBeUndefined()
  })

  it('nodeChips：observation 分类/等级/置信度，P2 优先级不上卡', () => {
    const chips = nodeChips(
      node({
        id: 'n-obs',
        kind: 'observation',
        confidence: 'verified',
        priority: 'medium',
        content: { category: 'issue', severity: 'HIGH' },
      }),
    )
    expect(chips.category).toBe('问题')
    expect(chips.severity).toBe('high') // 大写归一
    expect(chips.confidence).toBe('已验证')
    expect(chips.priority).toBeUndefined()
  })
})
