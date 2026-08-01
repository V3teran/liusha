import { describe, expect, it } from 'vitest'
import { render } from '@testing-library/react'
import { AssistantText } from './AssistantText'

describe('AssistantText', () => {
  it('渲染 markdown 内容', () => {
    const { container } = render(<AssistantText content="**加粗** 文本" />)
    expect(container.querySelector('strong')?.textContent).toBe('加粗')
  })

  it('空内容不崩', () => {
    const { container } = render(<AssistantText content="" />)
    expect(container.firstElementChild).toBeTruthy()
  })
})
