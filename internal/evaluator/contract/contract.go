// Package contract 承载裁决契约：重放通道语义（web 域机器重放 / generic 域自主取证）
// 与 evaluator.parseVerdict 依赖的裁决 JSON 形状。契约与解析代码同处一棵目录、
// 同改同测，不属于可自定义的章程；独立成叶子包只为让展示方（api 配置页）
// 读取原文时不依赖裁决实现。
package contract

import _ "embed"

// Text 是契约原文，拼接在章程之后进入 system prompt。
//
//go:embed contract.md
var Text string
