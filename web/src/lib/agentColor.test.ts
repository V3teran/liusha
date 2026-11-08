import { describe, expect, it } from 'vitest'
import { agentAccent, agentLabel } from './agentColor'

describe('agentAccent', () => {
  it('已知 agent 返回固定分类色 token', () => {
    expect(agentAccent('planner').accent).toBe('var(--cat-violet)')
    expect(agentAccent('executor').accent).toBe('var(--cat-emerald)')
    expect(agentAccent('evaluator').accent).toBe('var(--cat-sky)')
    expect(agentAccent('monitor').accent).toBe('var(--cat-pink)')
    expect(agentAccent('assistant').accent).toBe('var(--cat-neutral)')
  })

  it('空名返回中性灰', () => {
    expect(agentAccent().accent).toBe('var(--cat-neutral)')
    expect(agentAccent('').accent).toBe('var(--cat-neutral)')
    expect(agentAccent('   ').accent).toBe('var(--cat-neutral)')
  })

  it('未知 agent 用稳定哈希取色（同名总是同色）', () => {
    const a = agentAccent('some-new-agent')
    const b = agentAccent('some-new-agent')
    expect(a.accent).toBe(b.accent)
    expect(a.soft).toBe(b.soft)
  })

  it('不同未知 agent 可能取到不同颜色（分布在备选色盘内）', () => {
    const palette = ['pink', 'cyan', 'lime', 'orange', 'violet'].map((h) => `var(--cat-${h})`)
    const c = agentAccent('unknown-xyz').accent
    expect(palette).toContain(c)
  })
})

describe('agentLabel', () => {
  it('已知 agent 返回中文标签', () => {
    expect(agentLabel('planner')).toBe('规划')
    expect(agentLabel('executor')).toBe('执行')
    expect(agentLabel('evaluator')).toBe('裁决')
    expect(agentLabel('monitor')).toBe('监察')
    expect(agentLabel('assistant')).toBe('助手')
  })

  it('未知 agent 原样返回', () => {
    expect(agentLabel('custom-agent')).toBe('custom-agent')
  })

  it('空名返回空串', () => {
    expect(agentLabel()).toBe('')
    expect(agentLabel('')).toBe('')
  })
})
