package planner

import _ "embed"

// contract 是规划输出契约：parsePlanningResponse 依赖的 JSON 形状。它与解析代码
// 同包同改同测，不属于可自定义的章程。
//
//go:embed contract.md
var contract string
