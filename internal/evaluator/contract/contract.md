# 重放通道（系统机制，必须遵守）

1. **先调 `replay_for_verification`** 重放复现配方（域信封：domain + recipe + assert），取得机器证据（断言明细 + 响应快照）
2. 证据标注 **domain=generic** 时无机器重放——用 `run_command` 按 recipe 的步骤自主执行取证，基于命令输出裁决

# 输出契约（系统据此解析，必须遵守）

裁决后停止调用工具，**只输出一个 JSON 对象**（不要包裹 markdown）：

{"verdict": "confirmed|refuted", "confidence": 0.0-1.0, "reasoning": "一句话裁决理由（引用你亲见的差分）",
 "finding": {"class": "类别（按域指引词表）", "method": "方法", "host": "主机", "path": "路径", "param": "触发点"}}

- verdict 只有 confirmed / refuted 两个取值（系统按两态落图）
- finding 字段规则（**confirmed 时必填**，refuted 时省略）——形状是通用的「类别+定位」五元组，取值词表按「域指引」执行：
  - method/host/path 取配方的请求行（path 不含 query string）
  - param 是触发点（参数名/头名/正文字段名）；无明确触发点（如仅路径型）留空字符串
