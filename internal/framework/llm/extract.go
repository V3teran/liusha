package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// ExtractJSON 从 LLM 自由文本回复中截取 JSON 对象正文：取首个 '{' 到末个 '}' 之间
// 的内容。markdown 代码围栏（```json … ```）、前后闲聊都在截取中被剥掉。
// 无 JSON 时返回空串，调用方按解析失败处理。
// planner 规划解析、evaluator 裁决解析、result 分析解析共用本实现。
func ExtractJSON(content string) string {
	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")
	if start == -1 || end <= start {
		return ""
	}
	return content[start : end+1]
}

// ParseJSON 解析 LLM 输出中的 JSON：先按 RFC 8259 严格解析；失败时执行一次
// 机械修复再重试。修复只处理模型高频失手且可确定性纠正的三类问题——
// 字符串内未转义的反斜杠（如 Windows 路径 ..\..\x）、字符串内裸控制字符、
// 对象/数组末尾的逗号。修复只作用于已确认非法的输入，合法 JSON 永远走
// 严格路径原样解析，不存在误伤面。
//
// 模型把「内容」和「编码」混在一起是结构化输出的固有噪声：解析层对编码
// 容错、对语义（未知字段/枚举越界）保持严格，是两层职责的干净分界。
func ParseJSON(content string, v any) error {
	jsonBody := ExtractJSON(content)
	if jsonBody == "" {
		return fmt.Errorf("LLM 响应不包含有效 JSON")
	}
	return UnmarshalLoose([]byte(jsonBody), v)
}

// UnmarshalLoose 是 ParseJSON 的无提取变体：对已经截取好的 JSON 候选文本
// 做「严格优先、失败修复重试」解析。供调用方自行圈定候选（如执行器逐块
// 扫描 ReAct 输出）时复用同一修复层。
func UnmarshalLoose(data []byte, v any) error {
	if err := json.Unmarshal(data, v); err == nil {
		return nil
	}
	return json.Unmarshal(repairJSON(data), v)
}

// SanitizeJSON 把模型产出的 JSON 参数归一为合法字节：已合法原样返回（零拷贝
// 快路径），非法走机械修复。工具调用参数是模型生成的 JSON，转义噪声（裸反
// 斜杠、裸控制符、尾逗号）在运行时漏斗处一次性洗掉，各工具实现无需各自防御。
func SanitizeJSON(data []byte) []byte {
	if json.Valid(data) {
		return data
	}
	return repairJSON(data)
}

// repairJSON 对解析失败的 JSON 做确定性修复。逐字节扫描并跟踪字符串边界：
// 字符串内的非法转义（\x，x ∉ "\/bfnrtu"）补一个反斜杠变成字面量、裸控制
// 字符转成 \uXXXX、结构性尾逗号剔除；字符串外的字节原样保留。
func repairJSON(data []byte) []byte {
	var out bytes.Buffer
	out.Grow(len(data) + 16)
	inString := false
	escaped := false
	for i := 0; i < len(data); i++ {
		c := data[i]
		if !inString {
			if c == ',' {
				// 结构性尾逗号：跳过空白后遇 } 或 ] 则剔除该逗号
				j := i + 1
				for j < len(data) && (data[j] == ' ' || data[j] == '\t' || data[j] == '\n' || data[j] == '\r') {
					j++
				}
				if j < len(data) && (data[j] == '}' || data[j] == ']') {
					continue
				}
			}
			if c == '"' {
				inString = true
			}
			out.WriteByte(c)
			continue
		}
		// 字符串内
		if escaped {
			escaped = false
			out.WriteByte(c)
			continue
		}
		switch c {
		case '\\':
			if i+1 < len(data) && strings.ContainsRune(`"\/bfnrtu`, rune(data[i+1])) {
				escaped = true
				out.WriteByte(c)
				continue
			}
			// 非法转义：反斜杠翻倍，下一个字节按普通字符输出
			out.WriteString(`\\`)
		case '"':
			inString = false
			out.WriteByte(c)
		default:
			if c < 0x20 {
				out.WriteString(fmt.Sprintf(`\u%04x`, c))
				continue
			}
			out.WriteByte(c)
		}
	}
	return out.Bytes()
}
