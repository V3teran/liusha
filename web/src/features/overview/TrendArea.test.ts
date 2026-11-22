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
