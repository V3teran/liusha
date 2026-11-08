import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { Button } from './Button'

describe('Button', () => {
  it('默认 type=button，避免在表单内误触提交', () => {
    render(<Button>保存</Button>)
    expect(screen.getByRole('button', { name: '保存' })).toHaveAttribute('type', 'button')
  })

  it('显式 type=submit 时透传', () => {
    render(<Button type="submit">登录</Button>)
    expect(screen.getByRole('button', { name: '登录' })).toHaveAttribute('type', 'submit')
  })

  it('点击触发 onClick；disabled 时不触发', () => {
    const onClick = vi.fn()
    const { rerender } = render(<Button onClick={onClick}>新建</Button>)
    fireEvent.click(screen.getByRole('button'))
    expect(onClick).toHaveBeenCalledTimes(1)
    rerender(
      <Button onClick={onClick} disabled>
        新建
      </Button>,
    )
    fireEvent.click(screen.getByRole('button'))
    expect(onClick).toHaveBeenCalledTimes(1)
  })

  it('尺寸档位与控件体系同高（md h-9 / sm h-8）', () => {
    const { rerender } = render(<Button>a</Button>)
    expect(screen.getByRole('button').className).toContain('h-9')
    rerender(<Button size="sm">a</Button>)
    expect(screen.getByRole('button').className).toContain('h-8')
  })

  it('variant 决定视觉层级：primary 用强调色，secondary 为描边', () => {
    const { rerender } = render(<Button variant="primary">a</Button>)
    expect(screen.getByRole('button').className).toContain('text-accent')
    rerender(<Button variant="secondary">a</Button>)
    expect(screen.getByRole('button').className).toContain('border-border')
  })
})
