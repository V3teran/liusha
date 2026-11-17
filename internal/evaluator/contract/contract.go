// Package contract 承载判定契约：复现通道语义（按配方 kind：http 机器复现 / steps 自主取证）
// 与 evaluator.parseVerdict 依赖的判定 JSON 形状。契约与解析代码同处一棵目录、
// 同改同测，不属于可自定义的章程；独立成叶子包只为让展示方（api 配置页）
// 读取原文时不依赖判定实现。
package contract

import _ "embed"

// Text 是契约原文，拼接在章程之后进入 system prompt。
//
//go:embed contract.md
var Text string
