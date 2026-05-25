// Package grounding 把 vision LLM 输出的"语义像素坐标"换算成截图真实像素。
//
// 背景：vision 模型 grounding 训练有 2 派坐标系——
//   - normalized_1000：Qwen-VL / Doubao / Gemini，输出 0-1000 归一化（spike test 实测 ~3px 精度）
//   - real_pixels：Anthropic Claude computer-use / OpenAI GPT-4o，直接给真实像素
//
// 工具层（browser_use 的 click/input action）按当前 hunter 所用 provider 的坐标系做换算。
// CoordSystem 字符串值与 config.ProviderConfig.GroundingCoordSystem 同枚举集合。
package grounding

import "fmt"

// CoordSystem 是坐标系枚举的字符串类型化。
// 取值集合与 config.ProviderConfig.GroundingCoordSystem 一致：
// "normalized_1000" / "real_pixels" / "normalized_100"。
type CoordSystem string

const (
	Normalized1000 CoordSystem = "normalized_1000"
	RealPixels     CoordSystem = "real_pixels"
	Normalized100  CoordSystem = "normalized_100"
)

// ToRealPixels 把 LLM 给的 (x, y) 按 system 规则换算成视口真实像素。
//
// 入参：
//   - x, y       : LLM 输出的坐标值（语义按 system 不同）
//   - system     : 坐标系（来自 provider 配置）
//   - viewportW  : 视口宽度（像素，sandbox chromium 启动时固定）
//   - viewportH  : 视口高度（像素）
//
// 出参：(realX, realY, err)；非法 system 或 viewport ≤ 0 返 err。
//
// 注：normalized 系不做范围裁剪——LLM 偶尔给 1010 / -5 也按比例算（最终送 browser 仍可能边界外失败，
// 但工具层不替 LLM 决策；err 留给 browser 命令层报）。
func ToRealPixels(x, y int, system CoordSystem, viewportW, viewportH int) (int, int, error) {
	if viewportW <= 0 || viewportH <= 0 {
		return 0, 0, fmt.Errorf("grounding.ToRealPixels: viewport 必须 > 0，got (%d, %d)", viewportW, viewportH)
	}
	switch system {
	case Normalized1000:
		return x * viewportW / 1000, y * viewportH / 1000, nil
	case Normalized100:
		return x * viewportW / 100, y * viewportH / 100, nil
	case RealPixels:
		return x, y, nil
	default:
		return 0, 0, fmt.Errorf("grounding.ToRealPixels: 未知 system %q（仅支持 normalized_1000 / real_pixels / normalized_100）", system)
	}
}

// Describe 返回 system 的中文描述，给 LLM tool description 用——
// 让 LLM 知道当前模型该输出什么范围的坐标值。
func Describe(system CoordSystem) string {
	switch system {
	case Normalized1000:
		return "归一化 0-1000（看截图估比例 × 1000，工具内部按视口换算）"
	case Normalized100:
		return "归一化 0-100 百分比"
	case RealPixels:
		return "真实像素值（参考截图宽高）"
	default:
		return "未知坐标系"
	}
}
