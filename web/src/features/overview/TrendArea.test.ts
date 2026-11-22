import { describe, expect, it } from 'vitest'
import { smoothPaths } from './TrendArea'

/** 三次贝塞尔 y 值采样（de Casteljau）。 */
function sampleY([y0, y1, y2, y3]: [number, number, number, number]): number[] {
  const out: number[] = []
  for (let i = 1; i < 20; i++) {
    const t = i / 20
    const u = 1 - t
    out.push(u * u * u * y0 + 3 * u * u * t * y1 + 3 * u * t * t * y2 + t * t * t * y3)
  }
  return out
}

describe('smoothPaths（趋势平滑曲线）', () => {
  // 与实测一致的数据形态：长期零值、尾部尖峰（近 7 天 / 14 天新增发现的形状）。
  const spike = [0, 0, 0, 0, 0, 26, 30, 0, 0]

  it('曲线圆滑穿过全部数据点（插值）', () => {
    const { segments } = smoothPaths(spike, 30)
    expect(segments.length).toBe(spike.length - 1)
    segments.forEach(([start], i) => {
      expect(start).toBeCloseTo(20 + (1 - spike[i] / 30) * 80, 6) // 顶部余量 20%
    })
  })

  it('C1 连续：每个数据点的进出切线相等（无棱角）', () => {
    const { segments } = smoothPaths(spike, 30)
    for (let i = 1; i < segments.length; i++) {
      const [, , prevOut, y] = segments[i - 1]
      const nextIn = segments[i][1] // 段起点处的控制点 cy1（与上一段 cy2 共享同一条切线）
      const tangentIn = (3 * (y - prevOut)) / (100 / (spike.length - 1))
      const tangentOut = (3 * (nextIn - y)) / (100 / (spike.length - 1))
      expect(tangentOut).toBeCloseTo(tangentIn, 6)
    }
  })

  it('过冲有界：控制点与曲线采样不冲出图顶（顶部不被裁切），下潜不超过理论下界', () => {
    // 顶部余量 20% 时，Catmull–Rom 的最坏控制点 = 20 - 80/6 ≈ 6.7 ≥ 0——曲线永不冲出图顶
    for (const values of [
      [0, 0, 0, 0, 0, 26, 30, 0, 0],
      [30, 0, 0, 0, 0, 0, 26, 0, 0],
      [30, 26, 30, 0, 0, 26, 30, 26, 30],
      [3, 1, 4, 1, 5, 9, 2, 6, 5],
    ]) {
      const { segments } = smoothPaths(values, 30)
      for (const seg of segments) {
        for (const y of [seg[1], seg[2]]) {
          expect(y).toBeGreaterThanOrEqual(0) // 不冲出图顶 → 不会被裁平
        }
        for (const y of sampleY(seg)) {
          expect(y).toBeGreaterThanOrEqual(-1e-9) // 基线以下的下潜被 SVG 裁切，不可见
          expect(y).toBeLessThanOrEqual(100 + (100 - 20) / 6 + 1e-9) // 下潜的理论下界
        }
      }
    }
  })
})
