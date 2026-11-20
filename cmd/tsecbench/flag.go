package main

import (
	"fmt"
	"regexp"
	"strings"
)

// defaultFlagPattern 是 flag 的默认匹配格式。TSecBench v1 题型杂，多数 CTF flag 形如
// flag{...}；拿不准的格式由题目 description 提示后经 LIUSHA_TSB_FLAG_RE 覆盖，或直接把
// 候选 submit 让平台判定（submit 幂等、错误不扣分）。
//
// 花括号内不允许空白与引号：探索图正文里有大量 agent 的推理与命令文本，
// 「grep 'flag{' 于 /home ... }」这类片段在宽松模式下会被当成 flag 提交。
const defaultFlagPattern = `flag\{[^\s{}'"]{1,512}\}`

// placeholderChars 是格式示例里的占位字符：flag{...}、flag{xxx}、flag{***} 不是 flag。
// 目标说明本身就写着「形如 flag{...}」，agent 复述时会出现在探索图里。
const placeholderChars = ".…*xX?"

// flagExtractor 从自由文本里提取 flag 候选。来源文本按喂入顺序扫描（solver 喂入探索图
// result 节点正文，flag 经复现门坐实成 result），结果去重保序，
// solver 据此依次提交。
type flagExtractor struct {
	re *regexp.Regexp
}

// newFlagExtractor 用给定正则构造提取器；pattern 为空时用 defaultFlagPattern。
// 非法正则直接报错，不静默降级（否则会悄悄匹配不到任何 flag）。
func newFlagExtractor(pattern string) (*flagExtractor, error) {
	if pattern == "" {
		pattern = defaultFlagPattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("编译 flag 正则 %q: %w", pattern, err)
	}
	return &flagExtractor{re: re}, nil
}

// scan 按顺序扫描多段文本，返回去重保序的 flag 候选（滤掉格式占位）。
// 空切片而非 nil 由调用方无差别处理。
func (e *flagExtractor) scan(texts ...string) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0)
	for _, text := range texts {
		for _, m := range e.re.FindAllString(text, -1) {
			if _, dup := seen[m]; dup || isPlaceholder(m) {
				continue
			}
			seen[m] = struct{}{}
			out = append(out, m)
		}
	}
	return out
}

// isPlaceholder 判断命中是否只是格式占位：取最外层花括号内的内容（无花括号取整体），
// 全由占位字符组成即为占位。
func isPlaceholder(m string) bool {
	inner := m
	if i, j := strings.Index(m, "{"), strings.LastIndex(m, "}"); i >= 0 && j > i {
		inner = m[i+1 : j]
	}
	return strings.Trim(inner, placeholderChars) == ""
}
