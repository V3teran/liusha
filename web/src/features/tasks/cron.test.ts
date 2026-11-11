import { describe, expect, it } from 'vitest'
import { cronHuman } from './cron'

describe('cronHuman', () => {
  it.each([
    ['0 9 * * *', '每天 09:00'],
    ['30 4 * * 1', '每周一 04:30'],
    ['0 4 * * 7', '每周日 04:00'],
    ['5 23 15 * *', '每月 15 日 23:05'],
    ['*/5 * * * *', '*/5 * * * *'],
    ['0 9 * *', '无效表达式'],
  ])('%s → %s', (expr, want) => {
    expect(cronHuman(expr)).toBe(want)
  })
})
