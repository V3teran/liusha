import { afterEach, describe, expect, it, vi } from 'vitest'
import { copyText } from './clipboard'

describe('copyText', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('空字符串直接返回 false', async () => {
    expect(await copyText('')).toBe(false)
  })

  it('navigator.clipboard 可用时走现代路径', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
    expect(await copyText('hello')).toBe(true)
    expect(writeText).toHaveBeenCalledWith('hello')
  })

  it('navigator.clipboard 抛错时回退 execCommand', async () => {
    const writeText = vi.fn().mockRejectedValue(new Error('denied'))
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
    document.execCommand = vi.fn().mockReturnValue(true)
    expect(await copyText('fallback')).toBe(true)
    expect(document.execCommand).toHaveBeenCalledWith('copy')
  })

  it('navigator.clipboard 不存在时走 execCommand', async () => {
    Object.defineProperty(navigator, 'clipboard', { value: undefined, configurable: true })
    document.execCommand = vi.fn().mockReturnValue(true)
    expect(await copyText('noclip')).toBe(true)
  })

  it('execCommand 失败时返回 false', async () => {
    Object.defineProperty(navigator, 'clipboard', { value: undefined, configurable: true })
    document.execCommand = vi.fn().mockReturnValue(false)
    expect(await copyText('fails')).toBe(false)
  })

  it('execCommand 抛错时返回 false', async () => {
    Object.defineProperty(navigator, 'clipboard', { value: undefined, configurable: true })
    document.execCommand = vi.fn(() => {
      throw new Error('boom')
    })
    expect(await copyText('throws')).toBe(false)
  })
})
