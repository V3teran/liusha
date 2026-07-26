import { describe, it, expect } from 'vitest'
import {
  LLM_TIMEOUT_MS,
  durationLevel,
  isTimeout,
  latencyLevel,
  throughputLabel,
  throughputLevel,
  ttftLevel,
} from './llmTiming'

describe('throughputLevel', () => {
  it('按输出速率分三档', () => {
    expect(throughputLevel(45)).toBe('good')
    expect(throughputLevel(30)).toBe('good') // 边界含等号
    expect(throughputLevel(20)).toBe('warn')
    expect(throughputLevel(15)).toBe('warn')
    expect(throughputLevel(14.9)).toBe('bad')
  })
})

describe('durationLevel', () => {
  it('按纯耗时秒数分三档', () => {
    expect(durationLevel(1)).toBe('good')
    expect(durationLevel(4.9)).toBe('good')
    expect(durationLevel(5)).toBe('warn') // 边界不含
    expect(durationLevel(9.9)).toBe('warn')
    expect(durationLevel(10)).toBe('bad')
  })
})

describe('latencyLevel', () => {
  // 长输出耗时久是正常的，不该判成慢——这是"按吞吐而非纯耗时"的意义所在。
  it('输出足够多时按吞吐判，长输出不误判为慢', () => {
    // 20 秒输出 1000 token = 50 t/s，虽然耗时 20s（纯耗时会判 bad）但吞吐很好
    expect(latencyLevel(20_000, 1000)).toBe('good')
    // 20 秒只输出 200 token = 10 t/s，吞吐差
    expect(latencyLevel(20_000, 200)).toBe('bad')
  })

  it('输出太少时吞吐无意义，退化为按纯耗时判', () => {
    // 3 秒 / 10 token：按吞吐是 3.3 t/s（bad），但输出太少不该这么判 → 按耗时 3s = good
    expect(latencyLevel(3_000, 10)).toBe('good')
    // 12 秒 / 10 token：耗时确实久
    expect(latencyLevel(12_000, 10)).toBe('bad')
  })

  it('零耗时退化为纯耗时判，不做除零', () => {
    expect(latencyLevel(0, 500)).toBe('good')
  })
})

describe('ttftLevel', () => {
  it('按首 token 耗时分档（与输出量无关）', () => {
    expect(ttftLevel(800)).toBe('good')
    expect(ttftLevel(6_000)).toBe('warn')
    expect(ttftLevel(15_000)).toBe('bad')
  })
})

describe('isTimeout', () => {
  it('达到看门狗上限即判为超时打点', () => {
    expect(isTimeout({ latency_ms: LLM_TIMEOUT_MS })).toBe(true)
    expect(isTimeout({ latency_ms: 300_003 })).toBe(true) // 真实数据里的超时值
    expect(isTimeout({ latency_ms: 8_000 })).toBe(false)
  })
})

describe('throughputLabel', () => {
  it('正常调用给出 t/s', () => {
    expect(throughputLabel({ latency_ms: 2_000, out_tokens: 100 })).toBe('50.0 t/s')
  })

  it('无输出或无耗时不给速率', () => {
    expect(throughputLabel({ latency_ms: 2_000, out_tokens: 0 })).toBe('')
    expect(throughputLabel({ latency_ms: 0, out_tokens: 100 })).toBe('')
  })

  // 超时打点的分母是被看门狗截断的时长，算出来的速率是假的。
  it('超时打点不给速率', () => {
    expect(throughputLabel({ latency_ms: 300_003, out_tokens: 500 })).toBe('')
  })
})
