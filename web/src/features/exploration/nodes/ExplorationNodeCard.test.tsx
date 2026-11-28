import { describe, expect, it, vi } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import type { NodeProps } from '@xyflow/react'
import type { GraphNode, LeadSpec } from '@/api/explorationgraph'
import type { ExplorationFlowNode, ExplorationNodeData } from '../layout'
import { LEAD_ACCENT } from '../kindTheme'

// 连线端点要在 React Flow 的节点上下文里才能渲染，这里只看卡片本身
vi.mock('@xyflow/react', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@xyflow/react')>()
  return { ...actual, Handle: () => null }
})

import { ExplorationNodeCard } from './ExplorationNodeCard'

const SPEC: LeadSpec = { next: 'n', question: 'q', potential: 'p' }

const fact = (o: Partial<GraphNode> = {}): GraphNode =>
  ({
    id: 'n-1',
    task_id: 't-1',
    kind: 'observation',
    content: { statement: '后台数据库为 MariaDB 10.11.6' },
    source_type: 'executor',
    created_at: '2026-06-10T00:00:00Z',
    updated_at: '2026-06-10T00:00:00Z',
    ...o,
  }) as GraphNode

function renderCard(node: GraphNode, data: Partial<ExplorationNodeData> = {}) {
  const props = { data: { node, ...data } } as unknown as NodeProps<ExplorationFlowNode>
  const { container } = render(<ExplorationNodeCard {...props} />)
  return { card: container.querySelector('.exp-card') as HTMLElement, container }
}

describe('ExplorationNodeCard 的线索标记', () => {
  it('开着的线索：卡片四边加描边，脚印骑在卡片外面的右上角（卡片裁切溢出），头部给它让位', () => {
    const { card, container } = renderCard(fact({ lead: SPEC }))

    expect(card.classList.contains('exp-card-lead')).toBe(true)
    expect(card.style.getPropertyValue('--exp-lead').trim()).toBe(LEAD_ACCENT)
    const mark = container.querySelector('.exp-lead-mark') as HTMLElement
    expect(mark).not.toBeNull()
    expect(card.contains(mark)).toBe(false)
    expect(mark.previousElementSibling).toBe(card)
    expect(mark.querySelector('.exp-lead-mark, .lucide-flag')).toBeNull()
    expect(card.firstElementChild?.className).toContain('pr-9')
    expect(screen.queryByText('线索已关闭')).toBeNull()
  })

  it('线索不靠给卡片染色：底色与同类别的普通观测一模一样', () => {
    const lead = renderCard(fact({ lead: SPEC })).card
    const plain = renderCard(fact()).card
    expect(lead.style.background).toBe(plain.style.background)
    expect(lead.style.background).not.toContain(LEAD_ACCENT)
  })

  it('已关闭且被认领过的线索（图上有 pursues 边）：脚印与描边都换正文色——这条路走过了', () => {
    const { card, container } = renderCard(
      fact({ lead: SPEC, lead_closed: { kind: 'exhausted', reason: '之前的动作已做过' } }),
      { leadFollowed: true },
    )

    expect(card.classList.contains('exp-card-lead')).toBe(false)
    expect(card.classList.contains('exp-card-lead-followed')).toBe(true)
    expect(card.style.getPropertyValue('--exp-lead').trim()).toBe('var(--text)')
    const mark = container.querySelector('.exp-lead-mark') as HTMLElement
    expect(mark).not.toBeNull()
    expect(card.contains(mark)).toBe(false)
    expect(mark.classList.contains('text-text-secondary')).toBe(true)
    const icon = mark.querySelector('svg') as SVGElement
    expect(icon.style.color).toBe('')
    expect(card.firstElementChild?.className).toContain('pr-9')
    expect(screen.queryByText('线索已关闭')).toBeNull()
    expect(mark.getAttribute('title')).toContain('已走过的线索')
    expect(mark.getAttribute('title')).toContain('之前的动作已做过')
  })

  it('已关闭但没有认领边的线索：什么标记都没有——图上看不见因果的「走过」不算走过', () => {
    const { card, container } = renderCard(
      fact({ lead: SPEC, lead_closed: { kind: 'exhausted', reason: '之前的动作已做过' } }),
    )

    expect(card.classList.contains('exp-card-lead')).toBe(false)
    expect(container.querySelector('.exp-lead-mark')).toBeNull()
    expect(card.firstElementChild?.className).toContain('pr-3')
    expect(screen.queryByText('线索已关闭')).toBeNull()
  })

  it('不是线索的观测：什么标记都没有', () => {
    const { card, container } = renderCard(fact())
    expect(card.classList.contains('exp-card-lead')).toBe(false)
    expect(container.querySelector('.exp-lead-mark')).toBeNull()
    expect(screen.queryByText('线索已关闭')).toBeNull()
  })
})

// 加急：规划者标的动作在卡片头部标着色的「加急」（警示琥珀，不用中性白）；其余动作没有。
describe('ExplorationNodeCard 的加急标记', () => {
  const action = (over: Partial<GraphNode>): GraphNode =>
    ({ ...fact(), kind: 'action', state: 'open', source_type: 'planner', content: { instruction: '先续会话' }, ...over }) as GraphNode

  it('加急的着色：警示琥珀（text-status-warning），不用中性白', () => {
    const { card } = renderCard(action({ urgent: true }))
    const chip = within(card).getByText('加急')
    expect(chip.className).toContain('text-status-warning')
    expect(chip.className).not.toContain('text-text')
  })

  it('加急的动作标「加急」，悬停说明它为什么先派', () => {
    const { card } = renderCard(action({ urgent: true }))
    const mark = within(card).getByText('加急')
    expect(mark.getAttribute('title')).toContain('空位不够时先派')
  })

  it('没有加急的动作没有这个标记', () => {
    const { card } = renderCard(action({}))
    expect(within(card).queryByText('加急')).toBeNull()
  })
})

