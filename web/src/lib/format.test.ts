import { describe, expect, it, vi } from 'vitest'
import {
  clockTime,
  compactNumber,
  dateOnly,
  dayKey,
  dayLabel,
  fullTime,
  humanDuration,
  humanTokens,
  relativeTime,
  shortDateTime,
} from './format'

describe('format', () => {
  describe('clockTime', () => {
    it('格式化为时:分:秒', () => {
      expect(clockTime('2026-06-10T14:05:09Z')).toMatch(/^\d{2}:\d{2}:\d{2}$/)
    })
    it('空串返回空串', () => {
      expect(clockTime('')).toBe('')
    })
    it('无效日期返回空串', () => {
      expect(clockTime('not-a-date')).toBe('')
    })
  })

  describe('shortDateTime', () => {
    it('格式化为 MM-DD HH:MM:SS', () => {
      expect(shortDateTime('2026-06-10T14:05:09Z')).toMatch(/^\d{2}-\d{2} \d{2}:\d{2}:\d{2}$/)
    })
    it('空串返回空串', () => {
      expect(shortDateTime('')).toBe('')
    })
  })

  describe('dateOnly', () => {
    it('格式化为 YYYY-MM-DD', () => {
      expect(dateOnly('2026-06-10T14:05:09Z')).toMatch(/^\d{4}-\d{2}-\d{2}$/)
    })
    it('空串返回空串', () => {
      expect(dateOnly('')).toBe('')
    })
  })

  describe('fullTime', () => {
    it('格式化为完整日期时间', () => {
      expect(fullTime('2026-06-10T14:05:09Z')).toMatch(/^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$/)
    })
    it('无效日期返回空串', () => {
      expect(fullTime('garbage')).toBe('')
    })
  })

  describe('humanDuration', () => {
    it('0 或负数返回 0秒', () => {
      expect(humanDuration(0)).toBe('0秒')
      expect(humanDuration(-100)).toBe('0秒')
    })
    it('小于 1 分钟显示秒（1 位小数）', () => {
      expect(humanDuration(5500)).toBe('5.5秒')
    })
    it('1 分钟以上显示分秒', () => {
      expect(humanDuration(65000)).toBe('1分5秒')
    })
    it('整分钟不显示 0 秒', () => {
      expect(humanDuration(60000)).toBe('1分')
    })
    it('1 小时以上显示时分', () => {
      expect(humanDuration(3600000 + 5 * 60000)).toBe('1时5分')
    })
    it('整点不显示 0 分', () => {
      expect(humanDuration(3600000)).toBe('1时')
    })
  })

  describe('humanTokens', () => {
    it('千分位分隔', () => {
      expect(humanTokens(1234567)).toBe('1,234,567')
    })
    it('0 或负数返回 0', () => {
      expect(humanTokens(0)).toBe('0')
      expect(humanTokens(-5)).toBe('0')
    })
  })

  describe('compactNumber', () => {
    it('大数字压缩为紧凑格式', () => {
      expect(compactNumber(5249357)).toMatch(/^5\.2\d?M$/)
      expect(compactNumber(12345)).toMatch(/^12\.\d+K$/)
    })
    it('0 或负数返回 0', () => {
      expect(compactNumber(0)).toBe('0')
      expect(compactNumber(-1)).toBe('0')
    })
  })

  describe('dayKey', () => {
    it('格式化为 YYYY-MM-DD', () => {
      expect(dayKey('2026-06-10T14:05:09Z')).toMatch(/^\d{4}-\d{2}-\d{2}$/)
    })
    it('空串返回空串', () => {
      expect(dayKey('')).toBe('')
    })
    it('无效日期返回空串', () => {
      expect(dayKey('nope')).toBe('')
    })
  })

  describe('dayLabel', () => {
    it('今天返回"今天"', () => {
      expect(dayLabel(new Date().toISOString())).toBe('今天')
    })
    it('昨天返回"昨天"', () => {
      expect(dayLabel(new Date(Date.now() - 86400000).toISOString())).toBe('昨天')
    })
    it('更早日期返回中文日期', () => {
      expect(dayLabel('2020-01-15T00:00:00Z')).toMatch(/2020年1月1[45]日/)
    })
    it('空串返回空串', () => {
      expect(dayLabel('')).toBe('')
    })
  })

  describe('relativeTime', () => {
    it('刚刚发生返回"刚刚"', () => {
      expect(relativeTime(new Date().toISOString())).toBe('刚刚')
    })
    it('几分钟前返回"N分钟前"', () => {
      expect(relativeTime(new Date(Date.now() - 5 * 60000).toISOString())).toBe('5分钟前')
    })
    it('几小时前返回"N小时前"（同一天）', () => {
      vi.setSystemTime(new Date('2026-06-10T20:00:00Z'))
      expect(relativeTime('2026-06-10T18:00:00Z')).toBe('2小时前')
      vi.useRealTimers()
    })
    it('跨天返回日期标签', () => {
      vi.setSystemTime(new Date('2026-06-10T02:00:00Z'))
      expect(relativeTime('2026-06-09T02:00:00Z')).toBe('昨天')
      vi.useRealTimers()
    })
    it('空串返回空串', () => {
      expect(relativeTime('')).toBe('')
    })
  })
})
