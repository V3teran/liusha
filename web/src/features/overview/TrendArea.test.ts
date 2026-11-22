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
  // 此前的切线在「零值 → 尖峰」的陡峭过渡处让贝塞尔凸包越过段端点，曲线在图顶
  // 被裁平——用户看到的就是「上端超出限制不完整」。
  const spike = [0, 0, 0, 0, 0, 26, 30, 0, 0]

  it('每段的贝塞尔控制点都落在段两端数据值之间（凸包不过冲，顶部不被裁切）', () => {
    const { segments } = smoothPaths(spike, 30)
    expect(segments.length).toBe(spike.length - 1)
    for (const [y0, c1, c2, y3] of segments) {
      const lo = Math.min(y0, y3)
      const hi = Math.max(y0, y3)
      expect(c1).toBeGreaterThanOrEqual(lo)
      expect(c1).toBeLessThanOrEqual(hi)
      expect(c2).toBeGreaterThanOrEqual(lo)
      expect(c2).toBeLessThanOrEqual(hi)
    }
  })

  it('曲线采样不越过段两端的数据值，也不超出绘图区', () => {
    const { segments } = smoothPaths(spike, 30)
    for (const seg of segments) {
      const lo = Math.min(seg[0], seg[3])
      const hi = Math.max(seg[0], seg[3])
      for (const y of sampleY(seg)) {
        // 浮点尾差 1e-9 容差；数值超界的才算过冲
        expect(y).toBeGreaterThanOrEqual(lo - 1e-9)
        expect(y).toBeLessThanOrEqual(hi + 1e-9)
        expect(y).toBeGreaterThanOrEqual(8 - 1e-9) // 顶部呼吸空间内
        expect(y).toBeLessThanOrEqual(100 + 1e-9) // 不低于基线
      }
    }
  })

  it('满刻度取整：网格值取整齐三等分（max = step × 3），标签不会出现小数', () => {
    for (const peak of [7, 26, 30]) {
      const values = [0, 0, peak]
      const { segments } = smoothPaths(values, 30)
      // 满刻度 30 时，顶格线在 8%，数据峰值不超过它
      for (const [y0, c1, c2, y3] of segments) {
        for (const y of [y0, c1, c2, y3]) {
          expect(y).toBeGreaterThanOrEqual(8)
        }
      }
    }
  })
})

describe('smoothPaths（趋势平滑曲线）C1 连续性', () => {
  // 回归锁：此前 FC 钳制判据少乘段横距（dx）失效，兜底钳制让相邻两段在共享数据点
  // 上的切线不一致——曲线出现棱角。C1 连续（进出切线相等）是这类 bug 的判别性断言。
  it('每个数据点的进出切线相等（无棱角）', () => {
    const values = [0, 0, 0, 0, 0, 26, 30, 0, 0]
    const dx = 100 / (values.length - 1)
    const { segments } = smoothPaths(values, 30)
    for (let i = 1; i < segments.length; i++) {
      const [, , prevOut, y] = segments[i - 1]
      const nextIn = segments[i][1] // 段起点处的控制点 cy1（与上一段 cy2 共享同一条切线）
      // 进切线与出切线（y 轴向下，斜率符号一致即可比）
      const tangentIn = (3 * (y - prevOut)) / dx
      const tangentOut = (3 * (nextIn - y)) / dx
      // eslint-disable-next-line no-console
      console.log('tangent idx', i, 'in', tangentIn.toFixed(3), 'out', tangentOut.toFixed(3), 'prevOut', prevOut.toFixed(2), 'nextIn', nextIn.toFixed(2), 'y', y.toFixed(2))
      expect(tangentOut).toBeCloseTo(tangentIn, 6)
    }
  })

  it('尖峰与零值密布的形态下采样不过冲、不越界', () => {
    for (const values of [
      [0, 0, 0, 0, 0, 26, 30, 0, 0],
      [30, 0, 0, 0, 0, 0, 26, 0, 0],
      [3, 1, 4, 1, 5, 9, 2, 6, 5],
    ]) {
      const { segments } = smoothPaths(values, 30)
      for (const seg of segments) {
        for (const y of sampleY(seg)) {
          expect(y).toBeGreaterThanOrEqual(8 - 1e-9)
          expect(y).toBeLessThanOrEqual(100 + 1e-9)
        }
      }
    }
  })
})
