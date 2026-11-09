// Package contract 承载规划输出契约：planner.parsePlanningResponse 依赖的 JSON 形状。
// 契约与解析代码同处一棵目录、同改同测，不属于可自定义的章程；独立成叶子包
// 只为让展示方（api 配置页）无需引入 planner 的依赖树即可读取原文。
package contract

import _ "embed"

// Text 是契约原文，拼接在章程之后进入 system prompt。
//
//go:embed contract.md
var Text string
