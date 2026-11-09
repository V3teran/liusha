package evaluator

import _ "embed"

// contract 是裁决契约：重放通道语义（web 域机器重放 / generic 域自主取证）与
// parseVerdict 依赖的裁决 JSON 形状。它与解析代码同包同改同测，不属于可自定义的章程。
//
//go:embed contract.md
var contract string
