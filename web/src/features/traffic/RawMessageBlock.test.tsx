import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { RawMessageBlock } from './RawMessageBlock'

const JSON_REQ = 'POST /api HTTP/1.1\r\nContent-Type: application/json\r\n\r\n{"a":1}'

describe('RawMessageBlock', () => {
  // axe 实测：tab 触发器的 aria-controls 指向不存在的面板（报文 pre 不在 tabpanel 里）。
  it('选中 tab 的 aria-controls 指向承载报文的 tabpanel', () => {
    render(<RawMessageBlock title="请求" raw={JSON_REQ} emptyHint="无" />)
    const tab = screen.getByRole('tab', { name: '原文' })
    const panel = document.getElementById(tab.getAttribute('aria-controls') ?? '')
    expect(panel?.getAttribute('role')).toBe('tabpanel')
    expect(panel?.textContent).toContain('{"a":1}')
    expect(panel?.tabIndex).toBe(0)
  })

  it('切到美化后面板随之切换并格式化 body', async () => {
    const user = userEvent.setup()
    render(<RawMessageBlock title="请求" raw={JSON_REQ} emptyHint="无" />)
    const tab = screen.getByRole('tab', { name: '美化' })
    await user.click(tab)
    const panel = document.getElementById(tab.getAttribute('aria-controls') ?? '')
    expect(panel?.getAttribute('role')).toBe('tabpanel')
    expect(panel?.textContent).toContain('"a": 1')
  })
})
