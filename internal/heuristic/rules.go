// Package heuristic 提供 BAC 检测的辅助规则与结构相似度算法。
//
// Rule 用于在多身份重放后做"是否需要继续 LLM 判定"的快速过滤；
// 当所有响应都被拒绝、都为空、或都返回鉴权错误时，直接 skip 可省一次 LLM 调用。
package heuristic

import (
	"bytes"
	"strings"

	"github.com/V3teran/liusha/internal/replay"
)

// Rule 接受多身份的重放响应，返回 (是否跳过, 原因)；reason 仅在 skip=true 时有意义。
type Rule func(responses []replay.Response) (skip bool, reason string)

// AllDeniedByStatus 当所有响应的 StatusCode >= 400 时返回 skip=true。
// 空切片视为 true（无可比较内容，按"全部拒绝"处理由调用方决策）。
func AllDeniedByStatus(rs []replay.Response) (bool, string) {
	for _, r := range rs {
		if r.StatusCode < 400 {
			return false, ""
		}
	}
	return true, "all responses are 4xx/5xx"
}

// AllEmptyResponse 当所有响应的 body 视为空（""、"{}"、"[]"）时返回 skip=true。
// 仅去前后空白；不解析 JSON 字段，避免引入 unmarshal 成本。
func AllEmptyResponse(rs []replay.Response) (bool, string) {
	for _, r := range rs {
		body := bytes.TrimSpace(r.Body)
		s := string(body)
		if len(body) > 0 && s != "{}" && s != "[]" {
			return false, ""
		}
	}
	return true, "all response bodies are empty"
}

// DefaultAuthKeywords 是 AllAuthError 默认匹配的鉴权关键词（25 个，spec §7.3）。
// yaml 配置可通过传入自定义 keywords 列表扩展或替换。
var DefaultAuthKeywords = []string{
	// 中文（11）
	"未登录", "请登录", "登录后", "需要登录", "请先登录",
	"无权访问", "权限不足", "未授权", "无权操作", "拒绝访问", "禁止访问",
	// 中英混合（3）
	"会话过期", "token 已过期", "token expired",
	// 英文（11）
	"login required", "please login", "not authorized", "unauthorized",
	"forbidden", "permission denied", "access denied",
	"session expired", "invalid token", "authentication required", "auth required",
}

// AllAuthError 当所有响应的 body 都包含至少一个鉴权关键词时返回 skip=true。
// keywords 为 nil 或空时使用 DefaultAuthKeywords；匹配大小写不敏感。
func AllAuthError(rs []replay.Response, keywords []string) (bool, string) {
	if len(keywords) == 0 {
		keywords = DefaultAuthKeywords
	}
	// 预转小写，避免每次循环重复分配
	lowered := make([]string, len(keywords))
	for i, k := range keywords {
		lowered[i] = strings.ToLower(k)
	}
	for _, r := range rs {
		text := strings.ToLower(string(r.Body))
		hit := false
		for _, k := range lowered {
			if strings.Contains(text, k) {
				hit = true
				break
			}
		}
		if !hit {
			return false, ""
		}
	}
	return true, "all responses contain auth-error keyword"
}
