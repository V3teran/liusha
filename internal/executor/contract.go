package executor

import _ "embed"

// contract 是执行输出契约：ParseOutput 依赖的终稿 JSON 形状、observation 分类与
// repro 域信封规则，以及系统收集交付物所依赖的 $OUTPUT_DIR 约定。它与解析代码
// 同包同改同测，不属于可自定义的章程。
//
//go:embed contract.md
var contract string
