// Package clip 提供"喂 LLM 前的智能剪裁"工具：保留 schema/keys 完整，截短 values。
//
// 设计意图：LLM 做语义判定时（如 classify_traffic）需要看到字段 keys 是否存在 +
// 数据结构形态，但不需要看到完整 value 内容。截短 value 能大幅省 token 而几乎
// 无信息损失（敏感 value 还应配合 Redact 单独处理）。
//
// 当前消费者：internal/tools/traffic/classify_traffic.go。
// 未来消费者：任何需要把"完整流量/响应/请求"喂 LLM 的工具。
package clip

import (
	"encoding/json"
	"fmt"
)

// String 把字符串截到 n 字节（按 byte 计数；非 UTF-8 安全粗截）。
//
// 超长追加 "..."；len(s) <= n 时原样返回。
func String(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// StringMap 对 map 中所有 value 截到 max 字节；nil 输入返 nil。
func StringMap(in map[string]string, max int) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = String(v, max)
	}
	return out
}

// JSONValue 递归遍历 JSON 值（map/slice/string/数字/bool），把所有 string 截到 max；
// 其他类型保留。便于把"含大段长字符串的 JSON 树"压成"keys 全留 + values 截短"形态。
func JSONValue(v any, max int) any {
	switch t := v.(type) {
	case string:
		return String(t, max)
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = JSONValue(val, max)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = JSONValue(val, max)
		}
		return out
	default:
		return v
	}
}

// RequestBody 处理请求 body：
//   - 空 → `{}`
//   - 合法 JSON → 解析后递归 JSONValue(maxJSONValueLen)，重新 marshal
//   - 非 JSON（form/二进制等）→ 截 maxRawBytes 字节后包成 {"_raw":"..."}
//
// 返回 json.RawMessage 让上层直接放进 JSON tree（无需再 marshal）。
func RequestBody(body []byte, maxJSONValueLen, maxRawBytes int) json.RawMessage {
	if len(body) == 0 {
		return json.RawMessage(`{}`)
	}
	var parsed any
	if err := json.Unmarshal(body, &parsed); err == nil {
		shrunk := JSONValue(parsed, maxJSONValueLen)
		if out, err := json.Marshal(shrunk); err == nil {
			return out
		}
	}
	raw := String(string(body), maxRawBytes)
	out, _ := json.Marshal(map[string]string{"_raw": raw})
	return out
}

// ResponseBody 截响应 body：
//   - len(body) <= max → 原样返回
//   - 否则 前 (max-tail) 字节 + "...<truncated>..." + 尾 tail 字节
//
// 设计：保留前缀 + 尾部能同时捕获响应开头（数据形态）和尾部（分页/总数等关键 hint）。
// tail >= max 时退化为全前缀截断（fail-safe）。
func ResponseBody(body []byte, max, tail int) string {
	if len(body) == 0 {
		return ""
	}
	if len(body) <= max {
		return string(body)
	}
	if tail >= max {
		return string(body[:max])
	}
	headLen := max - tail
	return string(body[:headLen]) + "...<truncated>..." + string(body[len(body)-tail:])
}

// Redact 把任意字符串替换为 "<redacted len=N>"，N 为原长度。
// 用于敏感 header value（Cookie/Authorization 等），让 LLM 看到字段存在但拿不到真凭证。
func Redact(s string) string {
	return fmt.Sprintf("<redacted len=%d>", len(s))
}
