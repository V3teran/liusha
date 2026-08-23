import { describe, expect, it } from 'vitest'
import { agentAccent, agentLabel } from './agentColor'

describe('agentAccent', () => {
  it('已知 agent 返回固定配色', () => {
    expect(agentAccent('planner').accent).toBe('#a78bfa')
    expect(agentAccent('reconnaissance').accent).toBe('#38bdf8')
    expect(agentAccent('exploitation').accent).toBe('#34d399')
    expect(agentAccent('traffic-analysis').accent).toBe('#818cf8')
  })

  it('空名返回中性灰', () => {
    expect(agentAccent().accent).toBe('#8c8c8c')
    expect(agentAccent('').accent).toBe('#8c8c8c')
    expect(agentAccent('   ').accent).toBe('#8c8c8c')
  })

  it('未知 agent 用稳定哈希取色（同名总是同色）', () => {
    const a = agentAccent('some-new-agent')
    const b = agentAccent('some-new-agent')
    expect(a.accent).toBe(b.accent)
    expect(a.soft).toBe(b.soft)
  })

  it('不同未知 agent 可能取到不同颜色（分布在备选色盘内）', () => {
    const palette = ['#f472b6', '#22d3ee', '#a3e635', '#fb923c', '#c084fc']
    const c = agentAccent('unknown-xyz').accent
    expect(palette).toContain(c)
  })
})

describe('agentLabel', () => {
  it('已知 agent 返回中文标签', () => {
    expect(agentLabel('planner')).toBe('编排')
    expect(agentLabel('reconnaissance')).toBe('侦察')
    expect(agentLabel('exploitation')).toBe('利用')
    expect(agentLabel('traffic-analysis')).toBe('流量分析')
  })

  it('未知 agent 原样返回', () => {
    expect(agentLabel('custom-agent')).toBe('custom-agent')
  })

  it('空名返回空串', () => {
    expect(agentLabel()).toBe('')
    expect(agentLabel('')).toBe('')
  })
})
