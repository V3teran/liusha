import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { LeadMarkFollowed, LeadMark } from './LeadMark'
import { LEAD_ACCENT } from './kindTheme'

// 浏览器把颜色序列化成 rgb()：拿一个设了同一颜色的探针来比，不写死格式。
function asBrowserColor(color: string, property: 'background' | 'color'): string {
  const probe = document.createElement('span')
  probe.style[property] = color
  return probe.style[property]
}

describe('LeadMark', () => {
  it('标记是一串脚印：踩通了、还没走到头（旗子留给别的概念，不在这里用）', () => {
    const { container } = render(<LeadMark />)
    expect(container.querySelector('.lucide-footprints')).not.toBeNull()
    expect(container.querySelector('.lucide-flag')).toBeNull()
  })

  it('脚印是实心的；底色透明；取线索自己的颜色', () => {
    const { container } = render(<LeadMark title="执行者已踩通、尚未推进到结论的路径" />)
    const mark = container.querySelector('.exp-lead-mark') as HTMLElement
    expect(mark.style.background).toBe('')
    expect(mark.style.border).toBe('')
    const icon = mark.querySelector('svg') as SVGElement
    expect(icon.style.color).toBe(asBrowserColor(LEAD_ACCENT, 'color'))
    expect(icon.getAttribute('fill')).toBe('currentColor')
    // 读屏读到「线索」；悬停说明写明它是什么
    expect(screen.getByText('线索')).toBeTruthy()
    expect(mark.getAttribute('title')).toBe('执行者已踩通、尚未推进到结论的路径')
  })

  it('图例里的样例对读屏隐藏：旁边的文字就是它的名字，不重复读', () => {
    render(
      <span>
        <LeadMark aria-hidden />
        线索
      </span>,
    )
    // 文本查询不按 aria-hidden 过滤：读屏看得到的是「不被 aria-hidden 罩住」的那些
    const announced = screen.getAllByText('线索', { ignore: '[aria-hidden="true"], [aria-hidden="true"] *' })
    expect(announced).toHaveLength(1)
  })
})

describe('LeadMarkFollowed', () => {
  it('被跟进过且已关闭的线索保留脚印、换中性色：不再是线索自己的暖橙', () => {
    const { container } = render(<LeadMarkFollowed title="已走过的线索（已走到头：之前的动作已做过）" />)
    const mark = container.querySelector('.exp-lead-mark') as HTMLElement
    expect(mark.querySelector('.lucide-footprints')).not.toBeNull()
    expect(mark.classList.contains('text-text')).toBe(true)
    const icon = mark.querySelector('svg') as SVGElement
    expect(icon.style.color).toBe('')
    expect(icon.getAttribute('fill')).toBe('currentColor')
    // 读屏读到「线索（已走过）」；悬停说明带关闭的类别与理由
    expect(screen.getByText('线索（已走过）')).toBeTruthy()
    expect(mark.getAttribute('title')).toBe('已走过的线索（已走到头：之前的动作已做过）')
  })
})
