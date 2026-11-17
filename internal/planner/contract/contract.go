// Package contract 承载规划者的两份输出契约：常规规划（planner.parsePlanningResponse 依赖的
// JSON 形状）与结果分析（planner.callAnalysisLLM 依赖的 JSON 形状）。契约与解析代码同处一棵
// 目录、同改同测，不属于可自定义的章程；独立成叶子包只为让展示方（api 配置页）无需引入
// planner 的依赖树即可读取原文。
package contract

import _ "embed"

// Text 是常规规划的契约原文，拼接在章程之后进入规划的 system prompt。
//
//go:embed contract.md
var Text string

// AnalysisText 是结果分析的契约原文，拼接在章程之后进入结果分析的 system prompt。
//
//go:embed analysis.md
var AnalysisText string
