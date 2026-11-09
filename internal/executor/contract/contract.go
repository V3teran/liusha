// Package contract 承载执行输出契约：executor.ParseOutput 依赖的终稿 JSON 形状、
// observation 分类与 repro 域信封规则，以及系统收集交付物所依赖的 $OUTPUT_DIR 约定。
// 契约与解析代码同处一棵目录、同改同测，不属于可自定义的章程；独立成叶子包
// 只为让展示方（api 配置页）无需引入 executor 的依赖树即可读取原文。
package contract

import _ "embed"

// Text 是契约原文，拼接在章程之后进入 system prompt。
//
//go:embed contract.md
var Text string
